package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/irs269/eximphone/services/pkg/directory"
	"github.com/irs269/eximphone/services/pkg/server"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// closeStale closes every work day still open after local midnight at its site:
// departure time = last recorded activity, status "not_clocked". Returns affected days.
const closeStaleSQL = `
UPDATE attendance_days d SET
  clock_out    = GREATEST(d.last_activity_at, d.clock_in),
  departure    = 'not_clocked',
  duration_min = GREATEST(0, EXTRACT(EPOCH FROM (d.last_activity_at - d.clock_in)) / 60)::int
FROM users u JOIN sites s ON s.id = u.site_id
WHERE d.user_id = u.id AND d.clock_out IS NULL
  AND d.work_date < (now() AT TIME ZONE s.timezone)::date
RETURNING d.id, d.user_id, u.username::text, u.manager_id`

// syncDirectory mirrors the corporate directory into users: upserts profiles and deactivates
// accounts that disappeared from (or were disabled in) the directory.
func syncDirectory(ctx context.Context, db *pgxpool.Pool, dir directory.Client, log *slog.Logger) error {
	people, err := dir.ListAll()
	if err != nil {
		return err
	}
	if len(people) == 0 {
		return fmt.Errorf("directory returned no users; refusing to deactivate everyone")
	}
	names := make([]string, 0, len(people))
	for _, p := range people {
		names = append(names, p.Username)
		if _, err := db.Exec(ctx, `
			INSERT INTO users (username, display_name, email, extension, department_id, site_id)
			VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''),
			        (SELECT id FROM departments WHERE name = $5), (SELECT id FROM sites WHERE code = $6))
			ON CONFLICT (username) DO UPDATE SET display_name=EXCLUDED.display_name, email=EXCLUDED.email,
			  extension=EXCLUDED.extension, department_id=EXCLUDED.department_id, site_id=EXCLUDED.site_id, active=TRUE`,
			p.Username, p.DisplayName, p.Email, p.Extension, p.Department, p.SiteCode); err != nil {
			return err
		}
	}
	tag, err := db.Exec(ctx, `UPDATE users SET active=FALSE WHERE active AND NOT (username::text = ANY($1))`, names)
	if err != nil {
		return err
	}
	log.Info("directory synced", "users", len(people), "deactivated", tag.RowsAffected())
	return nil
}

// announceOffline tells every connected client that a user went offline (same event format as the api hub).
func announceOffline(ctx context.Context, rdb *redis.Client, userID int64) {
	if rdb == nil {
		return
	}
	rdb.HDel(ctx, "presence", fmt.Sprint(userID))
	data, _ := json.Marshal(map[string]any{"user_id": userID, "status": "offline"})
	b, _ := json.Marshal(map[string]any{"type": "presence", "data": json.RawMessage(data)})
	rdb.Publish(ctx, "eximphone:events", b)
}

func closeStale(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, log *slog.Logger) (int, error) {
	rows, err := db.Query(ctx, closeStaleSQL)
	if err != nil {
		return 0, err
	}
	type closed struct {
		day, user int64
		name      string
		manager   *int64
	}
	var list []closed
	for rows.Next() {
		var c closed
		if err := rows.Scan(&c.day, &c.user, &c.name, &c.manager); err != nil {
			return 0, err
		}
		list = append(list, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, c := range list {
		// Manager notification is delivered by the chat/notification system (phase 3);
		// until then the audit entry carries the manager id for that hook.
		_, _ = db.Exec(ctx, `INSERT INTO audit_log (actor_id, actor_name, action, details)
			VALUES ($1, $2, 'auto_close_not_clocked', jsonb_build_object('day_id', $3::bigint, 'manager_id', $4::bigint))`,
			c.user, c.name, c.day, c.manager)
		announceOffline(ctx, rdb, c.user)
		log.Info("closed day without clock-out", "day_id", c.day, "user_id", c.user)
	}
	return len(list), nil
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		server.Run("worker") // healthcheck probe or misconfiguration path
		return
	}
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatal(err)
	}
	var rdb *redis.Client
	if u := os.Getenv("REDIS_URL"); u != "" {
		if opt, err := redis.ParseURL(u); err == nil {
			rdb = redis.NewClient(opt)
		}
	}
	dir := directory.Client{
		URL: os.Getenv("LDAP_URL"), BaseDN: os.Getenv("LDAP_BASE_DN"), UserAttr: os.Getenv("LDAP_USER_ATTR"),
		BindDN: os.Getenv("LDAP_BIND_DN"), BindPassword: os.Getenv("LDAP_BIND_PASSWORD"),
	}
	server.RunWith("worker", nil, func(ctx context.Context, l *slog.Logger) {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		lastSync := time.Time{}
		for {
			// Directory sync at startup, then every night at 02:00 UTC.
			now := time.Now().UTC()
			if dir.URL != "" && (lastSync.IsZero() || (now.Hour() == 2 && now.Sub(lastSync) > 12*time.Hour)) {
				if err := syncDirectory(ctx, db, dir, l); err != nil {
					l.Error("directory sync failed", "err", err)
				} else {
					lastSync = now
				}
			}
			if _, err := closeStale(ctx, db, rdb, l); err != nil && ctx.Err() == nil {
				l.Error("close stale days failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})
}
