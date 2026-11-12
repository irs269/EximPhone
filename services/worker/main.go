package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/irs269/eximphone/services/pkg/server"

	"github.com/jackc/pgx/v5/pgxpool"
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

func closeStale(ctx context.Context, db *pgxpool.Pool, log *slog.Logger) (int, error) {
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
	server.RunWith("worker", nil, func(ctx context.Context, l *slog.Logger) {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			if _, err := closeStale(ctx, db, l); err != nil && ctx.Err() == nil {
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
