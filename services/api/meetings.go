package main

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
)

// Video meetings and one-to-one video calls.
//
// The api only manages who is invited and who took part, and issues a short-lived token; the media (camera,
// microphone, screen) goes through the media service, which forwards it between the participants (up to 12).
// A meeting ends when its host ends it or when nobody is left in it.

const (
	meetingMaxInvited = 50
	meetingMaxLive    = 12
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (a *app) meetingRoutes(r chi.Router) {
	r.Get("/meetings", a.listMeetings)
	r.Post("/meetings", a.createMeeting)
	r.Get("/meetings/{id}", a.getMeeting)
	r.Post("/meetings/{id}/token", a.meetingToken)
	r.Post("/meetings/{id}/invite", a.inviteToMeeting)
	r.Post("/meetings/{id}/decline", a.declineMeeting)
	r.Post("/meetings/{id}/leave", a.leaveMeeting)
	r.Post("/meetings/{id}/end", a.endMeeting)
}

type meetingPerson struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type meetingJSON struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Kind         string          `json:"kind"`
	HostID       int64           `json:"host_id"`
	Host         string          `json:"host"`
	CreatedAt    time.Time       `json:"created_at"`
	EndedAt      *time.Time      `json:"ended_at"`
	Me           string          `json:"me"` // my status: invited | joined | declined | left
	Participants []meetingPerson `json:"participants"`
}

func meetingID(r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	return id, uuidRE.MatchString(id)
}

const meetingSelect = `
	SELECT m.id::text, m.title, m.kind, m.host_id, h.display_name, m.created_at, m.ended_at, COALESCE(me.status,'')
	FROM meetings m JOIN users h ON h.id=m.host_id LEFT JOIN meeting_participants me ON me.meeting_id=m.id AND me.user_id=$1`

func (a *app) loadMeetings(ctx context.Context, uid int64, where string, args ...any) ([]meetingJSON, error) {
	rows, err := a.st.db.Query(ctx, meetingSelect+` WHERE `+where, append([]any{uid}, args...)...)
	if err != nil {
		return nil, err
	}
	var out []meetingJSON
	var ids []string
	for rows.Next() {
		var m meetingJSON
		if rows.Scan(&m.ID, &m.Title, &m.Kind, &m.HostID, &m.Host, &m.CreatedAt, &m.EndedAt, &m.Me) == nil {
			m.Participants = []meetingPerson{}
			out = append(out, m)
			ids = append(ids, m.ID)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return out, nil
	}
	idx := map[string]*meetingJSON{}
	for i := range out {
		idx[out[i].ID] = &out[i]
	}
	pr, err := a.st.db.Query(ctx, `SELECT p.meeting_id::text, u.id, u.display_name, p.status FROM meeting_participants p JOIN users u ON u.id=p.user_id
		WHERE p.meeting_id::text = ANY($1) ORDER BY p.invited_at, u.display_name`, ids)
	if err != nil {
		return out, nil
	}
	defer pr.Close()
	for pr.Next() {
		var mid string
		var p meetingPerson
		if pr.Scan(&mid, &p.ID, &p.Name, &p.Status) == nil {
			idx[mid].Participants = append(idx[mid].Participants, p)
		}
	}
	return out, nil
}

// myMeeting returns the meeting if uid takes part in it (host or invited).
func (a *app) myMeeting(ctx context.Context, uid int64, id string) (meetingJSON, bool) {
	ms, err := a.loadMeetings(ctx, uid, `m.id::text=$2 AND me.user_id IS NOT NULL`, id)
	if err != nil || len(ms) != 1 {
		return meetingJSON{}, false
	}
	return ms[0], true
}

func (m meetingJSON) everyone() []int64 {
	out := make([]int64, 0, len(m.Participants))
	for _, p := range m.Participants {
		out = append(out, p.ID)
	}
	return out
}

func (a *app) listMeetings(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	where := `me.user_id IS NOT NULL AND m.ended_at IS NULL AND me.status <> 'declined' AND m.created_at > now() - interval '12 hours'`
	if r.URL.Query().Get("view") == "recent" {
		where = `me.user_id IS NOT NULL AND (m.ended_at IS NOT NULL OR m.created_at <= now() - interval '12 hours') ORDER BY m.created_at DESC LIMIT 30`
	} else {
		where += ` ORDER BY m.created_at DESC LIMIT 50`
	}
	ms, err := a.loadMeetings(ctx, uid, where)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	if ms == nil {
		ms = []meetingJSON{}
	}
	writeJSON(w, 200, map[string]any{"meetings": ms})
}

func (a *app) createMeeting(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	var req struct {
		Title    string  `json:"title"`
		Kind     string  `json:"kind"`
		Invitees []int64 `json:"invitees"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	title := strings.TrimSpace(req.Title)
	if req.Kind == "" {
		req.Kind = "meeting"
	}
	if req.Kind != "meeting" && req.Kind != "video_call" {
		apiError(w, 400, "invalid kind")
		return
	}
	if title == "" {
		title = map[string]string{"meeting": "Réunion", "video_call": "Appel vidéo"}[req.Kind]
	}
	if utf8.RuneCountInString(title) > 200 {
		apiError(w, 400, "title too long")
		return
	}
	seen := map[int64]bool{uid: true}
	var invitees []int64
	for _, u := range req.Invitees {
		if !seen[u] {
			seen[u] = true
			invitees = append(invitees, u)
		}
	}
	if len(invitees) > meetingMaxLive-1 || (req.Kind == "video_call" && len(invitees) != 1) {
		apiError(w, 400, "a video call needs exactly one person; a meeting takes up to 11 invitees")
		return
	}
	if len(invitees) > 0 {
		var n int
		if a.st.db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE active AND id = ANY($1)`, invitees).Scan(&n) != nil || n != len(invitees) {
			apiError(w, 400, "unknown or inactive invitee")
			return
		}
	}
	tx, err := a.st.db.Begin(ctx)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO meetings(title, host_id, kind) VALUES ($1,$2,$3) RETURNING id::text`, title, uid, req.Kind).Scan(&id); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO meeting_participants(meeting_id, user_id, invited_by) VALUES ($1::uuid,$2,$2)`, id, uid); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	for _, u := range invitees {
		if _, err := tx.Exec(ctx, `INSERT INTO meeting_participants(meeting_id, user_id, invited_by) VALUES ($1::uuid,$2,$3)`, id, u, uid); err != nil {
			apiError(w, 500, "internal error")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	a.st.audit(ctx, &uid, uname, "meeting_created", clientIP(r), map[string]any{"meeting_id": id, "kind": req.Kind, "invitees": len(invitees)})
	m, _ := a.myMeeting(ctx, uid, id)
	a.hub.publish(ctx, invitees, "meeting_invite", map[string]any{"id": id, "title": title, "kind": req.Kind, "host_id": uid, "host": m.Host})
	writeJSON(w, 200, m)
}

func (a *app) getMeeting(w http.ResponseWriter, r *http.Request) {
	id, ok := meetingID(r)
	if !ok {
		apiError(w, 404, "not found")
		return
	}
	m, found := a.myMeeting(r.Context(), userID(r), id)
	if !found {
		apiError(w, 404, "not found")
		return
	}
	writeJSON(w, 200, m)
}

// meetingToken lets a participant open the media connection of an open meeting.
func (a *app) meetingToken(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id, ok := meetingID(r)
	if !ok {
		apiError(w, 404, "not found")
		return
	}
	m, found := a.myMeeting(ctx, uid, id)
	if !found || m.EndedAt != nil {
		apiError(w, 404, "no open meeting")
		return
	}
	if _, err := a.st.db.Exec(ctx, `UPDATE meeting_participants SET status='joined', joined_at=COALESCE(joined_at, now()), left_at=NULL WHERE meeting_id=$1::uuid AND user_id=$2`, id, uid); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	now := a.now()
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": strconv.FormatInt(uid, 10), "mid": id, "aud": "meeting", "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(),
	}).SignedString(a.jwtSecret)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	a.hub.publish(ctx, m.everyone(), "meeting_update", map[string]any{"id": id})
	writeJSON(w, 200, map[string]any{"token": tok, "title": m.Title, "kind": m.Kind, "host_id": m.HostID})
}

func (a *app) inviteToMeeting(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id, ok := meetingID(r)
	var req struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if !ok || !decodeBody(w, r, &req) || len(req.UserIDs) == 0 || len(req.UserIDs) > meetingMaxLive {
		apiError(w, 400, "invalid request")
		return
	}
	m, found := a.myMeeting(ctx, uid, id)
	if !found || m.EndedAt != nil || m.Me == "declined" || m.Kind == "video_call" {
		apiError(w, 404, "no open meeting")
		return
	}
	if len(m.Participants)+len(req.UserIDs) > meetingMaxInvited {
		apiError(w, 400, "too many people invited")
		return
	}
	var n int
	if a.st.db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE active AND id = ANY($1)`, req.UserIDs).Scan(&n) != nil || n != len(req.UserIDs) {
		apiError(w, 400, "unknown or inactive invitee")
		return
	}
	var added []int64
	for _, u := range req.UserIDs {
		tag, err := a.st.db.Exec(ctx, `INSERT INTO meeting_participants(meeting_id, user_id, invited_by) VALUES ($1::uuid,$2,$3) ON CONFLICT DO NOTHING`, id, u, uid)
		if err == nil && tag.RowsAffected() == 1 {
			added = append(added, u)
		}
	}
	a.hub.publish(ctx, added, "meeting_invite", map[string]any{"id": id, "title": m.Title, "kind": m.Kind, "host_id": m.HostID, "host": m.Host})
	a.hub.publish(ctx, m.everyone(), "meeting_update", map[string]any{"id": id})
	m, _ = a.myMeeting(ctx, uid, id)
	writeJSON(w, 200, m)
}

func (a *app) declineMeeting(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id, ok := meetingID(r)
	if !ok {
		apiError(w, 404, "not found")
		return
	}
	tag, err := a.st.db.Exec(ctx, `UPDATE meeting_participants SET status='declined' WHERE meeting_id=$1::uuid AND user_id=$2 AND status='invited'`, id, uid)
	if err != nil || tag.RowsAffected() != 1 {
		apiError(w, 409, "nothing to decline")
		return
	}
	var kind string
	var all []int64
	if m, found := a.myMeeting(ctx, uid, id); found {
		kind, all = m.Kind, m.everyone()
	}
	if kind == "video_call" { // the callee refused: the call is over for the caller too
		if tag, _ := a.st.db.Exec(ctx, `UPDATE meetings SET ended_at=now() WHERE id=$1::uuid AND ended_at IS NULL`, id); tag.RowsAffected() > 0 {
			a.dropMeetingMedia(id)
			a.hub.publish(ctx, all, "meeting_ended", map[string]any{"id": id, "declined_by": uid})
		}
	} else {
		a.afterMeetingChange(ctx, id)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) leaveMeeting(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id, ok := meetingID(r)
	if !ok {
		apiError(w, 404, "not found")
		return
	}
	a.markLeft(ctx, id, uid)
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) markLeft(ctx context.Context, id string, uid int64) {
	tag, _ := a.st.db.Exec(ctx, `UPDATE meeting_participants SET status='left', left_at=now() WHERE meeting_id=$1::uuid AND user_id=$2 AND status='joined'`, id, uid)
	if tag.RowsAffected() > 0 {
		a.afterMeetingChange(ctx, id)
	}
}

// afterMeetingChange ends the meeting when nobody is in it any more (everybody declined or left) and tells the participants.
func (a *app) afterMeetingChange(ctx context.Context, id string) {
	var joined int
	_ = a.st.db.QueryRow(ctx, `SELECT COUNT(*) FROM meeting_participants WHERE meeting_id=$1::uuid AND status='joined'`, id).Scan(&joined)
	var ids []int64
	if rows, err := a.st.db.Query(ctx, `SELECT user_id FROM meeting_participants WHERE meeting_id=$1::uuid`, id); err == nil {
		for rows.Next() {
			var u int64
			if rows.Scan(&u) == nil {
				ids = append(ids, u)
			}
		}
		rows.Close()
	}
	if joined == 0 {
		// the host may not have opened the room yet: only close the meeting once the host has joined, or if everyone else declined
		var hostJoined, pendingInvites int
		_ = a.st.db.QueryRow(ctx, `SELECT COUNT(*) FILTER (WHERE p.user_id=m.host_id AND p.status IN ('left','joined')),
			COUNT(*) FILTER (WHERE p.status='invited' AND p.user_id<>m.host_id)
			FROM meetings m JOIN meeting_participants p ON p.meeting_id=m.id WHERE m.id=$1::uuid`, id).Scan(&hostJoined, &pendingInvites)
		if hostJoined > 0 || pendingInvites == 0 {
			if tag, _ := a.st.db.Exec(ctx, `UPDATE meetings SET ended_at=now() WHERE id=$1::uuid AND ended_at IS NULL`, id); tag.RowsAffected() > 0 {
				a.hub.publish(ctx, ids, "meeting_ended", map[string]any{"id": id})
				return
			}
		}
	}
	a.hub.publish(ctx, ids, "meeting_update", map[string]any{"id": id})
}

func (a *app) endMeeting(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	id, ok := meetingID(r)
	if !ok {
		apiError(w, 404, "not found")
		return
	}
	m, found := a.myMeeting(ctx, uid, id)
	if !found {
		apiError(w, 404, "not found")
		return
	}
	if m.HostID != uid {
		apiError(w, 403, "only the host can end the meeting for everybody")
		return
	}
	tag, err := a.st.db.Exec(ctx, `UPDATE meetings SET ended_at=now() WHERE id=$1::uuid AND ended_at IS NULL`, id)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	if tag.RowsAffected() > 0 {
		_, _ = a.st.db.Exec(ctx, `UPDATE meeting_participants SET status='left', left_at=now() WHERE meeting_id=$1::uuid AND status='joined'`, id)
		a.dropMeetingMedia(id)
		a.st.audit(ctx, &uid, uname, "meeting_ended", clientIP(r), map[string]any{"meeting_id": id})
		a.hub.publish(ctx, m.everyone(), "meeting_ended", map[string]any{"id": id})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) dropMeetingMedia(id string) {
	go func() {
		base := os.Getenv("MEDIA_URL")
		if base == "" {
			base = "http://media:8080"
		}
		req, err := http.NewRequest(http.MethodDelete, base+"/meetings/"+id, nil)
		if err != nil {
			return
		}
		req.Header.Set("X-Internal-Token", os.Getenv("INTERNAL_TOKEN"))
		c := http.Client{Timeout: 3 * time.Second}
		if res, err := c.Do(req); err == nil {
			res.Body.Close()
		}
	}()
}

// internalMeetingLeft: the media service reports that a participant's connection closed.
func (a *app) internalMeetingLeft(w http.ResponseWriter, r *http.Request) {
	id, ok := meetingID(r)
	var body struct {
		UserID int64 `json:"user_id"`
	}
	if !ok || !decodeBody(w, r, &body) {
		return
	}
	a.markLeft(r.Context(), id, body.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// internalMeetingRC: the media service reports that remote control of a shared screen started or stopped.
// Both people are named in the audit log: this is the trace of who could act on whose computer, and for how long.
func (a *app) internalMeetingRC(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := meetingID(r)
	var body struct {
		Owner      int64  `json:"owner"`
		Controller int64  `json:"controller"`
		Action     string `json:"action"`
		Reason     string `json:"reason"`
		Seconds    int    `json:"seconds"`
	}
	if !ok || !decodeBody(w, r, &body) || (body.Action != "started" && body.Action != "stopped") || body.Owner == 0 || body.Controller == 0 {
		apiError(w, 400, "invalid request")
		return
	}
	var ownerName, controllerName string
	_ = a.st.db.QueryRow(ctx, `SELECT username::text FROM users WHERE id=$1`, body.Owner).Scan(&ownerName)
	_ = a.st.db.QueryRow(ctx, `SELECT username::text FROM users WHERE id=$1`, body.Controller).Scan(&controllerName)
	details := map[string]any{"meeting_id": id, "owner_id": body.Owner, "owner": ownerName}
	if body.Action == "stopped" {
		details["reason"] = body.Reason
		details["seconds"] = body.Seconds
	}
	a.st.audit(ctx, &body.Controller, controllerName, "remote_control_"+body.Action, "", details)
	w.WriteHeader(http.StatusNoContent)
}
