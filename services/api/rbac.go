package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// Roles: employee (implicit for everyone), manager, hr, communication, direction, it_admin, auditor.
var assignableRoles = map[string]bool{"manager": true, "hr": true, "communication": true, "direction": true, "it_admin": true, "auditor": true}

// rolesOf returns the roles of a user; "employee" is always included.
func (a *app) rolesOf(ctx context.Context, uid int64) []string {
	out := []string{"employee"}
	rows, err := a.st.db.Query(ctx, `SELECT role FROM user_roles WHERE user_id=$1 ORDER BY role`, uid)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r string
		if rows.Scan(&r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func (a *app) hasAny(ctx context.Context, uid int64, roles ...string) bool {
	for _, have := range a.rolesOf(ctx, uid) {
		for _, want := range roles {
			if have == want {
				return true
			}
		}
	}
	return false
}

func (a *app) requireAny(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !a.hasAny(r.Context(), userID(r), roles...) {
				apiError(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (a *app) actor(ctx context.Context, r *http.Request) (int64, string) {
	uid := userID(r)
	u, _ := a.st.getUser(ctx, uid)
	return uid, u.Username
}

// rbacRoutes registers the phase 6 endpoints: administration, announcements, HR, audit.
func (a *app) rbacRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(a.requireAny("it_admin"))
		r.Get("/admin/users", a.adminUsers)
		r.Put("/admin/users/{id}/roles", a.setRoles)
		r.Put("/admin/users/{id}/manager", a.setManager)
		r.Get("/admin/sites", a.adminSites)
		r.Put("/admin/sites/{id}", a.updateSite)
	})
	a.announcementRoutes(r)
	a.hrRoutes(r)
	a.ticketRoutes(r)
	r.With(a.requireAny("auditor")).Get("/audit", a.auditLog)
}

// ---------- users and roles ----------

func (a *app) adminUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.db.Query(r.Context(), `
		SELECT u.id, u.username::text, u.display_name, COALESCE(u.extension,''), COALESCE(d.name,''), COALESCE(s.name,''), u.manager_id, u.active,
		       COALESCE((SELECT array_agg(role ORDER BY role) FROM user_roles x WHERE x.user_id=u.id), '{}')
		FROM users u LEFT JOIN departments d ON d.id=u.department_id LEFT JOIN sites s ON s.id=u.site_id ORDER BY u.display_name`)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type row struct {
		ID         int64    `json:"id"`
		Username   string   `json:"username"`
		Name       string   `json:"display_name"`
		Extension  string   `json:"extension"`
		Department string   `json:"department"`
		Site       string   `json:"site"`
		ManagerID  *int64   `json:"manager_id"`
		Active     bool     `json:"active"`
		Roles      []string `json:"roles"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.Username, &x.Name, &x.Extension, &x.Department, &x.Site, &x.ManagerID, &x.Active, &x.Roles); err != nil {
			apiError(w, 500, "internal error")
			return
		}
		out = append(out, x)
	}
	writeJSON(w, 200, map[string]any{"users": out})
}

func (a *app) setRoles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin, adminName := a.actor(ctx, r)
	target, ok := pathID(r, "id")
	var req struct {
		Roles []string `json:"roles"`
	}
	if !ok || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req) != nil {
		apiError(w, 400, "invalid request")
		return
	}
	set := map[string]bool{}
	for _, role := range req.Roles {
		if role == "employee" {
			continue // implicit
		}
		if !assignableRoles[role] {
			apiError(w, 400, "unknown role "+role)
			return
		}
		set[role] = true
	}
	if target == admin && !set["it_admin"] {
		apiError(w, 400, "you cannot remove your own it_admin role")
		return
	}
	var exists bool
	if a.st.db.QueryRow(ctx, `SELECT true FROM users WHERE id=$1`, target).Scan(&exists) != nil {
		apiError(w, 404, "user not found")
		return
	}
	old := a.rolesOf(ctx, target)
	tx, err := a.st.db.Begin(ctx)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, target); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	for role := range set {
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id, role) VALUES ($1,$2)`, target, role); err != nil {
			apiError(w, 500, "internal error")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	a.st.audit(ctx, &admin, adminName, "roles_changed", clientIP(r), map[string]any{"user_id": target, "old": old, "new": a.rolesOf(ctx, target)})
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) setManager(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin, adminName := a.actor(ctx, r)
	target, ok := pathID(r, "id")
	var req struct {
		ManagerID *int64 `json:"manager_id"`
	}
	if !ok || json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&req) != nil || (req.ManagerID != nil && *req.ManagerID == target) {
		apiError(w, 400, "invalid request")
		return
	}
	tag, err := a.st.db.Exec(ctx, `UPDATE users SET manager_id=$2 WHERE id=$1`, target, req.ManagerID)
	if err != nil || tag.RowsAffected() != 1 {
		apiError(w, 400, "invalid user or manager")
		return
	}
	a.st.audit(ctx, &admin, adminName, "manager_changed", clientIP(r), map[string]any{"user_id": target, "manager_id": req.ManagerID})
	w.WriteHeader(http.StatusNoContent)
}

// ---------- sites and working hours ----------

func (a *app) adminSites(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.db.Query(r.Context(), `
		SELECT s.id, s.code, s.name, s.ip_ranges::text[], s.timezone, to_char(w.start_time,'HH24:MI'), to_char(w.end_time,'HH24:MI'), w.tolerance_min
		FROM sites s JOIN work_schedules w ON w.site_id=s.id ORDER BY s.id`)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type row struct {
		ID        int      `json:"id"`
		Code      string   `json:"code"`
		Name      string   `json:"name"`
		IPRanges  []string `json:"ip_ranges"`
		Timezone  string   `json:"timezone"`
		Start     string   `json:"start_time"`
		End       string   `json:"end_time"`
		Tolerance int      `json:"tolerance_min"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.Code, &x.Name, &x.IPRanges, &x.Timezone, &x.Start, &x.End, &x.Tolerance); err != nil {
			apiError(w, 500, "internal error")
			return
		}
		out = append(out, x)
	}
	writeJSON(w, 200, map[string]any{"sites": out})
}

func (a *app) updateSite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin, adminName := a.actor(ctx, r)
	id, ok := pathID(r, "id")
	var req struct {
		Name      string   `json:"name"`
		IPRanges  []string `json:"ip_ranges"`
		Timezone  string   `json:"timezone"`
		Start     string   `json:"start_time"`
		End       string   `json:"end_time"`
		Tolerance int      `json:"tolerance_min"`
	}
	if !ok || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.Name == "" || len(req.IPRanges) == 0 {
		apiError(w, 400, "invalid request")
		return
	}
	for _, c := range req.IPRanges {
		if _, _, err := net.ParseCIDR(c); err != nil {
			apiError(w, 400, "invalid CIDR "+c)
			return
		}
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		apiError(w, 400, "invalid time zone")
		return
	}
	st, e1 := time.Parse("15:04", req.Start)
	en, e2 := time.Parse("15:04", req.End)
	if e1 != nil || e2 != nil || !en.After(st) || req.Tolerance < 0 || req.Tolerance > 120 {
		apiError(w, 400, "invalid working hours")
		return
	}
	tx, err := a.st.db.Begin(ctx)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE sites SET name=$2, ip_ranges=$3::text[]::cidr[], timezone=$4 WHERE id=$1`, id, req.Name, req.IPRanges, req.Timezone)
	if err != nil || tag.RowsAffected() != 1 {
		apiError(w, 404, "site not found")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE work_schedules SET start_time=$2::time, end_time=$3::time, tolerance_min=$4 WHERE site_id=$1`, id, req.Start, req.End, req.Tolerance); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	a.st.audit(ctx, &admin, adminName, "site_updated", clientIP(r), map[string]any{"site_id": id, "ip_ranges": req.IPRanges, "start": req.Start, "end": req.End, "tolerance_min": req.Tolerance, "timezone": req.Timezone})
	w.WriteHeader(http.StatusNoContent)
}
