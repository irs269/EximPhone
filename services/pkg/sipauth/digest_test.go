package sipauth

import (
	"strings"
	"testing"
	"time"
)

var secret = []byte("nonce-secret")

func header(alg, user, pass, method, uri, realm, nonce string) string {
	resp := Response(alg, user, realm, pass, method, uri, nonce, "00000001", "abc", "auth")
	return `Digest username="` + user + `", realm="` + realm + `", nonce="` + nonce + `", uri="` + uri +
		`", response="` + resp + `", algorithm=` + alg + `, qop=auth, nc=00000001, cnonce="abc"`
}

func TestVerifyBothAlgorithms(t *testing.T) {
	now := time.Now()
	nonce := Nonce(secret, now)
	for _, alg := range []string{"MD5", "SHA-256"} {
		h := header(alg, "2001", "pw", "REGISTER", "sip:exim.local", "exim.local", nonce)
		p, err := Parse(h)
		if err != nil {
			t.Fatal(err)
		}
		if !Verify(p, "REGISTER", "pw", "exim.local", secret, now) {
			t.Errorf("%s: valid response rejected", alg)
		}
		if Verify(p, "REGISTER", "wrong", "exim.local", secret, now) {
			t.Errorf("%s: wrong password accepted", alg)
		}
		if Verify(p, "INVITE", "pw", "exim.local", secret, now) {
			t.Errorf("%s: response for another method accepted", alg)
		}
	}
}

func TestKnownMD5Vector(t *testing.T) {
	// RFC 2617 section 3.5 example (qop=auth).
	got := Response("MD5", "Mufasa", "testrealm@host.com", "Circle Of Life", "GET", "/dir/index.html",
		"dcd98b7102dd2f0e8b11d0f600bfb0c093", "00000001", "0a4f113b", "auth")
	if got != "6629fae49393a05397450978507c4ef1" {
		t.Fatalf("got %s", got)
	}
}

func TestNonceExpiryAndTamper(t *testing.T) {
	now := time.Now()
	n := Nonce(secret, now)
	if !NonceValid(secret, n, now.Add(time.Minute)) {
		t.Error("fresh nonce rejected")
	}
	if NonceValid(secret, n, now.Add(3*time.Minute)) {
		t.Error("expired nonce accepted")
	}
	if NonceValid([]byte("other"), n, now) {
		t.Error("nonce signed with another secret accepted")
	}
	if NonceValid(secret, "1-abc", now) || NonceValid(secret, "garbage", now) {
		t.Error("forged nonce accepted")
	}
}

func TestChallengesOfferSHA256ThenMD5(t *testing.T) {
	c := Challenges("exim.local", secret, time.Now(), false)
	if len(c) != 2 || !strings.Contains(c[0], "SHA-256") || !strings.Contains(c[1], "MD5") {
		t.Fatalf("unexpected challenges %v", c)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse("Basic abc"); err == nil {
		t.Error("non digest header accepted")
	}
}
