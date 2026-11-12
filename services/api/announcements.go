package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

func (a *app) announcementRoutes(r chi.Router) {
	r.Get("/meta/org", a.orgMeta)
	r.Get("/announcements", a.listAnnouncements)
	r.With(a.requireAny("communication", "direction")).Post("/announcements", a.publishAnnouncement)
	r.Post("/announcements/{id}/ack", a.ackAnnouncement)
	r.Get("/announcements/{id}/acks", a.announcementAcks)
}

// targetClause selects the announcements/recipients matching a user's department and site.
const targetClause = `(a.target_kind = 'all' OR (a.target_kind = 'department' AND a.target_id = $2) OR (a.target_kind = 'site' AND a.target_id = $3))`

func (a *app) myScope(ctx context.Context, uid int64) (dept, site int) {
	_ = a.st.db.QueryRow(ctx, `SELECT COALESCE(department_id,0), COALESCE(site_id,0) FROM users WHERE id=$1`, uid).Scan(&dept, &site)
	return
}

func (a *app) publishAnnouncement(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	var req struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		Level  string `json:"level"`
		Target struct {
			Kind string `json:"kind"`
			ID   int    `json:"id"`
		} `json:"target"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req) != nil {
		apiError(w, 400, "invalid request")
		return
	}
	req.Title, req.Body = strings.TrimSpace(req.Title), strings.TrimSpace(req.Body)
	if req.Title == "" || utf8.RuneCountInString(req.Title) > 200 || req.Body == "" || utf8.RuneCountInString(req.Body) > 5000 {
		apiError(w, 400, "title (max 200) and body (max 5000) are required")
		return
	}
	if req.Level == "" {
		req.Level = "normal"
	}
	if req.Level != "normal" && req.Level != "urgent" {
		apiError(w, 400, "invalid level")
		return
	}
	var targetID *int
	switch req.Target.Kind {
	case "", "all":
		req.Target.Kind = "all"
	case "department", "site":
		table := map[string]string{"department": "departments", "site": "sites"}[req.Target.Kind]
		var ok bool
		if a.st.db.QueryRow(ctx, `SELECT true FROM `+table+` WHERE id=$1`, req.Target.ID).Scan(&ok) != nil {
			apiError(w, 400, "unknown target")
			return
		}
		targetID = &req.Target.ID
	default:
		apiError(w, 400, "invalid target")
		return
	}
	var id int64
	if err := a.st.db.QueryRow(ctx, `INSERT INTO announcements(title, body, level, target_kind, target_id, author_id) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		req.Title, req.Body, req.Level, req.Target.Kind, targetID, uid).Scan(&id); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	recipients := a.recipients(ctx, req.Target.Kind, targetID)
	a.hub.publish(ctx, recipients, "announcement", map[string]any{"id": id, "title": req.Title, "level": req.Level})
	a.st.audit(ctx, &uid, uname, "announcement_published", clientIP(r), map[string]any{"id": id, "level": req.Level, "target": req.Target.Kind, "target_id": targetID, "recipients": len(recipients)})
	writeJSON(w, 200, map[string]any{"id": id, "recipients": len(recipients)})
}

func (a *app) recipients(ctx context.Context, kind string, targetID *int) []int64 {
	q := `SELECT id FROM users WHERE active`
	var args []any
	switch kind {
	case "department":
		q += ` AND department_id=$1`
		args = append(args, *targetID)
	case "site":
		q += ` AND site_id=$1`
		args = append(args, *targetID)
	}
	rows, err := a.st.db.Query(ctx, q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

func (a *app) listAnnouncements(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	dept, site := a.myScope(ctx, uid)
	rows, err := a.st.db.Query(ctx, `
		SELECT a.id, a.title, a.body, a.level, au.display_name, a.created_at, a.target_kind, COALESCE(d.name, s.name, ''), ak.acked_at
		FROM announcements a JOIN users au ON au.id = a.author_id
		LEFT JOIN departments d ON a.target_kind='department' AND d.id=a.target_id
		LEFT JOIN sites s ON a.target_kind='site' AND s.id=a.target_id
		LEFT JOIN announcement_acks ak ON ak.announcement_id=a.id AND ak.user_id=$1
		WHERE `+targetClause+` ORDER BY a.created_at DESC LIMIT 100`, uid, dept, site)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type row struct {
		ID         int64      `json:"id"`
		Title      string     `json:"title"`
		Body       string     `json:"body"`
		Level      string     `json:"level"`
		Author     string     `json:"author"`
		CreatedAt  time.Time  `json:"created_at"`
		TargetKind string     `json:"target_kind"`
		TargetName string     `json:"target_name"`
		AckedAt    *time.Time `json:"acked_at"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.Title, &x.Body, &x.Level, &x.Author, &x.CreatedAt, &x.TargetKind, &x.TargetName, &x.AckedAt); err != nil {
			apiError(w, 500, "internal error")
			return
		}
		out = append(out, x)
	}
	writeJSON(w, 200, map[string]any{"announcements": out})
}

func (a *app) ackAnnouncement(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id, ok := pathID(r, "id")
	if !ok {
		apiError(w, 400, "invalid id")
		return
	}
	dept, site := a.myScope(ctx, uid)
	tag, err := a.st.db.Exec(ctx, `INSERT INTO announcement_acks(announcement_id, user_id)
		SELECT a.id, $1 FROM announcements a WHERE a.id=$4 AND `+targetClause+` ON CONFLICT DO NOTHING`, uid, dept, site, id)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	var targeted bool
	_ = a.st.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM announcements a WHERE a.id=$1 AND `+targetClause+`)`, id, dept, site).Scan(&targeted)
	if tag.RowsAffected() == 0 && !targeted {
		apiError(w, 404, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// announcementAcks is the follow-up table: who has read, who has not.
func (a *app) announcementAcks(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id, ok := pathID(r, "id")
	if !ok {
		apiError(w, 400, "invalid id")
		return
	}
	var kind string
	var targetID *int
	var author int64
	if a.st.db.QueryRow(ctx, `SELECT target_kind, target_id, author_id FROM announcements WHERE id=$1`, id).Scan(&kind, &targetID, &author) != nil {
		apiError(w, 404, "not found")
		return
	}
	if author != uid && !a.hasAny(ctx, uid, "communication", "direction", "hr") {
		apiError(w, 403, "forbidden")
		return
	}
	rows, err := a.st.db.Query(ctx, `
		SELECT u.id, u.display_name, COALESCE(d.name,''), COALESCE(s.name,''), ak.acked_at
		FROM users u LEFT JOIN departments d ON d.id=u.department_id LEFT JOIN sites s ON s.id=u.site_id
		LEFT JOIN announcement_acks ak ON ak.announcement_id=$1 AND ak.user_id=u.id
		WHERE u.active AND ($2='all' OR ($2='department' AND u.department_id=$3) OR ($2='site' AND u.site_id=$3))
		ORDER BY ak.acked_at NULLS LAST, u.display_name`, id, kind, targetID)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type person struct {
		ID         int64      `json:"id"`
		Name       string     `json:"display_name"`
		Department string     `json:"department"`
		Site       string     `json:"site"`
		AckedAt    *time.Time `json:"acked_at"`
	}
	read, unread := []person{}, []person{}
	for rows.Next() {
		var p person
		if err := rows.Scan(&p.ID, &p.Name, &p.Department, &p.Site, &p.AckedAt); err != nil {
			apiError(w, 500, "internal error")
			return
		}
		if p.AckedAt != nil {
			read = append(read, p)
		} else {
			unread = append(unread, p)
		}
	}
	writeJSON(w, 200, map[string]any{"total": len(read) + len(unread), "read": read, "unread": unread})
}

// orgMeta lists departments and sites (ids and names only) so the UI can offer announcement targets.
func (a *app) orgMeta(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	load := func(q string) []item {
		out := []item{}
		rows, err := a.st.db.Query(r.Context(), q)
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			var x item
			if rows.Scan(&x.ID, &x.Name) == nil {
				out = append(out, x)
			}
		}
		return out
	}
	writeJSON(w, 200, map[string]any{
		"departments": load(`SELECT id, name FROM departments ORDER BY name`),
		"sites":       load(`SELECT id, name FROM sites ORDER BY name`),
	})
}
