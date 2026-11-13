package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/irs269/eximphone/services/pkg/attendance"
	"github.com/irs269/eximphone/services/pkg/crypto"
	"github.com/irs269/eximphone/services/pkg/directory"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/minio/minio-go/v7"
	"github.com/redis/go-redis/v9"
)

const (
	accessTTL     = 15 * time.Minute
	refreshTTL    = 12 * time.Hour
	loginMaxFails = 5
	loginWindow   = 15 * time.Minute
	refreshCookie = "refresh"
)

type ctxKey struct{}

type app struct {
	st        store
	rdb       *redis.Client
	ldap      directory.Client
	hub       *hub
	box       *crypto.Box
	sipBox    *crypto.Box
	recBox    *crypto.Box
	s3        *minio.Client
	jwtSecret []byte
	now       func() time.Time
}

func (a *app) routes(r chi.Router) {
	r.Post("/auth/login", a.login)
	r.Post("/auth/refresh", a.refresh)
	r.Get("/ws", a.serveWS)
	r.Route("/internal", a.internalRoutes)
	r.Group(func(r chi.Router) {
		r.Use(a.requireSession)
		a.chatRoutes(r)
		a.callRoutes(r)
		a.adminRoutes(r)
		a.rbacRoutes(r)
		a.recordingRoutes(r)
		r.Get("/me", a.me)
		r.Get("/me/attendance", a.history)
		r.Get("/auth/logout-summary", a.logoutSummary)
		r.Post("/auth/logout", a.logout)
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// clientIP trusts X-Real-IP, which only nginx (the sole entry point on the back network) sets.
func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *app) newAccessToken(u *user) (string, error) {
	now := a.now()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(u.ID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(accessTTL)),
	}).SignedString(a.jwtSecret)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return "rt:" + hex.EncodeToString(h[:])
}

func (a *app) issueRefresh(ctx context.Context, w http.ResponseWriter, userID int64) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	if err := a.rdb.Set(ctx, hashToken(tok), userID, refreshTTL).Err(); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: tok, Path: "/api/auth", MaxAge: int(refreshTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	return nil
}

func clearRefresh(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: refreshCookie, Value: "", Path: "/api/auth", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}

// ---- middleware ----

func (a *app) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		var claims jwt.RegisteredClaims
		tok, err := jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) { return a.jwtSecret, nil },
			jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !tok.Valid {
			apiError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		id, err := strconv.ParseInt(claims.Subject, 10, 64)
		if err != nil {
			apiError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ok, err := a.st.touch(r.Context(), id)
		if err != nil {
			apiError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok { // disabled account, or no open work day (already clocked out)
			apiError(w, http.StatusUnauthorized, "session closed")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func userID(r *http.Request) int64 { return r.Context().Value(ctxKey{}).(int64) }

// ---- handlers ----

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req loginReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) > 64 || len(req.Password) > 256 {
		apiError(w, http.StatusBadRequest, "invalid request")
		return
	}
	ip := clientIP(r)

	rlKey := fmt.Sprintf("rl:login:%s:%s", ip, strings.ToLower(req.Username))
	if n, _ := a.rdb.Get(ctx, rlKey).Int(); n >= loginMaxFails {
		apiError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}

	prof, err := a.ldap.Authenticate(req.Username, req.Password)
	if errors.Is(err, directory.ErrInvalidCredentials) {
		if a.rdb.Incr(ctx, rlKey).Val() == 1 {
			a.rdb.Expire(ctx, rlKey, loginWindow)
		}
		a.st.audit(ctx, nil, req.Username, "login_failed", ip, nil)
		apiError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err != nil {
		apiError(w, http.StatusServiceUnavailable, "directory unavailable")
		return
	}
	a.rdb.Del(ctx, rlKey)

	u, err := a.st.upsertUser(ctx, prof)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !u.Active {
		a.st.audit(ctx, &u.ID, u.Username, "login_denied_inactive", ip, nil)
		apiError(w, http.StatusForbidden, "account disabled")
		return
	}
	st, err := a.st.getSite(ctx, u.SiteID)
	if err != nil {
		apiError(w, http.StatusForbidden, "no work site assigned to this account")
		return
	}
	if !attendance.IPAllowed(ip, st.Ranges) {
		a.st.audit(ctx, &u.ID, u.Username, "login_denied_network", ip, nil)
		apiError(w, http.StatusForbidden, "clock-in is only possible from the Exim Bank internal network")
		return
	}

	now := a.now()
	d, created, err := a.st.clockIn(ctx, u.ID, now, attendance.WorkDate(now, st.TZ),
		st.Schedule.ArrivalStatus(now.In(st.TZ)))
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal error")
		return
	}
	access, err := a.newAccessToken(u)
	if err == nil {
		err = a.issueRefresh(ctx, w, u.ID)
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if a.rdb.HSetNX(ctx, "presence", strconv.FormatInt(u.ID, 10), "on").Val() {
		a.hub.publish(ctx, nil, "presence", map[string]any{"user_id": u.ID, "status": "on"})
	}
	a.st.audit(ctx, &u.ID, u.Username, "login", ip, map[string]any{"new_clock_in": created, "arrival": d.Arrival})
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "user": u, "attendance": d, "new_clock_in": created})
}

func (a *app) refresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(refreshCookie)
	if err != nil {
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	key := hashToken(c.Value)
	id, err := a.rdb.GetDel(r.Context(), key).Int64() // single use: rotate
	if err != nil {
		clearRefresh(w)
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	u, err := a.st.getUser(r.Context(), id)
	if err != nil || !u.Active {
		clearRefresh(w)
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if d, _ := a.st.openDay(r.Context(), id); d == nil {
		clearRefresh(w)
		apiError(w, http.StatusUnauthorized, "session closed")
		return
	}
	access, err := a.newAccessToken(u)
	if err == nil {
		err = a.issueRefresh(r.Context(), w, id)
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access})
}

func (a *app) me(w http.ResponseWriter, r *http.Request) {
	u, err := a.st.getUser(r.Context(), userID(r))
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal error")
		return
	}
	d, _ := a.st.openDay(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "attendance": d, "roles": a.rolesOf(r.Context(), u.ID)})
}

// logoutSummary feeds the confirmation screen: arrival, departure (now), duration.
func (a *app) logoutSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, _ := a.st.getUser(ctx, userID(r))
	st, err := a.st.getSite(ctx, u.SiteID)
	d, _ := a.st.openDay(ctx, u.ID)
	if err != nil || d == nil {
		apiError(w, http.StatusConflict, "no open work day")
		return
	}
	now := a.now()
	_, depErr := st.Schedule.DepartureStatus(now.In(st.TZ), "")
	writeJSON(w, http.StatusOK, map[string]any{
		"clock_in": d.ClockIn, "clock_out": now, "duration_min": attendance.Minutes(d.ClockIn, now),
		"early": errors.Is(depErr, attendance.ErrReasonRequired),
	})
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
	if len(req.Reason) > 500 {
		apiError(w, http.StatusBadRequest, "reason too long")
		return
	}
	u, _ := a.st.getUser(ctx, userID(r))
	st, err := a.st.getSite(ctx, u.SiteID)
	d, _ := a.st.openDay(ctx, u.ID)
	if err != nil || d == nil {
		apiError(w, http.StatusConflict, "no open work day")
		return
	}
	now := a.now()
	dep, err := st.Schedule.DepartureStatus(now.In(st.TZ), req.Reason)
	if errors.Is(err, attendance.ErrReasonRequired) {
		apiError(w, http.StatusUnprocessableEntity, "a reason is required for an early departure")
		return
	}
	mins := attendance.Minutes(d.ClockIn, now)
	if err := a.st.clockOut(ctx, d.ID, now, dep, strings.TrimSpace(req.Reason), mins); err != nil {
		apiError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if c, err := r.Cookie(refreshCookie); err == nil {
		a.rdb.Del(ctx, hashToken(c.Value))
	}
	clearRefresh(w)
	a.setPresence(ctx, u.ID, "offline")
	a.hub.publish(ctx, []int64{u.ID}, "kick", struct{}{}) // close every open socket of this user
	a.st.audit(ctx, &u.ID, u.Username, "logout", clientIP(r), map[string]any{"departure": dep, "duration_min": mins})
	writeJSON(w, http.StatusOK, map[string]any{"clock_in": d.ClockIn, "clock_out": now, "duration_min": mins, "departure": dep})
}

func (a *app) history(w http.ResponseWriter, r *http.Request) {
	days, err := a.st.history(r.Context(), userID(r), 31)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days})
}
