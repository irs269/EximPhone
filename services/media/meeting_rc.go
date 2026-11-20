package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Remote control of a shared screen, inside a meeting.
//
// The person who shares their screen (the owner) must run the desktop app and share a whole screen; they announce this with
// state.rc. Another participant (the controller) asks for control; the owner answers on a native dialog of the desktop app.
// Only while a grant is active does this service forward the controller's mouse and keyboard events to the owner, and
// only from that controller. A grant ends when either person stops it, leaves, or when the share stops; it also expires.
// The service never injects anything itself: it routes small, validated, rate-limited events.

// Variables (not constants) so that tests can shorten the delays.
var (
	rcRequestTimeout = 30 * time.Second
	rcMaxDuration    = 60 * time.Minute
)

const (
	rcEventsPerSec = 400
	rcBurst        = 800
)

type rcPending struct {
	controller int64
	timer      *time.Timer
}

type rcGrant struct {
	owner, controller int64
	since             time.Time
	expiry            *time.Timer
	mu                sync.Mutex
	tokens            float64
	last              time.Time
}

// rcInputEvent is what the controller may send; everything is clamped before it is forwarded.
type rcInputEvent struct {
	K    string  `json:"k"`           // m move, d button down, u button up, w wheel, kd key down, ku key up
	X    float64 `json:"x,omitempty"` // 0..1 across the shared screen
	Y    float64 `json:"y,omitempty"`
	B    int     `json:"b,omitempty"` // 0 left, 1 middle, 2 right
	Dx   float64 `json:"dx,omitempty"`
	Dy   float64 `json:"dy,omitempty"`
	Code string  `json:"code,omitempty"` // KeyboardEvent.code
	Key  string  `json:"key,omitempty"`  // KeyboardEvent.key
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// sanitize validates one input event; ok is false for anything unexpected.
func (e rcInputEvent) sanitize() (rcInputEvent, bool) {
	switch e.K {
	case "m", "d", "u", "w", "kd", "ku":
	default:
		return e, false
	}
	e.X, e.Y = clamp(e.X, 0, 1), clamp(e.Y, 0, 1)
	e.Dx, e.Dy = clamp(e.Dx, -2000, 2000), clamp(e.Dy, -2000, 2000)
	if e.B < 0 || e.B > 2 {
		return e, false
	}
	if len(e.Code) > 24 || len(e.Key) > 24 || strings.ContainsAny(e.Code+e.Key, "\x00") {
		return e, false
	}
	if (e.K == "kd" || e.K == "ku") && e.Code == "" {
		return e, false
	}
	return e, true
}

func (r *mRoom) rcInit() {
	if r.rcPend == nil {
		r.rcPend = map[int64]*rcPending{}
		r.rcActive = map[int64]*rcGrant{}
	}
}

func (r *mRoom) participant(id int64) *participant {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.parts[id]
}

func (r *mRoom) rcDenied(to *participant, owner int64, reason string) {
	if to != nil {
		to.send(sigMsg{Type: "rc_denied", User: owner, Reason: reason})
	}
}

// rcRequest: `controller` asks to control the screen of `owner`.
func (r *mRoom) rcRequest(controller, owner int64) {
	c, o := r.participant(controller), r.participant(owner)
	if c == nil {
		return
	}
	if o == nil || controller == owner {
		r.rcDenied(c, owner, "unavailable")
		return
	}
	o.mu.Lock()
	capable := o.rc && o.screen
	o.mu.Unlock()
	if !capable {
		r.rcDenied(c, owner, "not_supported") // not the desktop app, or not sharing a whole screen
		return
	}
	r.rcMu.Lock()
	r.rcInit()
	busy := r.rcActive[owner] != nil || r.rcPend[owner] != nil
	for _, g := range r.rcActive {
		if g.controller == controller || g.owner == controller || owner == g.controller {
			busy = true // one session at a time per person, and nobody is both controller and controlled
		}
	}
	for ow, p := range r.rcPend {
		if p.controller == controller || ow == controller {
			busy = true
		}
	}
	if busy {
		r.rcMu.Unlock()
		r.rcDenied(c, owner, "busy")
		return
	}
	pend := &rcPending{controller: controller}
	pend.timer = time.AfterFunc(rcRequestTimeout, func() {
		r.rcMu.Lock()
		cur := r.rcPend[owner]
		if cur == pend {
			delete(r.rcPend, owner)
		}
		r.rcMu.Unlock()
		if cur == pend {
			r.rcDenied(r.participant(controller), owner, "timeout")
			if ow := r.participant(owner); ow != nil {
				ow.send(sigMsg{Type: "rc_stopped", Owner: owner, User: controller, Reason: "timeout"})
			}
		}
	})
	r.rcPend[owner] = pend
	r.rcMu.Unlock()
	o.send(sigMsg{Type: "rc_request", User: controller})
}

// rcAnswer: the owner accepts or refuses the pending request of `controller`.
func (r *mRoom) rcAnswer(owner, controller int64, accept bool) {
	r.rcMu.Lock()
	r.rcInit()
	pend := r.rcPend[owner]
	if pend == nil || pend.controller != controller {
		r.rcMu.Unlock()
		return
	}
	pend.timer.Stop()
	delete(r.rcPend, owner)
	if !accept {
		r.rcMu.Unlock()
		r.rcDenied(r.participant(controller), owner, "refused")
		return
	}
	o := r.participant(owner)
	if o == nil {
		r.rcMu.Unlock()
		return
	}
	o.mu.Lock()
	capable := o.rc && o.screen
	o.mu.Unlock()
	if !capable {
		r.rcMu.Unlock()
		r.rcDenied(r.participant(controller), owner, "not_supported")
		return
	}
	g := &rcGrant{owner: owner, controller: controller, since: time.Now(), tokens: rcBurst, last: time.Now()}
	g.expiry = time.AfterFunc(rcMaxDuration, func() { r.rcEnd(owner, "expired") })
	r.rcActive[owner] = g
	r.rcMu.Unlock()
	r.broadcast(0, sigMsg{Type: "rc_started", Owner: owner, User: controller})
	r.mm.m.postAPI("/internal/meetings/"+r.id+"/rc", map[string]any{"owner": owner, "controller": controller, "action": "started"})
}

// rcEnd ends the grant (or pending request) on `owner`'s screen.
func (r *mRoom) rcEnd(owner int64, reason string) {
	r.rcMu.Lock()
	r.rcInit()
	if p := r.rcPend[owner]; p != nil {
		p.timer.Stop()
		delete(r.rcPend, owner)
		r.rcMu.Unlock()
		if c := r.participant(p.controller); c != nil {
			c.send(sigMsg{Type: "rc_denied", User: owner, Reason: reason})
		}
		if o := r.participant(owner); o != nil {
			o.send(sigMsg{Type: "rc_stopped", Owner: owner, User: p.controller, Reason: reason})
		}
		return
	}
	g := r.rcActive[owner]
	if g != nil {
		g.expiry.Stop()
		delete(r.rcActive, owner)
	}
	r.rcMu.Unlock()
	if g == nil {
		return
	}
	r.broadcast(0, sigMsg{Type: "rc_stopped", Owner: owner, User: g.controller, Reason: reason})
	r.mm.m.postAPI("/internal/meetings/"+r.id+"/rc", map[string]any{
		"owner": owner, "controller": g.controller, "action": "stopped", "reason": reason, "seconds": int(time.Since(g.since).Seconds()),
	})
}

// rcEndInvolving ends everything in which `user` is the owner or the controller (leaving, stopping the share, ...).
func (r *mRoom) rcEndInvolving(user int64, reason string) {
	r.rcMu.Lock()
	r.rcInit()
	var owners []int64
	for ow, g := range r.rcActive {
		if ow == user || g.controller == user {
			owners = append(owners, ow)
		}
	}
	for ow, p := range r.rcPend {
		if ow == user || p.controller == user {
			owners = append(owners, ow)
		}
	}
	r.rcMu.Unlock()
	for _, ow := range owners {
		r.rcEnd(ow, reason)
	}
}

// rcInput forwards one event from the controller to the owner, only during an active grant and within the rate limit.
func (r *mRoom) rcInput(from int64, raw json.RawMessage) {
	var ev rcInputEvent
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	ev, ok := ev.sanitize()
	if !ok {
		return
	}
	r.rcMu.Lock()
	var grant *rcGrant
	for _, g := range r.rcActive {
		if g.controller == from {
			grant = g
		}
	}
	r.rcMu.Unlock()
	if grant == nil {
		return
	}
	grant.mu.Lock()
	now := time.Now()
	grant.tokens += now.Sub(grant.last).Seconds() * rcEventsPerSec
	if grant.tokens > rcBurst {
		grant.tokens = rcBurst
	}
	grant.last = now
	allowed := grant.tokens >= 1
	if allowed {
		grant.tokens--
	}
	grant.mu.Unlock()
	if !allowed {
		return
	}
	if o := r.participant(grant.owner); o != nil {
		b, _ := json.Marshal(ev)
		o.send(sigMsg{Type: "rc_input", User: from, Ev: b})
	}
}

// postAPI sends an internal notification to the api (best effort).
func (m *manager) postAPI(path string, body any) {
	base := apiBase()
	if base == "" {
		return
	}
	b, _ := json.Marshal(body)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(b))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", m.internalToken)
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
		}
	}()
}
