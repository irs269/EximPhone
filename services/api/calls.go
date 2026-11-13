package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/irs269/eximphone/services/pkg/turncred"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

const (
	ringTimeout = 45 * time.Second
	activeTTL   = 4 * time.Hour
)

type partyJSON struct {
	ID        int64  `json:"id"`
	Name      string `json:"display_name"`
	Extension string `json:"extension"`
}

func (a *app) party(ctx context.Context, id int64) partyJSON {
	u, err := a.st.getUser(ctx, id)
	if err != nil {
		return partyJSON{ID: id}
	}
	return partyJSON{ID: u.ID, Name: u.DisplayName, Extension: u.Extension}
}

func (a *app) callRoutes(r chi.Router) {
	r.Get("/calls", a.listCalls)
	r.Get("/calls/ice", a.iceServers)
	r.Post("/calls", a.createCall)
	r.Post("/calls/{id}/answer", a.answerCall)
	r.Post("/calls/{id}/reject", a.hangupCall)
	r.Post("/calls/{id}/hangup", a.hangupCall)
	r.Post("/calls/{id}/hold", a.holdCall)
	r.Post("/calls/{id}/transfer", a.transferCall)
	r.Get("/calls/{id}/token", a.mediaToken)
}

func activeKey(uid int64) string { return "call:active:" + strconv.FormatInt(uid, 10) }

// claim marks a user as being in a call. It fails if they already are.
func (a *app) claim(ctx context.Context, uid int64, callID string) bool {
	ok, _ := a.rdb.SetNX(ctx, activeKey(uid), callID, activeTTL).Result()
	return ok
}

func (a *app) release(ctx context.Context, uid int64, callID string) {
	if v, err := a.rdb.Get(ctx, activeKey(uid)).Result(); err == nil && v == callID {
		a.rdb.Del(ctx, activeKey(uid))
	}
}

// enterCall / leaveCall switch a user to "busy" during a call and restore the previous status afterwards.
func (a *app) enterCall(ctx context.Context, uid int64) {
	field := strconv.FormatInt(uid, 10)
	prev, err := a.rdb.HGet(ctx, "presence", field).Result()
	if err != nil || prev == "busy" {
		return
	}
	a.rdb.HSet(ctx, "presence_prev", field, prev)
	a.setPresence(ctx, uid, "busy")
}

func (a *app) leaveCall(ctx context.Context, uid int64) {
	field := strconv.FormatInt(uid, 10)
	prev, err := a.rdb.HGet(ctx, "presence_prev", field).Result()
	if err != nil {
		return
	}
	a.rdb.HDel(ctx, "presence_prev", field)
	if cur, err := a.rdb.HGet(ctx, "presence", field).Result(); err == nil && cur == "busy" { // not changed by the user meanwhile
		a.setPresence(ctx, uid, prev)
	}
}

func (a *app) dropMedia(callID string) {
	go func() {
		base := os.Getenv("MEDIA_URL")
		if base == "" {
			base = "http://media:8080"
		}
		req, err := http.NewRequest(http.MethodDelete, base+"/calls/"+callID, nil)
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

// insertEnded records a call that never rang (callee offline, in DND or busy).
func (a *app) insertEnded(ctx context.Context, caller, callee int64, outcome string) (string, error) {
	var id string
	err := a.st.db.QueryRow(ctx, `INSERT INTO calls(caller_id, callee_id, state, outcome, ended_at, duration_sec)
		VALUES ($1,$2,'ended',$3,now(),0) RETURNING id::text`, caller, callee, outcome).Scan(&id)
	return id, err
}

// finish ends a call exactly once and tells both parties. byUser is 0 for system-initiated ends;
// onlyRinging makes the ring timeout harmless for calls that were answered in the meantime.
func (a *app) finish(ctx context.Context, callID, ringingOutcome string, byUser int64, onlyRinging bool) (found bool) {
	var caller, callee int64
	var answered *time.Time
	err := a.st.db.QueryRow(ctx, `UPDATE calls SET state='ended', ended_at=now(),
		  outcome = CASE WHEN state='active' THEN 'answered' ELSE $2 END,
		  duration_sec = CASE WHEN state='active' THEN GREATEST(0, EXTRACT(EPOCH FROM (now() - answered_at)))::int ELSE 0 END
		WHERE id=$1::uuid AND state<>'ended' AND ($3 = false OR state='ringing') RETURNING caller_id, callee_id, answered_at`, callID, ringingOutcome, onlyRinging).Scan(&caller, &callee, &answered)
	if err != nil {
		return false
	}
	for _, u := range []int64{caller, callee} {
		a.release(ctx, u, callID)
		if answered != nil {
			a.leaveCall(ctx, u)
		}
	}
	outcome := ringingOutcome
	if answered != nil {
		outcome = "answered"
	}
	a.hub.publish(ctx, []int64{caller, callee}, "call_ended", map[string]any{"call_id": callID, "outcome": outcome, "by": byUser})
	a.sipCommand(ctx, map[string]any{"type": "ended", "call_id": callID}) // BYE/CANCEL toward desk phones
	a.dropMedia(callID)
	return true
}

func (a *app) answerCall(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id := chi.URLParam(r, "id")
	var caller int64
	err := a.st.db.QueryRow(ctx, `UPDATE calls SET state='active', answered_at=now() WHERE id=$1::uuid AND callee_id=$2 AND state='ringing'
		RETURNING caller_id`, id, uid).Scan(&caller)
	if err != nil {
		apiError(w, 404, "no ringing call")
		return
	}
	a.enterCall(ctx, caller)
	a.enterCall(ctx, uid)
	recorded := a.maybeRecord(ctx, id, caller, uid)
	a.hub.publish(ctx, []int64{caller, uid}, "call_answered", map[string]any{"call_id": id, "via": "pc", "recorded": recorded})
	a.sipCommand(ctx, map[string]any{"type": "pc_answered", "call_id": id}) // cancel the desk phones that were ringing
	writeJSON(w, 200, map[string]string{"id": id, "state": "active"})
}

// hangupCall serves both /hangup (either party) and /reject (callee refusing a ringing call).
func (a *app) hangupCall(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id := chi.URLParam(r, "id")
	var caller, callee int64
	var state string
	if err := a.st.db.QueryRow(ctx, `SELECT caller_id, callee_id, state FROM calls WHERE id=$1::uuid`, id).Scan(&caller, &callee, &state); err != nil || (uid != caller && uid != callee) {
		apiError(w, 404, "not found")
		return
	}
	outcome := "cancelled" // caller hangs up while ringing
	if uid == callee {
		outcome = "rejected"
	}
	if !a.finish(ctx, id, outcome, uid, false) {
		apiError(w, 409, "call already ended")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) participants(ctx context.Context, id string, uid int64) (caller, callee int64, state string, ok bool) {
	err := a.st.db.QueryRow(ctx, `SELECT caller_id, callee_id, state FROM calls WHERE id=$1::uuid`, id).Scan(&caller, &callee, &state)
	return caller, callee, state, err == nil && (uid == caller || uid == callee)
}

func (a *app) holdCall(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id := chi.URLParam(r, "id")
	var req struct {
		On bool `json:"on"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128)).Decode(&req) != nil {
		apiError(w, 400, "invalid request")
		return
	}
	caller, callee, state, ok := a.participants(ctx, id, uid)
	if !ok || state != "active" {
		apiError(w, 404, "no active call")
		return
	}
	other := caller
	if uid == caller {
		other = callee
	}
	a.hub.publish(ctx, []int64{other}, "call_hold", map[string]any{"call_id": id, "on": req.On, "by": uid})
	w.WriteHeader(http.StatusNoContent)
}

// transferCall is a blind transfer: the requester drops out and the other party is connected to the target.
func (a *app) transferCall(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id := chi.URLParam(r, "id")
	var req struct {
		ToUserID int64 `json:"to_user_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128)).Decode(&req) != nil || req.ToUserID == 0 {
		apiError(w, 400, "invalid request")
		return
	}
	caller, callee, state, ok := a.participants(ctx, id, uid)
	if !ok || state != "active" {
		apiError(w, 404, "no active call")
		return
	}
	transferee := caller // the party being transferred is the one who is not transferring
	if uid == caller {
		transferee = callee
	}
	if req.ToUserID == caller || req.ToUserID == callee {
		apiError(w, 400, "invalid transfer target")
		return
	}
	target, err := a.st.getUser(ctx, req.ToUserID)
	if err != nil || !target.Active {
		apiError(w, 404, "user not found")
		return
	}
	status, err := a.rdb.HGet(ctx, "presence", strconv.FormatInt(target.ID, 10)).Result()
	if errors.Is(err, redis.Nil) || status == "dnd" {
		apiError(w, 409, "target unavailable")
		return
	}

	var newID string
	tx, err := a.st.db.Begin(ctx)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `INSERT INTO calls(caller_id, callee_id, state, transferred_from) VALUES ($1,$2,'ringing',$3::uuid) RETURNING id::text`,
		transferee, target.ID, id).Scan(&newID); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	tag, err := tx.Exec(ctx, `UPDATE calls SET state='ended', outcome='transferred', ended_at=now(),
		duration_sec=GREATEST(0, EXTRACT(EPOCH FROM (now() - answered_at)))::int WHERE id=$1::uuid AND state='active'`, id)
	if err != nil || tag.RowsAffected() != 1 {
		apiError(w, 409, "call already ended")
		return
	}
	if !a.claim(ctx, target.ID, newID) {
		apiError(w, 409, "target is busy")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.release(ctx, target.ID, newID)
		apiError(w, 500, "internal error")
		return
	}
	// The transferee keeps their "in a call" mark but now points at the new call.
	a.rdb.Set(ctx, activeKey(transferee), newID, activeTTL)
	a.release(ctx, uid, id)
	a.leaveCall(ctx, uid)
	a.dropMedia(id)

	a.hub.publish(ctx, []int64{uid}, "call_ended", map[string]any{"call_id": id, "outcome": "transferred", "by": uid})
	a.hub.publish(ctx, []int64{transferee}, "call_transferred", map[string]any{"old_call_id": id, "call_id": newID, "to": a.party(ctx, target.ID)})
	a.hub.publish(ctx, []int64{target.ID}, "call_incoming", map[string]any{
		"call_id": newID, "from": a.party(ctx, transferee), "silent": status == "meet", "transferred_by": a.party(ctx, uid),
	})
	time.AfterFunc(ringTimeout, func() { a.finish(context.Background(), newID, "missed", 0, true) })
	writeJSON(w, 200, map[string]string{"id": newID})
}

// mediaToken lets a participant of an active call open a WebRTC leg on the media service.
func (a *app) mediaToken(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id := chi.URLParam(r, "id")
	if _, _, state, ok := a.participants(ctx, id, uid); !ok || state != "active" {
		apiError(w, 404, "no active call")
		return
	}
	now := a.now()
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": strconv.FormatInt(uid, 10), "cid": id, "aud": "media", "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(),
	}).SignedString(a.jwtSecret)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, map[string]string{"token": tok})
}

func (a *app) iceServers(w http.ResponseWriter, r *http.Request) {
	host := os.Getenv("TURN_PUBLIC_HOST")
	if host == "" {
		host = "localhost"
	}
	user, pass := turncred.Generate(os.Getenv("TURN_SECRET"), userID(r), a.now(), time.Hour)
	writeJSON(w, 200, map[string]any{"ice_servers": []map[string]any{
		{"urls": []string{"stun:" + host + ":3478"}},
		{"urls": []string{"turn:" + host + ":3478?transport=udp", "turn:" + host + ":3478?transport=tcp"}, "username": user, "credential": pass},
	}})
}

func (a *app) listCalls(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	rows, err := a.st.db.Query(ctx, `SELECT id::text, caller_id, callee_id, COALESCE(outcome,''), state, created_at, COALESCE(duration_sec,0)
		FROM calls WHERE caller_id=$1 OR callee_id=$1 ORDER BY created_at DESC LIMIT 50`, uid)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type row struct {
		ID        string    `json:"id"`
		Direction string    `json:"direction"` // in | out
		Other     partyJSON `json:"other"`
		Outcome   string    `json:"outcome"`
		State     string    `json:"state"`
		CreatedAt time.Time `json:"created_at"`
		Duration  int       `json:"duration_sec"`
	}
	var out []row
	var others []int64
	for rows.Next() {
		var x row
		var caller, callee int64
		if err := rows.Scan(&x.ID, &caller, &callee, &x.Outcome, &x.State, &x.CreatedAt, &x.Duration); err != nil {
			apiError(w, 500, "internal error")
			return
		}
		other := callee
		x.Direction = "out"
		if callee == uid {
			other, x.Direction = caller, "in"
		}
		x.Other = partyJSON{ID: other}
		out = append(out, x)
		others = append(others, other)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		apiError(w, 500, "internal error")
		return
	}
	cache := map[int64]partyJSON{}
	for i := range out {
		id := out[i].Other.ID
		if _, ok := cache[id]; !ok {
			cache[id] = a.party(ctx, id)
		}
		out[i].Other = cache[id]
	}
	_ = others
	if out == nil {
		out = []row{}
	}
	writeJSON(w, 200, map[string]any{"calls": out})
}
