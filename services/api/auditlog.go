package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// auditLog lists the append-only journal. Only the auditor role can read it, and every consultation is itself recorded.
func (a *app) auditLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	from, to := time.Unix(0, 0), time.Now().Add(24*time.Hour)
	if v, err := time.Parse(dateLayout, q.Get("from")); err == nil {
		from = v
	}
	if v, err := time.Parse(dateLayout, q.Get("to")); err == nil {
		to = v.Add(24 * time.Hour)
	}
	rows, err := a.st.db.Query(ctx, `
		SELECT id, at, COALESCE(actor_name,''), action, COALESCE(host(ip),''), details
		FROM audit_log WHERE at >= $1 AND at < $2 AND ($3 = '' OR action = $3) AND ($4 = '' OR actor_name = $4)
		ORDER BY id DESC LIMIT $5 OFFSET $6`, from, to, q.Get("action"), q.Get("actor"), limit, offset)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type entry struct {
		ID      int64           `json:"id"`
		At      time.Time       `json:"at"`
		Actor   string          `json:"actor"`
		Action  string          `json:"action"`
		IP      string          `json:"ip"`
		Details json.RawMessage `json:"details"`
	}
	out := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.IP, &e.Details); err != nil {
			apiError(w, 500, "internal error")
			return
		}
		out = append(out, e)
	}
	a.st.audit(ctx, &uid, uname, "audit_viewed", clientIP(r), map[string]any{"action": q.Get("action"), "actor": q.Get("actor"), "offset": offset})
	writeJSON(w, 200, map[string]any{"entries": out})
}
