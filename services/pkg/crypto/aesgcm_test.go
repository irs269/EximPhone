package crypto

import (
	"bytes"
	"strings"
	"testing"
)

const testKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestRoundTrip(t *testing.T) {
	b, err := NewBox(testKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := b.Seal([]byte("bonjour"), []byte("7"))
	if bytes.Contains(sealed, []byte("bonjour")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	got, err := b.Open(sealed, []byte("7"))
	if err != nil || string(got) != "bonjour" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestWrongContextRejected(t *testing.T) {
	b, _ := NewBox(testKey)
	sealed, _ := b.Seal([]byte("secret"), []byte("conv-1"))
	if _, err := b.Open(sealed, []byte("conv-2")); err == nil {
		t.Fatal("message moved to another conversation must fail to decrypt")
	}
}

func TestNoncesDiffer(t *testing.T) {
	b, _ := NewBox(testKey)
	a, _ := b.Seal([]byte("x"), nil)
	c, _ := b.Seal([]byte("x"), nil)
	if bytes.Equal(a, c) {
		t.Fatal("nonce reuse")
	}
}

func TestBadKey(t *testing.T) {
	for _, k := range []string{"", "zz", strings.Repeat("a", 62)} {
		if _, err := NewBox(k); err == nil {
			t.Errorf("key %q should be rejected", k)
		}
	}
}
