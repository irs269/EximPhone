package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/irs269/eximphone/services/pkg/attendance"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type store struct{ db *pgxpool.Pool }

type user struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Extension   string `json:"extension,omitempty"`
	SiteID      int    `json:"site_id"`
	Active      bool   `json:"-"`
}

type site struct {
	ID       int
	Ranges   []string
	TZ       *time.Location
	Schedule attendance.Schedule
}

type day struct {
	ID          int64      `json:"id"`
	WorkDate    string     `json:"work_date"`
	ClockIn     time.Time  `json:"clock_in"`
	ClockOut    *time.Time `json:"clock_out,omitempty"`
	Arrival     string     `json:"arrival"`
	Departure   *string    `json:"departure,omitempty"`
	DurationMin *int       `json:"duration_min,omitempty"`
}

const dayCols = `id, work_date::text, clock_in, clock_out, arrival::text, departure::text, duration_min`

func scanDay(r pgx.Row) (*day, error) {
	var d day
	if err := r.Scan(&d.ID, &d.WorkDate, &d.ClockIn, &d.ClockOut, &d.Arrival, &d.Departure, &d.DurationMin); err != nil {
		return nil, err
	}
	return &d, nil
}

// upsertUser syncs the directory profile into users and returns the local record.
func (s store) upsertUser(ctx context.Context, p *Profile) (*user, error) {
	var u user
	err := s.db.QueryRow(ctx, `
		INSERT INTO users (username, display_name, email, extension, department_id, site_id)
		VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''),
		        (SELECT id FROM departments WHERE name = $5),
		        (SELECT id FROM sites WHERE code = $6))
		ON CONFLICT (username) DO UPDATE SET
		  display_name = EXCLUDED.display_name, email = EXCLUDED.email, extension = EXCLUDED.extension,
		  department_id = EXCLUDED.department_id, site_id = EXCLUDED.site_id
		RETURNING id, username::text, display_name, COALESCE(extension,''), COALESCE(site_id,0), active`,
		p.Username, p.DisplayName, p.Email, p.Extension, p.Department, p.SiteCode,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Extension, &u.SiteID, &u.Active)
	return &u, err
}

func (s store) getUser(ctx context.Context, id int64) (*user, error) {
	var u user
	err := s.db.QueryRow(ctx, `SELECT id, username::text, display_name, COALESCE(extension,''), COALESCE(site_id,0), active
		FROM users WHERE id=$1`, id).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Extension, &u.SiteID, &u.Active)
	return &u, err
}

func (s store) getSite(ctx context.Context, id int) (*site, error) {
	var (
		st        site
		tz        string
		start, en time.Time
		tol       int
	)
	err := s.db.QueryRow(ctx, `
		SELECT s.id, s.ip_ranges::text[], s.timezone, w.start_time, w.end_time, w.tolerance_min
		FROM sites s JOIN work_schedules w ON w.site_id = s.id WHERE s.id=$1`, id,
	).Scan(&st.ID, &st.Ranges, &tz, &start, &en, &tol)
	if err != nil {
		return nil, err
	}
	if st.TZ, err = time.LoadLocation(tz); err != nil {
		return nil, err
	}
	st.Schedule = attendance.Schedule{
		Start: tod(start), End: tod(en), Tolerance: time.Duration(tol) * time.Minute,
	}
	return &st, nil
}

func tod(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
}

func (s store) openDay(ctx context.Context, userID int64) (*day, error) {
	d, err := scanDay(s.db.QueryRow(ctx, `SELECT `+dayCols+` FROM attendance_days WHERE user_id=$1 AND clock_out IS NULL`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// clockIn opens today's day. An already-open day (any device, any date) is returned as is, so there
// is only ever one open day. Logging in again after a same-day logout reopens that day.
func (s store) clockIn(ctx context.Context, userID int64, now time.Time, workDate time.Time, arrival attendance.Arrival) (*day, bool, error) {
	if d, err := s.openDay(ctx, userID); err != nil || d != nil {
		return d, false, err
	}
	d, err := scanDay(s.db.QueryRow(ctx, `
		INSERT INTO attendance_days (user_id, work_date, clock_in, arrival)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, work_date) DO UPDATE SET
		  clock_out=NULL, departure=NULL, early_reason=NULL, duration_min=NULL, last_activity_at=now()
		RETURNING `+dayCols, userID, workDate, now, string(arrival)))
	return d, err == nil, err
}

func (s store) clockOut(ctx context.Context, dayID int64, out time.Time, dep attendance.Departure, reason string, mins int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE attendance_days SET clock_out=$2, departure=$3::departure_status,
		  early_reason=NULLIF($4,''), duration_min=$5 WHERE id=$1 AND clock_out IS NULL`,
		dayID, out, string(dep), reason, mins)
	return err
}

// touch records activity and confirms the session is still valid: the employee must be
// active and have an open day (login = clock-in; no open day means no access).
func (s store) touch(ctx context.Context, userID int64) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE attendance_days d SET last_activity_at = now() FROM users u
		WHERE u.id = d.user_id AND u.id = $1 AND u.active AND d.clock_out IS NULL`, userID)
	return tag.RowsAffected() == 1, err
}

func (s store) audit(ctx context.Context, actorID *int64, name, action, ip string, details map[string]any) {
	b, _ := json.Marshal(details)
	_, _ = s.db.Exec(ctx, `INSERT INTO audit_log (actor_id, actor_name, action, ip, details)
		VALUES ($1, $2, $3, NULLIF($4,'')::inet, $5)`, actorID, name, action, ip, b)
}

// history returns the most recent work days, newest first.
func (s store) history(ctx context.Context, userID int64, limit int) ([]day, error) {
	rows, err := s.db.Query(ctx, `SELECT `+dayCols+` FROM attendance_days WHERE user_id=$1 ORDER BY work_date DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []day{}
	for rows.Next() {
		d, err := scanDay(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}
