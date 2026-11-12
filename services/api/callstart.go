package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const sipChannel = "eximphone:sip"

var (
	errCallee = errors.New("callee not found")
	errInCall = errors.New("already in a call")
)

// sipCommand tells the sip service what to do with desk phones (fire and forget over Redis).
func (a *app) sipCommand(ctx context.Context, cmd map[string]any) {
	if b, err := json.Marshal(cmd); err == nil {
		a.rdb.Publish(ctx, sipChannel, b)
	}
}

type startResult struct {
	ID      string
	State   string // ringing | ended
	Outcome string // set when the call never rang: offline | dnd | busy
	Callee  int64
	Silent  bool
}

// startCall is the routing decision shared by PC callers (REST) and phone callers (sip service):
// delegate forwarding, offline / do-not-disturb / busy handling, ringing every device of the callee.
func (a *app) startCall(ctx context.Context, caller, calleeID int64) (*startResult, error) {
	if v, err := a.rdb.Get(ctx, activeKey(caller)).Result(); err == nil && v != "" {
		return nil, errInCall
	}
	callee, err := a.st.getUser(ctx, calleeID)
	if err != nil || !callee.Active {
		return nil, errCallee
	}
	status, err := a.rdb.HGet(ctx, "presence", strconv.FormatInt(callee.ID, 10)).Result()
	offline := errors.Is(err, redis.Nil)

	// "Away" forwards to the delegate configured by the callee.
	if !offline && status == "away" {
		var delegate *int64
		if a.st.db.QueryRow(ctx, `SELECT delegate_user_id FROM users WHERE id=$1`, callee.ID).Scan(&delegate) == nil && delegate != nil && *delegate != caller {
			if d, err := a.st.getUser(ctx, *delegate); err == nil && d.Active {
				callee = d
				status, err = a.rdb.HGet(ctx, "presence", strconv.FormatInt(d.ID, 10)).Result()
				offline = errors.Is(err, redis.Nil)
			}
		}
	}

	outcome := ""
	switch {
	case offline:
		outcome = "offline" // not clocked in: voicemail arrives with a later phase
	case status == "dnd":
		outcome = "dnd" // equivalent of SIP 486 Busy Here + voicemail
	}
	if outcome != "" {
		id, err := a.insertEnded(ctx, caller, callee.ID, outcome)
		if err != nil {
			return nil, err
		}
		return &startResult{ID: id, State: "ended", Outcome: outcome, Callee: callee.ID}, nil
	}

	var id string
	if err := a.st.db.QueryRow(ctx, `INSERT INTO calls(caller_id, callee_id, state) VALUES ($1,$2,'ringing') RETURNING id::text`, caller, callee.ID).Scan(&id); err != nil {
		return nil, err
	}
	if !a.claim(ctx, caller, id) {
		_, _ = a.st.db.Exec(ctx, `DELETE FROM calls WHERE id=$1::uuid`, id)
		return nil, errInCall
	}
	if !a.claim(ctx, callee.ID, id) { // callee already in another call: SIP 486 Busy Here
		a.release(ctx, caller, id)
		_, _ = a.st.db.Exec(ctx, `UPDATE calls SET state='ended', outcome='busy', ended_at=now(), duration_sec=0 WHERE id=$1::uuid`, id)
		return &startResult{ID: id, State: "ended", Outcome: "busy", Callee: callee.ID}, nil
	}
	silent := status == "meet" // "in meeting": rings without sound
	a.hub.publish(ctx, []int64{callee.ID}, "call_incoming", map[string]any{"call_id": id, "from": a.party(ctx, caller), "silent": silent})
	// Ring the callee's desk phones too (forking): the first device to answer wins.
	if n, _ := a.rdb.HLen(ctx, "sip:reg:"+strconv.FormatInt(callee.ID, 10)).Result(); n > 0 {
		a.sipCommand(ctx, map[string]any{"type": "originate", "call_id": id, "callee_user_id": callee.ID, "from_user_id": caller})
	}
	time.AfterFunc(ringTimeout, func() { a.finish(context.Background(), id, "missed", 0, true) })
	return &startResult{ID: id, State: "ringing", Callee: callee.ID, Silent: silent}, nil
}

func (a *app) createCall(w http.ResponseWriter, r *http.Request) {
	ctx, caller := r.Context(), userID(r)
	var req struct {
		ToUserID int64 `json:"to_user_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&req) != nil || req.ToUserID == 0 || req.ToUserID == caller {
		apiError(w, 400, "invalid request")
		return
	}
	res, err := a.startCall(ctx, caller, req.ToUserID)
	switch {
	case errors.Is(err, errCallee):
		apiError(w, 404, "user not found")
	case errors.Is(err, errInCall):
		apiError(w, 409, "already in a call")
	case err != nil:
		apiError(w, 500, "internal error")
	case res.State == "ended":
		writeJSON(w, 200, map[string]any{"id": res.ID, "state": "ended", "outcome": res.Outcome})
	default:
		writeJSON(w, 200, map[string]any{"id": res.ID, "state": "ringing", "to": a.party(ctx, res.Callee)})
	}
}
