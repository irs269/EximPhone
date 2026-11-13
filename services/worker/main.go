package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/irs269/eximphone/services/pkg/directory"
	"github.com/irs269/eximphone/services/pkg/server"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
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
	// Bootstrap: the first it_admin accounts come from the environment (comma separated usernames).
	if admins := strings.Split(os.Getenv("INITIAL_IT_ADMINS"), ","); len(admins) > 0 && strings.TrimSpace(admins[0]) != "" {
		for i := range admins {
			admins[i] = strings.TrimSpace(admins[i])
		}
		if _, err := db.Exec(ctx, `INSERT INTO user_roles(user_id, role) SELECT id, 'it_admin' FROM users WHERE username::text = ANY($1) ON CONFLICT DO NOTHING`, admins); err != nil {
			return err
		}
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

// purgeRecordings deletes recordings older than the retention period (objects first, then rows) and audits it.
func purgeRecordings(ctx context.Context, db *pgxpool.Pool, s3 *minio.Client, days int, log *slog.Logger) {
	rows, err := db.Query(ctx, `SELECT id::text, s3_key FROM call_recordings WHERE created_at < now() - make_interval(days => $1)`, days)
	if err != nil {
		log.Error("recording purge query failed", "err", err)
		return
	}
	type item struct{ id, key string }
	var old []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.key) == nil {
			old = append(old, it)
		}
	}
	rows.Close()
	purged := 0
	for _, it := range old {
		if err := s3.RemoveObject(ctx, "recordings", it.key, minio.RemoveObjectOptions{}); err != nil {
			log.Warn("recording object removal failed", "key", it.key, "err", err)
			continue
		}
		if _, err := db.Exec(ctx, `DELETE FROM call_recordings WHERE id=$1::uuid`, it.id); err == nil {
			purged++
		}
	}
	if purged > 0 {
		_, _ = db.Exec(ctx, `INSERT INTO audit_log(actor_name, action, details) VALUES ('system', 'recordings_purged', jsonb_build_object('count', $1::int, 'older_than_days', $2::int))`, purged, days)
		log.Info("recordings purged", "count", purged, "older_than_days", days)
	}
}

func s3FromEnv() *minio.Client {
	endpoint := os.Getenv("S3_ENDPOINT")
	if endpoint == "" {
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil
	}
	c, err := minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(os.Getenv("S3_ACCESS_KEY_ID"), os.Getenv("S3_SECRET_ACCESS_KEY"), ""),
		Secure:       u.Scheme == "https",
		Region:       "garage",
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil
	}
	return c
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
		lastPurge := time.Time{}
		s3 := s3FromEnv()
		retention, _ := strconv.Atoi(os.Getenv("RECORDING_RETENTION_DAYS"))
		if retention <= 0 {
			retention = 365
		}
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
			if s3 != nil && time.Since(lastPurge) > 24*time.Hour {
				purgeRecordings(ctx, db, s3, retention, l)
				lastPurge = time.Now()
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
