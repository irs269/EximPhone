package turncred

import (
	"testing"
	"time"
)

func TestGenerateAndValid(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	user, pass := Generate("s3cret", 42, now, time.Hour)
	if user != "1800003600:42" {
		t.Fatalf("username = %s", user)
	}
	if pass != Password("s3cret", user) {
		t.Fatal("password must be derivable from the secret")
	}
	if Password("other", user) == pass {
		t.Fatal("different secrets must give different passwords")
	}
	if !Valid(user, now.Add(59*time.Minute)) {
		t.Fatal("should still be valid")
	}
	if Valid(user, now.Add(time.Hour)) {
		t.Fatal("expired credentials must be rejected")
	}
}

func TestValidRejectsGarbage(t *testing.T) {
	for _, u := range []string{"", "abc", "abc:1", ":1"} {
		if Valid(u, time.Now()) {
			t.Errorf("%q must be invalid", u)
		}
	}
}
