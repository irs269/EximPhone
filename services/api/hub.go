package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
)

const eventsChannel = "eximphone:events"

// event travels through Redis pub/sub so every api instance can reach its local sockets.
type event struct {
	Users []int64         `json:"users,omitempty"` // empty = everyone
	Type  string          `json:"type"`
	Data  json.RawMessage `json:"data"`
}

type wsClient struct {
	userID int64
	send   chan []byte
	cancel context.CancelFunc
}

type hub struct {
	mu    sync.RWMutex
	conns map[int64]map[*wsClient]struct{}
	rdb   *redis.Client
	log   *slog.Logger
}

func newHub(rdb *redis.Client, log *slog.Logger) *hub {
	return &hub{conns: map[int64]map[*wsClient]struct{}{}, rdb: rdb, log: log}
}

// publish sends an event to the given users (nil = all connected users).
func (h *hub) publish(ctx context.Context, users []int64, typ string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	b, _ := json.Marshal(event{Users: users, Type: typ, Data: raw})
	if err := h.rdb.Publish(ctx, eventsChannel, b).Err(); err != nil {
		h.log.Error("publish failed", "err", err)
	}
}

// run delivers Redis events to local sockets until ctx ends.
func (h *hub) run(ctx context.Context) {
	sub := h.rdb.Subscribe(ctx, eventsChannel)
	defer sub.Close()
	for msg := range sub.Channel() {
		var ev event
		if json.Unmarshal([]byte(msg.Payload), &ev) == nil {
			h.deliver(ev)
		}
	}
}

func (h *hub) deliver(ev event) {
	out, _ := json.Marshal(map[string]any{"type": ev.Type, "data": ev.Data})
	h.mu.RLock()
	defer h.mu.RUnlock()
	send := func(set map[*wsClient]struct{}) {
		for c := range set {
			if ev.Type == "kick" {
				c.cancel()
				continue
			}
			select {
			case c.send <- out:
			default: // slow consumer: drop it, the client reconnects and resyncs
				c.cancel()
			}
		}
	}
	if len(ev.Users) == 0 {
		for _, set := range h.conns {
			send(set)
		}
		return
	}
	for _, id := range ev.Users {
		send(h.conns[id])
	}
}

func (h *hub) add(c *wsClient) {
	h.mu.Lock()
	if h.conns[c.userID] == nil {
		h.conns[c.userID] = map[*wsClient]struct{}{}
	}
	h.conns[c.userID][c] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) remove(c *wsClient) {
	h.mu.Lock()
	delete(h.conns[c.userID], c)
	h.mu.Unlock()
}

// serveWS authenticates with a first {"type":"auth","token":...} frame (tokens never go in URLs).
func (a *app) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4096)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	authCtx, authCancel := context.WithTimeout(ctx, 5*time.Second)
	_, data, err := conn.Read(authCtx)
	authCancel()
	var first struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if err != nil || json.Unmarshal(data, &first) != nil || first.Type != "auth" {
		conn.Close(websocket.StatusPolicyViolation, "auth required")
		return
	}
	var claims jwt.RegisteredClaims
	tok, err := jwt.ParseWithClaims(first.Token, &claims, func(*jwt.Token) (any, error) { return a.jwtSecret, nil },
		jwt.WithValidMethods([]string{"HS256"}))
	uid, perr := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || !tok.Valid || perr != nil {
		conn.Close(websocket.StatusPolicyViolation, "unauthorized")
		return
	}
	if ok, err := a.st.touch(ctx, uid); err != nil || !ok {
		conn.Close(websocket.StatusPolicyViolation, "session closed")
		return
	}

	c := &wsClient{userID: uid, send: make(chan []byte, 64), cancel: cancel}
	a.hub.add(c)
	defer a.hub.remove(c)

	// writer
	go func() {
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-c.send:
				wctx, wc := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Write(wctx, websocket.MessageText, m)
				wc()
				if err != nil {
					cancel()
					return
				}
			case <-ping.C:
				pctx, pc := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Ping(pctx)
				pc()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	c.send <- []byte(`{"type":"ready"}`)

	// reader
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var in struct {
			Type           string `json:"type"`
			ConversationID int64  `json:"conversation_id"`
			MessageID      int64  `json:"message_id"`
		}
		if json.Unmarshal(data, &in) != nil {
			continue
		}
		switch in.Type {
		case "typing":
			if members, err := a.members(ctx, in.ConversationID, uid); err == nil {
				a.hub.publish(ctx, members, "typing", map[string]int64{"conversation_id": in.ConversationID, "user_id": uid})
			}
		case "delivered":
			a.markDelivered(ctx, uid, in.MessageID)
		}
	}
}
