// Package sipauth implements SIP digest authentication (RFC 3261 / RFC 7616) for the registrar.
// Both SHA-256 (RFC 8760) and MD5 are supported: modern phones use SHA-256, older ones only MD5.
// Nonces are stateless (timestamp + HMAC) so any sip instance can validate them.
package sipauth

import (
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // MD5 digest is still the lowest common denominator of SIP phones
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"time"
)

const nonceTTL = 2 * time.Minute

// Nonce returns "<unix ts>-<hmac>".
func Nonce(secret []byte, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	return ts + "-" + sign(secret, ts)
}

func sign(secret []byte, ts string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// NonceValid checks the signature and that the nonce is fresh.
func NonceValid(secret []byte, nonce string, now time.Time) bool {
	ts, mac, ok := strings.Cut(nonce, "-")
	if !ok {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(mac), []byte(sign(secret, ts))) != 1 {
		return false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	age := now.Sub(time.Unix(sec, 0))
	return age >= -5*time.Second && age < nonceTTL
}

// Challenges returns the WWW-Authenticate values to send (SHA-256 first, MD5 as fallback).
func Challenges(realm string, secret []byte, now time.Time, stale bool) []string {
	n := Nonce(secret, now)
	s := ""
	if stale {
		s = ", stale=true"
	}
	return []string{
		fmt.Sprintf(`Digest realm="%s", nonce="%s", algorithm=SHA-256, qop="auth"%s`, realm, n, s),
		fmt.Sprintf(`Digest realm="%s", nonce="%s", algorithm=MD5, qop="auth"%s`, realm, n, s),
	}
}

// Parse reads the parameters of an Authorization header value ("Digest k=v, k="v", ...").
func Parse(header string) (map[string]string, error) {
	h := strings.TrimSpace(header)
	if len(h) < 7 || !strings.EqualFold(h[:7], "digest ") {
		return nil, fmt.Errorf("not a digest header")
	}
	params := map[string]string{}
	rest := h[7:]
	for len(rest) > 0 {
		rest = strings.TrimLeft(rest, ", ")
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(rest[:eq]))
		rest = rest[eq+1:]
		var val string
		if strings.HasPrefix(rest, `"`) {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("unterminated quote")
			}
			val = rest[1 : 1+end]
			rest = rest[end+2:]
		} else {
			end := strings.IndexByte(rest, ',')
			if end < 0 {
				end = len(rest)
			}
			val = strings.TrimSpace(rest[:end])
			rest = rest[end:]
		}
		params[key] = val
	}
	return params, nil
}

func newHash(algorithm string) (func() hash.Hash, bool) {
	switch strings.ToUpper(algorithm) {
	case "", "MD5":
		return md5.New, true
	case "SHA-256":
		return sha256.New, true
	}
	return nil, false
}

func hexHash(h func() hash.Hash, s string) string {
	x := h()
	x.Write([]byte(s))
	return hex.EncodeToString(x.Sum(nil))
}

// Response computes the digest response a client must send (also used by test phones).
func Response(algorithm, user, realm, password, method, uri, nonce, nc, cnonce, qop string) string {
	h, ok := newHash(algorithm)
	if !ok {
		return ""
	}
	ha1 := hexHash(h, user+":"+realm+":"+password)
	ha2 := hexHash(h, method+":"+uri)
	if qop == "" {
		return hexHash(h, ha1+":"+nonce+":"+ha2)
	}
	return hexHash(h, ha1+":"+nonce+":"+nc+":"+cnonce+":"+qop+":"+ha2)
}

// Verify checks a parsed Authorization header against the known password.
func Verify(params map[string]string, method, password, realm string, secret []byte, now time.Time) bool {
	if params["realm"] != realm || !NonceValid(secret, params["nonce"], now) {
		return false
	}
	if _, ok := newHash(params["algorithm"]); !ok {
		return false
	}
	want := Response(params["algorithm"], params["username"], realm, password, method, params["uri"],
		params["nonce"], params["nc"], params["cnonce"], params["qop"])
	return want != "" && subtle.ConstantTimeCompare([]byte(want), []byte(params["response"])) == 1
}
