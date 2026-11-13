package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

// Service tickets.
//
//	pending --approve--> approved --assign--> assigned --start--> in_progress --resolve--> resolved
//	   |  \--reject--> rejected                  (re-assign allowed until resolved)
//	   \--(requester) cancel--> cancelled        (also from approved)
//
// An employee files a ticket for a service (department). A head of that service approves or rejects it
// (never their own ticket), then assigns it to an active member of the service, who starts and resolves it.
// it_admin may act as head of any service (administrative fallback). Every step is recorded in ticket_events
// (append-only) and in the audit log, and the people concerned are notified over the WebSocket.

func (a *app) ticketRoutes(r chi.Router) {
	r.Get("/tickets", a.listTickets)
	r.Get("/tickets/summary", a.ticketSummary)
	r.Get("/tickets/services", a.ticketServices)
	r.Post("/tickets", a.createTicket)
	r.Get("/tickets/{id}", a.getTicket)
	r.Get("/tickets/{id}/candidates", a.ticketCandidates)
	r.Post("/tickets/{id}/decision", a.decideTicket)
	r.Post("/tickets/{id}/assign", a.assignTicket)
	r.Post("/tickets/{id}/start", a.startTicket)
	r.Post("/tickets/{id}/resolve", a.resolveTicket)
	r.Post("/tickets/{id}/cancel", a.cancelTicket)
	r.Post("/tickets/{id}/comments", a.commentTicket)
	r.Group(func(r chi.Router) {
		r.Use(a.requireAny("it_admin"))
		r.Get("/admin/departments", a.adminDepartments)
		r.Put("/admin/departments/{id}/heads", a.setDepartmentHeads)
	})
}

type ticket struct {
	ID           int64      `json:"id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Priority     string     `json:"priority"`
	Status       string     `json:"status"`
	RequesterID  int64      `json:"requester_id"`
	Requester    string     `json:"requester"`
	DepartmentID int        `json:"department_id"`
	Department   string     `json:"department"`
	AssigneeID   *int64     `json:"assignee_id"`
	Assignee     string     `json:"assignee"`
	Decision     string     `json:"decision_comment"`
	DecidedBy    string     `json:"decided_by"`
	Resolution   string     `json:"resolution"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Can          ticketCan  `json:"can"`
	Events       []ticketEv `json:"events,omitempty"`
}

// ticketCan lists what the current user may do with a ticket, so the UI never has to guess.
type ticketCan struct {
	Decide  bool `json:"decide"`
	Assign  bool `json:"assign"`
	Start   bool `json:"start"`
	Resolve bool `json:"resolve"`
	Cancel  bool `json:"cancel"`
}

type ticketEv struct {
	Kind      string    `json:"kind"`
	Actor     string    `json:"actor"`
	Comment   string    `json:"comment"`
	CreatedAt time.Time `json:"created_at"`
}

const ticketSelect = `
	SELECT t.id, t.title, t.description, t.priority, t.status, t.requester_id, rq.display_name, t.department_id, d.name,
	       t.assignee_id, COALESCE(asg.display_name,''), COALESCE(t.decision_comment,''), COALESCE(dc.display_name,''),
	       COALESCE(t.resolution,''), t.created_at, t.updated_at
	FROM tickets t JOIN users rq ON rq.id=t.requester_id JOIN departments d ON d.id=t.department_id
	LEFT JOIN users asg ON asg.id=t.assignee_id LEFT JOIN users dc ON dc.id=t.decided_by`

type scanner interface{ Scan(dest ...any) error }

func scanTicket(s scanner) (ticket, error) {
	var t ticket
	err := s.Scan(&t.ID, &t.Title, &t.Description, &t.Priority, &t.Status, &t.RequesterID, &t.Requester, &t.DepartmentID, &t.Department,
		&t.AssigneeID, &t.Assignee, &t.Decision, &t.DecidedBy, &t.Resolution, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// ticketViewer holds what is needed to compute visibility and permissions for one user.
type ticketViewer struct {
	uid   int64
	admin bool
	heads map[int]bool // departments the user heads
}

func (a *app) viewer(ctx context.Context, uid int64) ticketViewer {
	v := ticketViewer{uid: uid, admin: a.hasAny(ctx, uid, "it_admin"), heads: map[int]bool{}}
	rows, err := a.st.db.Query(ctx, `SELECT department_id FROM department_heads WHERE user_id=$1`, uid)
	if err != nil {
		return v
	}
	defer rows.Close()
	for rows.Next() {
		var d int
		if rows.Scan(&d) == nil {
			v.heads[d] = true
		}
	}
	return v
}

// manages: the user acts as head for this department (real head, or it_admin as administrative fallback).
func (v ticketViewer) manages(dept int) bool { return v.admin || v.heads[dept] }

func (v ticketViewer) sees(t ticket) bool {
	return t.RequesterID == v.uid || (t.AssigneeID != nil && *t.AssigneeID == v.uid) || v.manages(t.DepartmentID)
}

func (v ticketViewer) can(t ticket) ticketCan {
	mine := t.AssigneeID != nil && *t.AssigneeID == v.uid
	return ticketCan{
		Decide:  t.Status == "pending" && v.manages(t.DepartmentID) && t.RequesterID != v.uid,
		Assign:  (t.Status == "approved" || t.Status == "assigned" || t.Status == "in_progress") && v.manages(t.DepartmentID),
		Start:   t.Status == "assigned" && mine,
		Resolve: (t.Status == "assigned" || t.Status == "in_progress") && mine,
		Cancel:  (t.Status == "pending" || t.Status == "approved") && t.RequesterID == v.uid,
	}
}

func (a *app) loadTicket(ctx context.Context, id int64) (ticket, bool) {
	t, err := scanTicket(a.st.db.QueryRow(ctx, ticketSelect+` WHERE t.id=$1`, id))
	return t, err == nil
}

func ticketID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

// ticketFor loads the ticket and checks that the caller may see it (404 otherwise, never 403: no existence leak).
func (a *app) ticketFor(w http.ResponseWriter, r *http.Request) (ticket, ticketViewer, bool) {
	ctx := r.Context()
	id, ok := ticketID(r)
	if !ok {
		apiError(w, 404, "not found")
		return ticket{}, ticketViewer{}, false
	}
	t, found := a.loadTicket(ctx, id)
	v := a.viewer(ctx, userID(r))
	if !found || !v.sees(t) {
		apiError(w, 404, "not found")
		return ticket{}, v, false
	}
	return t, v, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(v) != nil {
		apiError(w, 400, "invalid request")
		return false
	}
	return true
}

func cleanText(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	return s, s != "" && utf8.RuneCountInString(s) <= max
}

// notifyTicket tells everyone concerned (requester, assignee, heads of the service) to refresh.
func (a *app) notifyTicket(ctx context.Context, t ticket, kind string, actor int64, also ...int64) {
	set := map[int64]bool{t.RequesterID: true}
	if t.AssigneeID != nil {
		set[*t.AssigneeID] = true
	}
	for _, u := range also {
		set[u] = true
	}
	if rows, err := a.st.db.Query(ctx, `SELECT user_id FROM department_heads WHERE department_id=$1`, t.DepartmentID); err == nil {
		for rows.Next() {
			var u int64
			if rows.Scan(&u) == nil {
				set[u] = true
			}
		}
		rows.Close()
	}
	users := make([]int64, 0, len(set))
	for u := range set {
		users = append(users, u)
	}
	a.hub.publish(ctx, users, "ticket", map[string]any{"id": t.ID, "kind": kind, "title": t.Title, "status": t.Status, "actor_id": actor})
}

func (a *app) ticketEvent(ctx context.Context, id, actor int64, kind, comment string) {
	_, _ = a.st.db.Exec(ctx, `INSERT INTO ticket_events(ticket_id, actor_id, kind, comment) VALUES ($1,$2,$3,NULLIF($4,''))`, id, actor, kind, comment)
}

// ---------- reading ----------

func (a *app) listTickets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := a.viewer(ctx, userID(r))
	headOf := `EXISTS (SELECT 1 FROM department_heads h WHERE h.department_id=t.department_id AND h.user_id=$1)`
	var where string
	switch r.URL.Query().Get("view") {
	case "mine": // filed by me
		where = `t.requester_id=$1`
	case "todo": // assigned to me
		where = `t.assignee_id=$1`
	case "service": // tickets of the services I head (it_admin: every service)
		where = headOf
		if v.admin {
			where = `true`
		}
	default: // everything I may see
		where = `t.requester_id=$1 OR t.assignee_id=$1 OR ` + headOf
		if v.admin {
			where = `true`
		}
	}
	// $1 is always referenced so the driver can type it, whichever branch applies
	rows, err := a.st.db.Query(ctx, ticketSelect+` WHERE ($1::bigint > 0) AND (`+where+`) ORDER BY
		CASE t.status WHEN 'pending' THEN 0 WHEN 'approved' THEN 1 WHEN 'assigned' THEN 2 WHEN 'in_progress' THEN 3 ELSE 4 END, t.created_at DESC LIMIT 200`, v.uid)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			apiError(w, 500, "internal error")
			return
		}
		t.Can = v.can(t)
		out = append(out, t)
	}
	writeJSON(w, 200, map[string]any{"tickets": out})
}

// ticketSummary feeds the navigation badge: work waiting for the current user.
func (a *app) ticketSummary(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	var toDecide, toAssign, toDo, mine int
	_ = a.st.db.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE t.status='pending'  AND t.requester_id<>$1 AND h.user_id IS NOT NULL),
		  COUNT(*) FILTER (WHERE t.status='approved' AND h.user_id IS NOT NULL),
		  COUNT(*) FILTER (WHERE t.status IN ('assigned','in_progress') AND t.assignee_id=$1),
		  COUNT(*) FILTER (WHERE t.requester_id=$1 AND t.status IN ('pending','approved','assigned','in_progress'))
		FROM tickets t LEFT JOIN department_heads h ON h.department_id=t.department_id AND h.user_id=$1
		WHERE t.status IN ('pending','approved','assigned','in_progress')`, uid).Scan(&toDecide, &toAssign, &toDo, &mine)
	v := a.viewer(ctx, uid)
	writeJSON(w, 200, map[string]any{"to_decide": toDecide, "to_assign": toAssign, "to_do": toDo, "mine_open": mine, "waiting": toDecide + toAssign + toDo,
		"is_head": v.admin || len(v.heads) > 0})
}

// ticketServices lists the services a ticket can be filed for, with their heads.
func (a *app) ticketServices(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.db.Query(r.Context(), `
		SELECT d.id, d.name, COALESCE((SELECT array_agg(u.display_name ORDER BY u.display_name) FROM department_heads h JOIN users u ON u.id=h.user_id WHERE h.department_id=d.id AND u.active), '{}')
		FROM departments d ORDER BY d.name`)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type svc struct {
		ID    int      `json:"id"`
		Name  string   `json:"name"`
		Heads []string `json:"heads"`
	}
	out := []svc{}
	for rows.Next() {
		var s svc
		if rows.Scan(&s.ID, &s.Name, &s.Heads) == nil {
			out = append(out, s)
		}
	}
	writeJSON(w, 200, map[string]any{"services": out})
}

func (a *app) getTicket(w http.ResponseWriter, r *http.Request) {
	t, v, ok := a.ticketFor(w, r)
	if !ok {
		return
	}
	t.Can = v.can(t)
	t.Events = []ticketEv{}
	rows, err := a.st.db.Query(r.Context(), `SELECT e.kind, u.display_name, COALESCE(e.comment,''), e.created_at FROM ticket_events e JOIN users u ON u.id=e.actor_id WHERE e.ticket_id=$1 ORDER BY e.id`, t.ID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var e ticketEv
			if rows.Scan(&e.Kind, &e.Actor, &e.Comment, &e.CreatedAt) == nil {
				t.Events = append(t.Events, e)
			}
		}
	}
	writeJSON(w, 200, t)
}

// ticketCandidates lists the active members of the service a head may assign the ticket to.
func (a *app) ticketCandidates(w http.ResponseWriter, r *http.Request) {
	t, v, ok := a.ticketFor(w, r)
	if !ok {
		return
	}
	if !v.manages(t.DepartmentID) {
		apiError(w, 403, "forbidden")
		return
	}
	rows, err := a.st.db.Query(r.Context(), `SELECT id, display_name, COALESCE(extension,'') FROM users WHERE active AND department_id=$1 ORDER BY display_name`, t.DepartmentID)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type cand struct {
		ID          int64  `json:"id"`
		DisplayName string `json:"display_name"`
		Extension   string `json:"extension"`
	}
	out := []cand{}
	for rows.Next() {
		var c cand
		if rows.Scan(&c.ID, &c.DisplayName, &c.Extension) == nil {
			out = append(out, c)
		}
	}
	writeJSON(w, 200, map[string]any{"candidates": out})
}

// ---------- workflow ----------

func (a *app) createTicket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	var req struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		Priority     string `json:"priority"`
		DepartmentID int    `json:"department_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	title, ok1 := cleanText(req.Title, 200)
	desc, ok2 := cleanText(req.Description, 5000)
	if !ok1 || !ok2 {
		apiError(w, 400, "title (max 200) and description (max 5000) are required")
		return
	}
	if req.Priority == "" {
		req.Priority = "normal"
	}
	switch req.Priority {
	case "low", "normal", "high", "urgent":
	default:
		apiError(w, 400, "invalid priority")
		return
	}
	var heads int
	if a.st.db.QueryRow(ctx, `SELECT COUNT(*) FROM department_heads h JOIN users u ON u.id=h.user_id WHERE h.department_id=$1 AND u.active`, req.DepartmentID).Scan(&heads) != nil {
		apiError(w, 500, "internal error")
		return
	}
	var exists bool
	if a.st.db.QueryRow(ctx, `SELECT true FROM departments WHERE id=$1`, req.DepartmentID).Scan(&exists) != nil {
		apiError(w, 400, "unknown service")
		return
	}
	if heads == 0 {
		apiError(w, 409, "this service has no head yet: ask IT to designate one")
		return
	}
	var id int64
	if err := a.st.db.QueryRow(ctx, `INSERT INTO tickets(title, description, priority, requester_id, department_id) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		title, desc, req.Priority, uid, req.DepartmentID).Scan(&id); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	a.ticketEvent(ctx, id, uid, "created", "")
	t, _ := a.loadTicket(ctx, id)
	a.st.audit(ctx, &uid, uname, "ticket_created", clientIP(r), map[string]any{"ticket_id": id, "department_id": req.DepartmentID, "priority": req.Priority})
	a.notifyTicket(ctx, t, "created", uid)
	t.Can = a.viewer(ctx, uid).can(t)
	writeJSON(w, 200, t)
}

// transition applies a status change atomically: it only succeeds while the ticket is still in one of `from`.
func (a *app) transition(ctx context.Context, id int64, from []string, set string, args ...any) bool {
	q := `UPDATE tickets SET ` + set + `, updated_at=now() WHERE id=$1 AND status = ANY($2)`
	tag, err := a.st.db.Exec(ctx, q, append([]any{id, from}, args...)...)
	return err == nil && tag.RowsAffected() == 1
}

func (a *app) decideTicket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, v, ok := a.ticketFor(w, r)
	if !ok {
		return
	}
	var req struct {
		Approve bool   `json:"approve"`
		Comment string `json:"comment"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	comment := strings.TrimSpace(req.Comment)
	if utf8.RuneCountInString(comment) > 2000 || (!req.Approve && comment == "") {
		apiError(w, 400, "a comment is required to reject a ticket (max 2000)")
		return
	}
	if !v.can(t).Decide {
		apiError(w, 403, "forbidden")
		return
	}
	status, kind := "approved", "approved"
	if !req.Approve {
		status, kind = "rejected", "rejected"
	}
	if !a.transition(ctx, t.ID, []string{"pending"}, `status=$3, decided_by=$4, decided_at=now(), decision_comment=NULLIF($5,'')`, status, v.uid, comment) {
		apiError(w, 409, "this ticket was already decided")
		return
	}
	_, uname := a.actor(ctx, r)
	a.ticketEvent(ctx, t.ID, v.uid, kind, comment)
	a.st.audit(ctx, &v.uid, uname, "ticket_"+kind, clientIP(r), map[string]any{"ticket_id": t.ID})
	nt, _ := a.loadTicket(ctx, t.ID)
	a.notifyTicket(ctx, nt, kind, v.uid)
	nt.Can = v.can(nt)
	writeJSON(w, 200, nt)
}

func (a *app) assignTicket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, v, ok := a.ticketFor(w, r)
	if !ok {
		return
	}
	var req struct {
		UserID  int64  `json:"user_id"`
		Comment string `json:"comment"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	comment := strings.TrimSpace(req.Comment)
	if utf8.RuneCountInString(comment) > 2000 {
		apiError(w, 400, "comment too long")
		return
	}
	if !v.can(t).Assign {
		apiError(w, 403, "forbidden")
		return
	}
	// the assignee must be an active member of the service the ticket was filed for
	var member bool
	if a.st.db.QueryRow(ctx, `SELECT true FROM users WHERE id=$1 AND active AND department_id=$2`, req.UserID, t.DepartmentID).Scan(&member) != nil {
		apiError(w, 400, "the assignee must be an active member of this service")
		return
	}
	// a re-assignment goes back to "assigned" (the new person starts from scratch)
	if !a.transition(ctx, t.ID, []string{"approved", "assigned", "in_progress"}, `status='assigned', assignee_id=$3, assigned_by=$4, assigned_at=now()`, req.UserID, v.uid) {
		apiError(w, 409, "this ticket can no longer be assigned")
		return
	}
	_, uname := a.actor(ctx, r)
	a.ticketEvent(ctx, t.ID, v.uid, "assigned", comment)
	a.st.audit(ctx, &v.uid, uname, "ticket_assigned", clientIP(r), map[string]any{"ticket_id": t.ID, "assignee_id": req.UserID})
	nt, _ := a.loadTicket(ctx, t.ID)
	var previous []int64
	if t.AssigneeID != nil {
		previous = append(previous, *t.AssigneeID)
	}
	a.notifyTicket(ctx, nt, "assigned", v.uid, previous...)
	nt.Can = v.can(nt)
	writeJSON(w, 200, nt)
}

func (a *app) startTicket(w http.ResponseWriter, r *http.Request) {
	a.progress(w, r, "start", []string{"assigned"}, `status='in_progress'`, "started", false)
}

func (a *app) resolveTicket(w http.ResponseWriter, r *http.Request) {
	a.progress(w, r, "resolve", []string{"assigned", "in_progress"}, `status='resolved', resolved_at=now(), resolution=NULLIF($3,'')`, "resolved", true)
}

func (a *app) cancelTicket(w http.ResponseWriter, r *http.Request) {
	a.progress(w, r, "cancel", []string{"pending", "approved"}, `status='cancelled'`, "cancelled", false)
}

// progress runs the simple transitions done by the assignee (start, resolve) or the requester (cancel).
func (a *app) progress(w http.ResponseWriter, r *http.Request, action string, from []string, set, kind string, withComment bool) {
	ctx := r.Context()
	t, v, ok := a.ticketFor(w, r)
	if !ok {
		return
	}
	var req struct {
		Comment string `json:"comment"`
	}
	if r.ContentLength != 0 && !decodeBody(w, r, &req) {
		return
	}
	comment := strings.TrimSpace(req.Comment)
	if utf8.RuneCountInString(comment) > 2000 {
		apiError(w, 400, "comment too long")
		return
	}
	c := v.can(t)
	allowed := map[string]bool{"start": c.Start, "resolve": c.Resolve, "cancel": c.Cancel}[action]
	if !allowed {
		apiError(w, 403, "forbidden")
		return
	}
	args := []any{}
	if withComment {
		args = append(args, comment)
	}
	if !a.transition(ctx, t.ID, from, set, args...) {
		apiError(w, 409, "this ticket changed in the meantime")
		return
	}
	_, uname := a.actor(ctx, r)
	a.ticketEvent(ctx, t.ID, v.uid, kind, comment)
	a.st.audit(ctx, &v.uid, uname, "ticket_"+kind, clientIP(r), map[string]any{"ticket_id": t.ID})
	nt, _ := a.loadTicket(ctx, t.ID)
	a.notifyTicket(ctx, nt, kind, v.uid)
	nt.Can = v.can(nt)
	writeJSON(w, 200, nt)
}

func (a *app) commentTicket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, v, ok := a.ticketFor(w, r)
	if !ok {
		return
	}
	var req struct {
		Comment string `json:"comment"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	comment, ok := cleanText(req.Comment, 2000)
	if !ok {
		apiError(w, 400, "comment required (max 2000)")
		return
	}
	a.ticketEvent(ctx, t.ID, v.uid, "comment", comment)
	_, _ = a.st.db.Exec(ctx, `UPDATE tickets SET updated_at=now() WHERE id=$1`, t.ID)
	a.notifyTicket(ctx, t, "comment", v.uid)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- administration: heads of service ----------

func (a *app) adminDepartments(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.db.Query(r.Context(), `
		SELECT d.id, d.name, COALESCE((SELECT json_agg(json_build_object('id', u.id, 'display_name', u.display_name) ORDER BY u.display_name)
		                              FROM department_heads h JOIN users u ON u.id=h.user_id WHERE h.department_id=d.id), '[]'::json)
		FROM departments d ORDER BY d.name`)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type dep struct {
		ID    int             `json:"id"`
		Name  string          `json:"name"`
		Heads json.RawMessage `json:"heads"`
	}
	out := []dep{}
	for rows.Next() {
		var d dep
		if rows.Scan(&d.ID, &d.Name, &d.Heads) == nil {
			out = append(out, d)
		}
	}
	writeJSON(w, 200, map[string]any{"departments": out})
}

// setDepartmentHeads replaces the heads of a service with the given active users (usually members of the service).
func (a *app) setDepartmentHeads(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	dept, err := strconv.Atoi(chi.URLParam(r, "id"))
	var req struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if err != nil || !decodeBody(w, r, &req) || len(req.UserIDs) > 10 {
		apiError(w, 400, "invalid request")
		return
	}
	var exists bool
	if a.st.db.QueryRow(ctx, `SELECT true FROM departments WHERE id=$1`, dept).Scan(&exists) != nil {
		apiError(w, 404, "not found")
		return
	}
	tx, err := a.st.db.Begin(ctx)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM department_heads WHERE department_id=$1`, dept); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	for _, u := range req.UserIDs {
		tag, err := tx.Exec(ctx, `INSERT INTO department_heads(department_id, user_id) SELECT $1, id FROM users WHERE id=$2 AND active ON CONFLICT DO NOTHING`, dept, u)
		if err != nil || tag.RowsAffected() != 1 {
			apiError(w, 400, "unknown or inactive user")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	a.st.audit(ctx, &uid, uname, "department_heads_set", clientIP(r), map[string]any{"department_id": dept, "user_ids": req.UserIDs})
	writeJSON(w, 200, map[string]any{"ok": true})
}
