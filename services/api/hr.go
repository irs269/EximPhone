package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/irs269/eximphone/services/pkg/attendance"

	"github.com/go-chi/chi/v5"
	"github.com/xuri/excelize/v2"
)

const dateLayout = "2006-01-02"

var monthRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

func (a *app) hrRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(a.requireAny("hr", "manager"))
		r.Get("/hr/overview", a.hrOverview)
		r.Get("/hr/attendance", a.hrAttendance)
		r.Get("/hr/early-departures", a.hrEarlyDepartures)
		r.Post("/hr/attendance/{id}/validate", a.hrValidate)
	})
	r.With(a.requireAny("hr")).Post("/hr/attendance/{id}/correct", a.hrCorrect)
	r.With(a.requireAny("hr")).Get("/hr/export", a.hrExport)
}

// scope tells which employees the caller may see: HR sees everyone, a manager only their team (and themselves).
type scope struct {
	all bool
	ids []int64
}

func (a *app) scopeOf(ctx context.Context, uid int64) scope {
	if a.hasAny(ctx, uid, "hr") {
		return scope{all: true}
	}
	ids := []int64{uid}
	rows, err := a.st.db.Query(ctx, `SELECT id FROM users WHERE manager_id=$1`, uid)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
	}
	return scope{ids: ids}
}

func (s scope) allows(id int64) bool {
	if s.all {
		return true
	}
	for _, x := range s.ids {
		if x == id {
			return true
		}
	}
	return false
}

func parseDate(s string, def time.Time) (time.Time, bool) {
	if s == "" {
		return def, true
	}
	t, err := time.Parse(dateLayout, s)
	return t, err == nil
}

// siteToday is "today" at the head office time zone (default Africa/Dar_es_Salaam).
func siteToday() time.Time {
	loc, err := time.LoadLocation("Africa/Dar_es_Salaam")
	if err != nil {
		loc = time.UTC
	}
	y, m, d := time.Now().In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ---------- real-time overview ----------

type overviewRow struct {
	UserID     int64      `json:"user_id"`
	Name       string     `json:"display_name"`
	Department string     `json:"department"`
	Site       string     `json:"site"`
	Status     string     `json:"status"` // present | late | absent
	DayID      *int64     `json:"day_id,omitempty"`
	ClockIn    *time.Time `json:"clock_in,omitempty"`
	ClockOut   *time.Time `json:"clock_out,omitempty"`
	Departure  string     `json:"departure,omitempty"`
}

type bucket struct {
	Name    string `json:"name"`
	Present int    `json:"present"`
	Late    int    `json:"late"`
	Absent  int    `json:"absent"`
}

func (a *app) hrOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sc := a.scopeOf(ctx, userID(r))
	date, ok := parseDate(r.URL.Query().Get("date"), siteToday())
	if !ok {
		apiError(w, 400, "invalid date")
		return
	}
	rows, err := a.st.db.Query(ctx, `
		SELECT u.id, u.display_name, COALESCE(d.name,''), COALESCE(s.name,''), x.id, x.clock_in, x.clock_out, COALESCE(x.arrival::text,''), COALESCE(x.departure::text,'')
		FROM users u LEFT JOIN departments d ON d.id=u.department_id LEFT JOIN sites s ON s.id=u.site_id
		LEFT JOIN attendance_days x ON x.user_id=u.id AND x.work_date=$1::date
		WHERE u.active AND ($2 OR u.id = ANY($3)) ORDER BY u.display_name`, date, sc.all, sc.ids)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	list := []overviewRow{}
	bySite, byDept := map[string]*bucket{}, map[string]*bucket{}
	totals := bucket{Name: "total"}
	count := func(b *bucket, status string) {
		switch status {
		case "present":
			b.Present++
		case "late":
			b.Late++
		default:
			b.Absent++
		}
	}
	for rows.Next() {
		var x overviewRow
		var arrival string
		if err := rows.Scan(&x.UserID, &x.Name, &x.Department, &x.Site, &x.DayID, &x.ClockIn, &x.ClockOut, &arrival, &x.Departure); err != nil {
			apiError(w, 500, "internal error")
			return
		}
		switch {
		case x.DayID == nil:
			x.Status = "absent"
		case arrival == "late":
			x.Status = "late"
		default:
			x.Status = "present"
		}
		list = append(list, x)
		for _, pair := range []struct {
			m   map[string]*bucket
			key string
		}{{bySite, x.Site}, {byDept, x.Department}} {
			b := pair.m[pair.key]
			if b == nil {
				b = &bucket{Name: pair.key}
				pair.m[pair.key] = b
			}
			count(b, x.Status)
		}
		count(&totals, x.Status)
	}
	flat := func(m map[string]*bucket) []bucket {
		out := make([]bucket, 0, len(m))
		for _, b := range m {
			out = append(out, *b)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}
	writeJSON(w, 200, map[string]any{
		"date": date.Format(dateLayout), "weekend": date.Weekday() == time.Saturday || date.Weekday() == time.Sunday,
		"totals": totals, "by_site": flat(bySite), "by_department": flat(byDept), "rows": list,
	})
}

// ---------- history ----------

type dayRow struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Name       string     `json:"display_name"`
	Username   string     `json:"username"`
	Department string     `json:"department"`
	Site       string     `json:"site"`
	Date       string     `json:"work_date"`
	ClockIn    time.Time  `json:"clock_in"`
	ClockOut   *time.Time `json:"clock_out"`
	Arrival    string     `json:"arrival"`
	Departure  string     `json:"departure"`
	Duration   *int       `json:"duration_min"`
	Reason     string     `json:"early_reason"`
	Validation string     `json:"early_validation"`
	Comment    string     `json:"validation_comment"`
	TZ         string     `json:"-"`
}

const daySelect = `
	SELECT x.id, u.id, u.display_name, u.username::text, COALESCE(d.name,''), COALESCE(s.name,''), x.work_date::text, x.clock_in, x.clock_out,
	       x.arrival::text, COALESCE(x.departure::text,''), x.duration_min, COALESCE(x.early_reason,''), COALESCE(x.early_validation,''),
	       COALESCE(x.validation_comment,''), COALESCE(s.timezone,'UTC')
	FROM attendance_days x JOIN users u ON u.id=x.user_id LEFT JOIN departments d ON d.id=u.department_id LEFT JOIN sites s ON s.id=u.site_id`

func (a *app) queryDays(ctx context.Context, where string, args ...any) ([]dayRow, error) {
	rows, err := a.st.db.Query(ctx, daySelect+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dayRow{}
	for rows.Next() {
		var x dayRow
		if err := rows.Scan(&x.ID, &x.UserID, &x.Name, &x.Username, &x.Department, &x.Site, &x.Date, &x.ClockIn, &x.ClockOut,
			&x.Arrival, &x.Departure, &x.Duration, &x.Reason, &x.Validation, &x.Comment, &x.TZ); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (a *app) hrAttendance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sc := a.scopeOf(ctx, userID(r))
	q := r.URL.Query()
	to, ok1 := parseDate(q.Get("to"), siteToday())
	from, ok2 := parseDate(q.Get("from"), to.AddDate(0, 0, -30))
	if !ok1 || !ok2 || to.Before(from) || to.Sub(from) > 366*24*time.Hour {
		apiError(w, 400, "invalid period")
		return
	}
	uid, _ := strconv.ParseInt(q.Get("user_id"), 10, 64)
	days, err := a.queryDays(ctx, `WHERE x.work_date BETWEEN $1::date AND $2::date AND ($3 OR u.id = ANY($4))
		AND ($5 = 0 OR u.id = $5) AND ($6 = '' OR s.code = $6) AND ($7 = '' OR d.name = $7)
		AND ($8 = '' OR ($8 = 'late' AND x.arrival = 'late') OR ($8 = 'early' AND x.departure = 'early') OR ($8 = 'not_clocked' AND x.departure = 'not_clocked'))
		ORDER BY x.work_date DESC, u.display_name LIMIT 2000`,
		from, to, sc.all, sc.ids, uid, q.Get("site"), q.Get("department"), q.Get("status"))
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, map[string]any{"from": from.Format(dateLayout), "to": to.Format(dateLayout), "days": days})
}

// ---------- early departures: validation by manager / HR ----------

func (a *app) hrEarlyDepartures(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sc := a.scopeOf(ctx, userID(r))
	filter := "AND x.early_validation IS NULL"
	if r.URL.Query().Get("status") == "all" {
		filter = ""
	}
	days, err := a.queryDays(ctx, `WHERE x.departure='early' AND ($1 OR u.id = ANY($2)) `+filter+` ORDER BY x.work_date DESC, u.display_name LIMIT 500`, sc.all, sc.ids)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, map[string]any{"days": days})
}

func (a *app) hrValidate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	id, ok := pathID(r, "id")
	var req struct {
		Approved bool   `json:"approved"`
		Comment  string `json:"comment"`
	}
	if !ok || json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&req) != nil || len(req.Comment) > 500 {
		apiError(w, 400, "invalid request")
		return
	}
	var owner int64
	var dep string
	if a.st.db.QueryRow(ctx, `SELECT user_id, COALESCE(departure::text,'') FROM attendance_days WHERE id=$1`, id).Scan(&owner, &dep) != nil {
		apiError(w, 404, "not found")
		return
	}
	if !a.scopeOf(ctx, uid).allows(owner) || (owner == uid && !a.hasAny(ctx, uid, "hr")) {
		apiError(w, 403, "forbidden") // managers cannot validate their own early departure
		return
	}
	if dep != "early" {
		apiError(w, 409, "only early departures need validation")
		return
	}
	verdict := "rejected"
	if req.Approved {
		verdict = "approved"
	}
	if _, err := a.st.db.Exec(ctx, `UPDATE attendance_days SET early_validation=$2, validated_by=$3, validated_at=now(), validation_comment=NULLIF($4,'') WHERE id=$1`,
		id, verdict, uid, strings.TrimSpace(req.Comment)); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	a.st.audit(ctx, &uid, uname, "early_departure_validated", clientIP(r), map[string]any{"day_id": id, "user_id": owner, "verdict": verdict, "comment": req.Comment})
	w.WriteHeader(http.StatusNoContent)
}

// ---------- manual correction (HR only, justified, audited) ----------

func (a *app) hrCorrect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	id, ok := pathID(r, "id")
	var req struct {
		ClockIn       *time.Time `json:"clock_in"`
		ClockOut      *time.Time `json:"clock_out"`
		Justification string     `json:"justification"`
	}
	if !ok || json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&req) != nil {
		apiError(w, 400, "invalid request")
		return
	}
	req.Justification = strings.TrimSpace(req.Justification)
	if len(req.Justification) < 5 || len(req.Justification) > 500 {
		apiError(w, 400, "a justification (5 to 500 characters) is required")
		return
	}
	if req.ClockIn == nil && req.ClockOut == nil {
		apiError(w, 400, "nothing to correct")
		return
	}
	var owner int64
	var siteID int
	var oldIn time.Time
	var oldOut *time.Time
	var oldArrival, oldDeparture string
	if a.st.db.QueryRow(ctx, `SELECT x.user_id, COALESCE(u.site_id,0), x.clock_in, x.clock_out, x.arrival::text, COALESCE(x.departure::text,'')
		FROM attendance_days x JOIN users u ON u.id=x.user_id WHERE x.id=$1`, id).Scan(&owner, &siteID, &oldIn, &oldOut, &oldArrival, &oldDeparture) != nil {
		apiError(w, 404, "not found")
		return
	}
	st, err := a.st.getSite(ctx, siteID)
	if err != nil {
		apiError(w, 409, "employee has no site")
		return
	}
	in, out := oldIn, oldOut
	if req.ClockIn != nil {
		in = req.ClockIn.UTC()
	}
	if req.ClockOut != nil {
		o := req.ClockOut.UTC()
		out = &o
	}
	if out == nil || !out.After(in) {
		apiError(w, 400, "departure must be after arrival")
		return
	}
	arrival := st.Schedule.ArrivalStatus(in.In(st.TZ))
	dep, _ := st.Schedule.DepartureStatus(out.In(st.TZ), "corrected") // only classifies: the reason is the justification
	mins := attendance.Minutes(in, *out)

	tx, err := a.st.db.Begin(ctx)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer tx.Rollback(ctx)
	oldV, _ := json.Marshal(map[string]any{"clock_in": oldIn, "clock_out": oldOut, "arrival": oldArrival, "departure": oldDeparture})
	newV, _ := json.Marshal(map[string]any{"clock_in": in, "clock_out": out, "arrival": string(arrival), "departure": string(dep), "duration_min": mins})
	if _, err := tx.Exec(ctx, `UPDATE attendance_days SET clock_in=$2, clock_out=$3, arrival=$4::arrival_status, departure=$5::departure_status, duration_min=$6,
		early_validation=NULL, validated_by=NULL, validated_at=NULL, validation_comment=NULL WHERE id=$1`, id, in, out, string(arrival), string(dep), mins); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO attendance_corrections(day_id, corrected_by, justification, old_values, new_values) VALUES ($1,$2,$3,$4,$5)`,
		id, uid, req.Justification, oldV, newV); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	a.st.audit(ctx, &uid, uname, "attendance_corrected", clientIP(r), map[string]any{"day_id": id, "user_id": owner, "justification": req.Justification, "old": json.RawMessage(oldV), "new": json.RawMessage(newV)})
	writeJSON(w, 200, map[string]any{"arrival": arrival, "departure": dep, "duration_min": mins})
}

// ---------- monthly export for payroll ----------

var exportHeader = []string{"Matricule", "Nom", "Departement", "Site", "Date", "Arrivee", "Depart", "Duree (min)", "Ponctualite", "Depart (statut)", "Motif", "Validation"}

var labelArrival = map[string]string{"on_time": "A l'heure", "late": "En retard"}
var labelDeparture = map[string]string{"normal": "Normal", "early": "Anticipe", "not_clocked": "Non pointe", "": ""}
var labelValidation = map[string]string{"": "", "approved": "Approuve", "rejected": "Refuse"}

func (d dayRow) cells() []string {
	loc, err := time.LoadLocation(d.TZ)
	if err != nil {
		loc = time.UTC
	}
	out := ""
	if d.ClockOut != nil {
		out = d.ClockOut.In(loc).Format("15:04")
	}
	dur := ""
	if d.Duration != nil {
		dur = strconv.Itoa(*d.Duration)
	}
	return []string{d.Username, d.Name, d.Department, d.Site, d.Date, d.ClockIn.In(loc).Format("15:04"), out, dur,
		labelArrival[d.Arrival], labelDeparture[d.Departure], d.Reason, labelValidation[d.Validation]}
}

// csvSafe neutralises spreadsheet formula injection in text cells (values starting with = + - @).
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func (a *app) hrExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	month := r.URL.Query().Get("month")
	format := r.URL.Query().Get("format")
	if !monthRe.MatchString(month) || (format != "" && format != "csv" && format != "xlsx") {
		apiError(w, 400, "month=YYYY-MM and format=csv|xlsx expected")
		return
	}
	first, _ := time.Parse(dateLayout, month+"-01")
	last := first.AddDate(0, 1, -1)
	days, err := a.queryDays(ctx, `WHERE x.work_date BETWEEN $1::date AND $2::date ORDER BY u.display_name, x.work_date`, first, last)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	a.st.audit(ctx, &uid, uname, "hr_export", clientIP(r), map[string]any{"month": month, "format": format, "rows": len(days)})

	if format == "xlsx" {
		f := excelize.NewFile()
		defer f.Close()
		detail := "Detail"
		_ = f.SetSheetName("Sheet1", detail)
		for i, h := range exportHeader {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			_ = f.SetCellValue(detail, cell, h)
		}
		type sum struct {
			name        string
			days, late  int
			early, miss int
			minutes     int
		}
		sums := map[string]*sum{}
		var order []string
		for i, d := range days {
			cells := d.cells()
			for c, v := range cells {
				cell, _ := excelize.CoordinatesToCellName(c+1, i+2)
				if c == 7 && d.Duration != nil {
					_ = f.SetCellValue(detail, cell, *d.Duration)
				} else {
					_ = f.SetCellStr(detail, cell, csvSafe(v))
				}
			}
			s := sums[d.Username]
			if s == nil {
				s = &sum{name: d.Name}
				sums[d.Username] = s
				order = append(order, d.Username)
			}
			s.days++
			if d.Arrival == "late" {
				s.late++
			}
			if d.Departure == "early" {
				s.early++
			}
			if d.Departure == "not_clocked" {
				s.miss++
			}
			if d.Duration != nil {
				s.minutes += *d.Duration
			}
		}
		summary := "Synthese"
		_, _ = f.NewSheet(summary)
		for i, h := range []string{"Matricule", "Nom", "Jours presents", "Retards", "Departs anticipes", "Departs non pointes", "Heures totales"} {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			_ = f.SetCellValue(summary, cell, h)
		}
		for i, u := range order {
			s := sums[u]
			row := []any{u, s.name, s.days, s.late, s.early, s.miss, float64(s.minutes) / 60}
			for c, v := range row {
				cell, _ := excelize.CoordinatesToCellName(c+1, i+2)
				if str, ok := v.(string); ok {
					v = csvSafe(str)
				}
				_ = f.SetCellValue(summary, cell, v)
			}
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pointages-%s.xlsx"`, month))
		_ = f.Write(w)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pointages-%s.csv"`, month))
	_, _ = w.Write([]byte("\xef\xbb\xbf")) // BOM so Excel opens UTF-8 correctly
	cw := csv.NewWriter(w)
	cw.Comma = ';' // French Excel default
	_ = cw.Write(exportHeader)
	for _, d := range days {
		cells := d.cells()
		for i := range cells {
			cells[i] = csvSafe(cells[i])
		}
		_ = cw.Write(cells)
	}
	cw.Flush()
}
