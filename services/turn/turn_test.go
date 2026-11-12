package main

import (
	"net"
	"testing"
	"time"

	"github.com/irs269/eximphone/services/pkg/turncred"

	"github.com/pion/logging"
	"github.com/pion/turn/v4"
)

// allocate performs a real TURN allocation against an in-process server.
func allocate(t *testing.T, addr, user, pass string) error {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	c, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: addr, TURNServerAddr: addr, Conn: conn,
		Username: user, Password: pass, Realm: realm, LoggerFactory: logging.NewDefaultLoggerFactory(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Listen(); err != nil {
		t.Fatal(err)
	}
	relay, err := c.Allocate()
	if err != nil {
		return err
	}
	relay.Close()
	return nil
}

func TestTurnAllocationWithValidAndInvalidCredentials(t *testing.T) {
	const secret = "unit-test-secret"
	srv, err := newServer(secret, "127.0.0.1", "127.0.0.1:34780", 50000, 50050)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	user, pass := turncred.Generate(secret, 7, time.Now(), time.Minute)
	if err := allocate(t, "127.0.0.1:34780", user, pass); err != nil {
		t.Fatalf("valid credentials must get a relay allocation: %v", err)
	}
	if err := allocate(t, "127.0.0.1:34780", user, "wrong-password"); err == nil {
		t.Fatal("wrong password must be rejected")
	}
	expired, epass := turncred.Generate(secret, 7, time.Now().Add(-2*time.Hour), time.Hour)
	if err := allocate(t, "127.0.0.1:34780", expired, epass); err == nil {
		t.Fatal("expired credentials must be rejected")
	}
}
