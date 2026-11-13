package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// internalOnly guards the endpoints used by the sip service. nginx does not expose them either.
func internalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := os.Getenv("INTERNAL_TOKEN")
		if want == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Token")), []byte(want)) != 1 {
			apiError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *app) internalRoutes(r chi.Router) {
	r.Use(internalOnly)
	r.Post("/calls", a.internalCreateCall)
	r.Post("/calls/{id}/answered", a.internalAnswered)
	r.Post("/calls/{id}/hangup", a.internalHangup)
	r.Post("/recordings", a.internalRecordings)
}

// internalCreateCall: a desk phone dials an extension. Same routing rules as a PC call.
func (a *app) internalCreateCall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CallerUserID int64  `json:"caller_user_id"`
		ToExtension  string `json:"to_extension"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&req) != nil || req.CallerUserID == 0 || req.ToExtension == "" {
		apiError(w, 400, "invalid request")
		return
	}
	var calleeID int64
	if err := a.st.db.QueryRow(r.Context(), `SELECT id FROM users WHERE extension=$1 AND active`, req.ToExtension).Scan(&calleeID); err != nil || calleeID == req.CallerUserID {
		apiError(w, 404, "unknown extension")
		return
	}
	res, err := a.startCall(r.Context(), req.CallerUserID, calleeID)
	switch {
	case errors.Is(err, errInCall):
		apiError(w, 409, "already in a call")
	case err != nil:
		apiError(w, 500, "internal error")
	default:
		writeJSON(w, 200, map[string]any{"call_id": res.ID, "state": res.State, "outcome": res.Outcome, "callee_user_id": res.Callee})
	}
}

// internalAnswered: a desk phone picked up. The call becomes active and the callee's other devices stop ringing.
func (a *app) internalAnswered(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), chi.URLParam(r, "id")
	var caller, callee int64
	err := a.st.db.QueryRow(ctx, `UPDATE calls SET state='active', answered_at=now() WHERE id=$1::uuid AND state='ringing'
		RETURNING caller_id, callee_id`, id).Scan(&caller, &callee)
	if err != nil {
		apiError(w, 409, "no ringing call")
		return
	}
	a.enterCall(ctx, caller)
	a.enterCall(ctx, callee)
	recorded := a.maybeRecord(ctx, id, caller, callee)
	a.hub.publish(ctx, []int64{caller, callee}, "call_answered", map[string]any{"call_id": id, "via": "sip", "answered_by": strconv.FormatInt(callee, 10), "recorded": recorded})
	w.WriteHeader(http.StatusNoContent)
}

// internalHangup: a phone sent BYE, CANCEL or refused the call.
func (a *app) internalHangup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ByUserID int64  `json:"by_user_id"`
		As       string `json:"as"` // outcome if the call was still ringing: cancelled | rejected
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&req)
	if req.As != "rejected" {
		req.As = "cancelled"
	}
	if !a.finish(r.Context(), chi.URLParam(r, "id"), req.As, req.ByUserID, false) {
		apiError(w, 409, "call already ended")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
