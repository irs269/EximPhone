// media relays call audio. Each participant opens a WebRTC leg to this service
// (offer/answer over HTTPS); RTP received on one leg is forwarded to the other.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

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
	server.RunWith("media", routes(m, []byte(secret), os.Getenv("INTERNAL_TOKEN")), func(ctx context.Context, l *slog.Logger) {
		<-ctx.Done()
	})
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
