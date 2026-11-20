package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// sigClient is a participant that only speaks the signalling protocol (remote control needs no media).
type sigClient struct {
	t    *testing.T
	user int64
	ws   *websocket.Conn
	in   chan sigMsg
}

func joinSignalling(t *testing.T, srv *httptest.Server, meeting string, user int64) *sigClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, wsURL(srv, meeting), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &sigClient{t: t, user: user, ws: ws, in: make(chan sigMsg, 4096)}
	t.Cleanup(func() { _ = ws.CloseNow() })
	c.send(sigMsg{Type: "join", Token: meetToken(user, meeting)})
	go func() {
		for {
			_, data, err := ws.Read(context.Background())
			if err != nil {
				close(c.in)
				return
			}
			var m sigMsg
			if json.Unmarshal(data, &m) == nil {
				c.in <- m
			}
		}
	}()
	c.expect("joined", 0)
	return c
}

func (c *sigClient) send(m sigMsg) {
	b, _ := json.Marshal(m)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.ws.Write(ctx, websocket.MessageText, b)
}

// expect waits for a message of that type (and, when about != 0, concerning that user or owner).
func (c *sigClient) expect(typ string, about int64) sigMsg {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-c.in:
			if !ok {
				c.t.Fatalf("user %d: connection closed while waiting for %q", c.user, typ)
			}
			if m.Type == typ && (about == 0 || m.User == about || m.Owner == about) {
				return m
			}
		case <-deadline:
			c.t.Fatalf("user %d: no %q message", c.user, typ)
		}
	}
}

// none reports whether no message of that type arrives within d.
func (c *sigClient) none(typ string, d time.Duration) bool {
	deadline := time.After(d)
	for {
		select {
		case m, ok := <-c.in:
			if !ok {
				return true
			}
			if m.Type == typ {
				return false
			}
		case <-deadline:
			return true
		}
	}
}

// drain counts the rc_input messages received until things go quiet.
func (c *sigClient) drainInputs() (n int, last rcInputEvent) {
	for {
		select {
		case m, ok := <-c.in:
			if !ok {
				return
			}
			if m.Type == "rc_input" {
				n++
				_ = json.Unmarshal(m.Ev, &last)
			}
		case <-time.After(400 * time.Millisecond):
			return
		}
	}
}

func on() *bool { b := true; return &b }

func TestRemoteControl(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, body)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer api.Close()
	t.Setenv("API_URL", api.URL)

	srv, _ := newTestServer(t)
	defer srv.Close()
	const owner, ctl, other = 1, 2, 3
	a := joinSignalling(t, srv, "rc1", owner)
	b := joinSignalling(t, srv, "rc1", ctl)
	c := joinSignalling(t, srv, "rc1", other)

	// the owner is not sharing a controllable screen yet
	b.send(sigMsg{Type: "rc_request", User: owner})
	if d := b.expect("rc_denied", owner); d.Reason != "not_supported" {
		t.Fatalf("expected not_supported, got %q", d.Reason)
	}
	// somebody asking to control themselves, or an absent person
	b.send(sigMsg{Type: "rc_request", User: ctl})
	b.expect("rc_denied", ctl)
	b.send(sigMsg{Type: "rc_request", User: 99})
	b.expect("rc_denied", 99)

	// the owner shares a whole screen from the desktop app
	a.send(sigMsg{Type: "state", Screen: on(), RC: on()})
	if s := b.expect("state", owner); s.RC == nil || !*s.RC {
		t.Fatal("others must see that the screen can be controlled")
	}

	// request: only the owner is asked; a refusal ends it
	b.send(sigMsg{Type: "rc_request", User: owner})
	if r := a.expect("rc_request", 0); r.User != ctl {
		t.Fatalf("the owner was asked by %d", r.User)
	}
	if !c.none("rc_request", 200*time.Millisecond) {
		t.Fatal("a bystander must not see the request")
	}
	a.send(sigMsg{Type: "rc_deny", User: ctl})
	if d := b.expect("rc_denied", owner); d.Reason != "refused" {
		t.Fatalf("expected refused, got %q", d.Reason)
	}
	// input without any grant goes nowhere
	b.send(sigMsg{Type: "rc_input", Ev: json.RawMessage(`{"k":"m","x":0.5,"y":0.5}`)})
	if n, _ := a.drainInputs(); n != 0 {
		t.Fatalf("input was forwarded without a grant (%d)", n)
	}

	// a second request, accepted: everybody learns who controls what
	b.send(sigMsg{Type: "rc_request", User: owner})
	a.expect("rc_request", 0)
	a.send(sigMsg{Type: "rc_accept", User: ctl})
	for _, x := range []*sigClient{a, b, c} {
		if s := x.expect("rc_started", owner); s.User != ctl {
			t.Fatalf("started with controller %d", s.User)
		}
	}
	// someone else cannot take over, nor ask while the screen is controlled
	c.send(sigMsg{Type: "rc_request", User: owner})
	if d := c.expect("rc_denied", owner); d.Reason != "busy" {
		t.Fatalf("expected busy, got %q", d.Reason)
	}

	// input flows from the controller to the owner only, sanitised
	b.send(sigMsg{Type: "rc_input", Ev: json.RawMessage(`{"k":"m","x":5,"y":-2}`)})
	m := a.expect("rc_input", 0)
	var ev rcInputEvent
	_ = json.Unmarshal(m.Ev, &ev)
	if ev.K != "m" || ev.X != 1 || ev.Y != 0 {
		t.Fatalf("coordinates must be clamped to the screen: %+v", ev)
	}
	b.send(sigMsg{Type: "rc_input", Ev: json.RawMessage(`{"k":"kd","code":"KeyA","key":"a"}`)})
	a.expect("rc_input", 0)
	for _, bad := range []string{`{"k":"exec","code":"x"}`, `{"k":"d","b":9}`, `{"k":"kd"}`, `{"k":"kd","code":"0123456789012345678901234567890"}`, `not json`} {
		b.send(sigMsg{Type: "rc_input", Ev: json.RawMessage(bad)})
	}
	c.send(sigMsg{Type: "rc_input", Ev: json.RawMessage(`{"k":"m","x":0.1,"y":0.1}`)}) // not the controller
	if n, _ := a.drainInputs(); n != 0 {
		t.Fatalf("invalid or foreign input was forwarded (%d)", n)
	}
	if !c.none("rc_input", 100*time.Millisecond) || !b.none("rc_input", 100*time.Millisecond) {
		t.Fatal("input must only reach the owner")
	}

	// a flood is cut off by the rate limit
	for i := 0; i < 3000; i++ {
		b.send(sigMsg{Type: "rc_input", Ev: json.RawMessage(`{"k":"m","x":0.2,"y":0.2}`)})
	}
	if n, _ := a.drainInputs(); n == 0 || n >= 3000 {
		t.Fatalf("the rate limit did not apply: %d of 3000 events were forwarded", n)
	}

	// the owner stops: nothing is forwarded afterwards
	a.send(sigMsg{Type: "rc_stop"})
	for _, x := range []*sigClient{a, b, c} {
		if s := x.expect("rc_stopped", owner); s.Reason != "stopped" {
			t.Fatalf("stopped with %q", s.Reason)
		}
	}
	b.send(sigMsg{Type: "rc_input", Ev: json.RawMessage(`{"k":"m","x":0.3,"y":0.3}`)})
	if n, _ := a.drainInputs(); n != 0 {
		t.Fatalf("input still forwarded after the stop (%d)", n)
	}

	// the share stops: the session ends with it
	b.send(sigMsg{Type: "rc_request", User: owner})
	a.expect("rc_request", 0)
	a.send(sigMsg{Type: "rc_accept", User: ctl})
	b.expect("rc_started", owner)
	off := false
	a.send(sigMsg{Type: "state", Screen: &off})
	if s := b.expect("rc_stopped", owner); s.Reason != "share_stopped" {
		t.Fatalf("expected share_stopped, got %q", s.Reason)
	}
	a.send(sigMsg{Type: "state", Screen: on(), RC: on()})

	// the controller leaves: the owner is told and control ends
	b.send(sigMsg{Type: "rc_request", User: owner})
	a.expect("rc_request", 0)
	a.send(sigMsg{Type: "rc_accept", User: ctl})
	a.expect("rc_started", owner)
	b.send(sigMsg{Type: "leave"})
	if s := a.expect("rc_stopped", owner); s.Reason != "left" {
		t.Fatalf("expected left, got %q", s.Reason)
	}

	// every start and stop is reported to the api for the audit trail
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if n >= 6 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	started, stopped := 0, 0
	for _, c := range calls {
		if c["owner"] == nil {
			continue // peer_left notifications
		}
		switch c["action"] {
		case "started":
			started++
		case "stopped":
			stopped++
			if c["reason"] == nil {
				t.Fatal("a stop must carry its reason")
			}
		}
	}
	if started != 3 || stopped != 3 {
		t.Fatalf("audit notifications: %d started, %d stopped (want 3 and 3): %v", started, stopped, calls)
	}
}

func TestRemoteControlTimeouts(t *testing.T) {
	old1, old2 := rcRequestTimeout, rcMaxDuration
	rcRequestTimeout, rcMaxDuration = 300*time.Millisecond, 400*time.Millisecond
	defer func() { rcRequestTimeout, rcMaxDuration = old1, old2 }()

	srv, _ := newTestServer(t)
	defer srv.Close()
	a := joinSignalling(t, srv, "rc2", 1)
	b := joinSignalling(t, srv, "rc2", 2)
	a.send(sigMsg{Type: "state", Screen: on(), RC: on()})
	b.expect("state", 1)

	// nobody answers: the request expires on both sides
	b.send(sigMsg{Type: "rc_request", User: 1})
	a.expect("rc_request", 0)
	if d := b.expect("rc_denied", 1); d.Reason != "timeout" {
		t.Fatalf("expected timeout, got %q", d.Reason)
	}
	a.expect("rc_stopped", 1)

	// an accepted session expires by itself
	b.send(sigMsg{Type: "rc_request", User: 1})
	a.expect("rc_request", 0)
	a.send(sigMsg{Type: "rc_accept", User: 2})
	b.expect("rc_started", 1)
	if s := b.expect("rc_stopped", 1); s.Reason != "expired" {
		t.Fatalf("expected expired, got %q", s.Reason)
	}
}
