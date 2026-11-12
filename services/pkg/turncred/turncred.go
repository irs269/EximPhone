// Package turncred implements time-limited TURN credentials (the "TURN REST API" scheme):
// username = "<expiry unix>:<user id>", password = base64(HMAC-SHA1(secret, username)).
// The api issues them, the turn service verifies them; no credential is ever stored.
package turncred

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // required by the TURN REST scheme, used as a MAC only
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Generate returns a username/password pair valid for ttl.
func Generate(secret string, userID int64, now time.Time, ttl time.Duration) (username, password string) {
	username = fmt.Sprintf("%d:%d", now.Add(ttl).Unix(), userID)
	return username, Password(secret, username)
}

func Password(secret, username string) string {
	m := hmac.New(sha1.New, []byte(secret))
	m.Write([]byte(username))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// Valid reports whether username has not expired. The caller derives the password with Password.
func Valid(username string, now time.Time) bool {
	exp, _, ok := strings.Cut(username, ":")
	if !ok {
		return false
	}
	ts, err := strconv.ParseInt(exp, 10, 64)
	return err == nil && now.Unix() < ts
}
