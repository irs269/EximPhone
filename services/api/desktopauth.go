package main

// Signing in to the PC application from a session that is already open in the web browser (or the other way round),
// without typing the password again.
//
//	web page (signed in) --POST /auth/desktop-code--> one-time code, valid 60 s, bound to this user
//	eximconnect://signin?code=...  opens the PC application, which loads the site with ?desktop_code=...
//	the site --POST /auth/desktop-exchange--> access token + refresh cookie, exactly like a refresh
//
// The code is random (256 bits), stored hashed in Redis, usable once and only while the user's work day is open.
// It never carries a password or a long-lived token, and signing in this way is NOT a new clock-in.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"time"
)

const desktopCodeTTL = 60 * time.Second

// 32 random bytes in unpadded base64url
var desktopCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func desktopCodeKey(code string) string {
	h := sha256.Sum256([]byte(code))
	return "dc:" + hex.EncodeToString(h[:])
}

// POST /auth/desktop-code (needs a signed-in session): issues the one-time code.
func (a *app) desktopCode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := userID(r)
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		apiError(w, http.StatusInternalServerError, "internal error")
		return
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	if err := a.rdb.Set(ctx, desktopCodeKey(code), id, desktopCodeTTL).Err(); err != nil {
		apiError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if u, err := a.st.getUser(ctx, id); err == nil {
		a.st.audit(ctx, &u.ID, u.Username, "desktop_code_issued", clientIP(r), nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "expires_in": int(desktopCodeTTL.Seconds())})
}

// POST /auth/desktop-exchange {code}: turns the code into a session. Every failure looks the same (401).
func (a *app) desktopExchange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil || !desktopCodeRe.MatchString(req.Code) {
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := a.rdb.GetDel(ctx, desktopCodeKey(req.Code)).Int64() // single use
	if err != nil {
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	u, err := a.st.getUser(ctx, id)
	if err != nil || !u.Active {
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	d, _ := a.st.openDay(ctx, id)
	if d == nil { // clocked out since the code was issued: signing in again requires the password
		apiError(w, http.StatusUnauthorized, "session closed")
		return
	}
	access, err := a.newAccessToken(u)
	if err == nil {
		err = a.issueRefresh(ctx, w, id)
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "internal error")
		return
	}
	a.st.audit(ctx, &u.ID, u.Username, "desktop_signin", clientIP(r), nil)
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "user": u, "attendance": d, "new_clock_in": false})
}
