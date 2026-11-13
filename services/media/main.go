// media relays call audio. Each participant opens a WebRTC leg to this service
// (offer/answer over HTTPS); RTP received on one leg is forwarded to the other.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/irs269/eximphone/services/pkg/server"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
)

type mediaClaims struct {
	CallID string `json:"cid"`
	jwt.RegisteredClaims
}

func routes(m *manager, jwtSecret []byte, internalToken string) func(chi.Router) {
	return func(r chi.Router) {
		// Called by the browser. The short-lived token is issued by the api for one user and one call.
		r.Post("/calls/{id}/offer", func(w http.ResponseWriter, req *http.Request) {
			callID := chi.URLParam(req, "id")
			var claims mediaClaims
			tok, err := jwt.ParseWithClaims(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "), &claims,
				func(*jwt.Token) (any, error) { return jwtSecret, nil },
				jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("media"))
			if err != nil || !tok.Valid {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			user, err := strconv.ParseInt(claims.Subject, 10, 64)
			if err != nil || claims.CallID != callID {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			var body struct {
				SDP string `json:"sdp"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10)).Decode(&body) != nil || body.SDP == "" {
				http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
				return
			}
			answer, err := m.join(req.Context(), callID, user, body.SDP)
			if errors.Is(err, errRoomFull) {
				http.Error(w, `{"error":"room full"}`, http.StatusConflict)
				return
			}
			if err != nil {
				m.log.Warn("join failed", "err", err)
				http.Error(w, `{"error":"negotiation failed"}`, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"sdp": answer})
		})

		// --- SIP legs: used by the sip service for desk phones (internal token only) ---
		internal := func(w http.ResponseWriter, req *http.Request) bool {
			if subtle.ConstantTimeCompare([]byte(req.Header.Get("X-Internal-Token")), []byte(internalToken)) != 1 {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return false
			}
			return true
		}
		r.Post("/legs", func(w http.ResponseWriter, req *http.Request) {
			if !internal(w, req) {
				return
			}
			var body struct {
				CallID string `json:"call_id"`
				UserID int64  `json:"user_id"`
				SRTP   bool   `json:"srtp"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024)).Decode(&body) != nil || body.CallID == "" {
				http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
				return
			}
			l, err := m.newSIPLeg(body.CallID, body.UserID, body.SRTP)
			if err != nil {
				http.Error(w, `{"error":"no free port"}`, http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			out := map[string]any{"leg_id": l.id, "port": l.port}
			if l.secure {
				out["srtp_key"] = base64.StdEncoding.EncodeToString(l.srtpKey)
			}
			_ = json.NewEncoder(w).Encode(out)
		})
		r.Post("/legs/{id}/activate", func(w http.ResponseWriter, req *http.Request) {
			if !internal(w, req) {
				return
			}
			l := m.sipLeg(chi.URLParam(req, "id"))
			var body struct {
				Remote  string `json:"remote"`
				SRTPKey string `json:"srtp_key"`
			}
			if l == nil || json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024)).Decode(&body) != nil {
				http.Error(w, `{"error":"unknown leg"}`, http.StatusNotFound)
				return
			}
			addr, err := net.ResolveUDPAddr("udp4", body.Remote)
			if err != nil {
				http.Error(w, `{"error":"invalid remote"}`, http.StatusBadRequest)
				return
			}
			remoteKey, _ := base64.StdEncoding.DecodeString(body.SRTPKey)
			if err := l.activate(addr, remoteKey); errors.Is(err, errRoomFull) {
				http.Error(w, `{"error":"room full"}`, http.StatusConflict)
				return
			} else if errors.Is(err, errBadKey) {
				http.Error(w, `{"error":"srtp key required"}`, http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
		r.Delete("/legs/{id}", func(w http.ResponseWriter, req *http.Request) {
			if !internal(w, req) {
				return
			}
			if l := m.sipLeg(chi.URLParam(req, "id")); l != nil {
				l.close()
			}
			w.WriteHeader(http.StatusNoContent)
		})

		// The api marks a call for recording (department rule), identifying who is the caller.
		r.Post("/calls/{id}/record", func(w http.ResponseWriter, req *http.Request) {
			if !internal(w, req) {
				return
			}
			var cfg recordingConfig
			if json.NewDecoder(http.MaxBytesReader(w, req.Body, 512)).Decode(&cfg) != nil || cfg.CallerUserID == 0 {
				http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
				return
			}
			m.setRecording(chi.URLParam(req, "id"), cfg)
			w.WriteHeader(http.StatusNoContent)
		})

		// Called by the api when a call ends.
		r.Delete("/calls/{id}", func(w http.ResponseWriter, req *http.Request) {
			if subtle.ConstantTimeCompare([]byte(req.Header.Get("X-Internal-Token")), []byte(internalToken)) != 1 {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			m.closeRoom(chi.URLParam(req, "id"))
			w.WriteHeader(http.StatusNoContent)
		})
	}
}

func main() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" || (len(os.Args) > 1 && os.Args[1] == "-healthcheck") {
		server.Run("media") // healthcheck probe path
		return
	}
	minP, _ := strconv.Atoi(envOr("MEDIA_MIN_PORT", "10000"))
	maxP, _ := strconv.Atoi(envOr("MEDIA_MAX_PORT", "10200"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "media")
	m, err := newManager(os.Getenv("MEDIA_PUBLIC_IP"), uint16(minP), uint16(maxP), logger)
	if err != nil {
		log.Fatal(err)
	}
	m.recEnv = recorderFromEnv(logger)
	server.RunWith("media", routes(m, []byte(secret), os.Getenv("INTERNAL_TOKEN")), func(ctx context.Context, l *slog.Logger) {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.gc(2 * time.Minute)
			}
		}
	})
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
