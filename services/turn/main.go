// turn is the in-house STUN/TURN server (pion/turn) used by PCs behind NAT.
// Credentials are time-limited and derived from TURN_SECRET (see pkg/turncred).
package main

import (
	"context"
	"log"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/irs269/eximphone/services/pkg/server"
	"github.com/irs269/eximphone/services/pkg/turncred"

	"github.com/pion/turn/v4"
)

const realm = "eximphone"

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newServer(secret, publicIP, listen string, minPort, maxPort uint16) (*turn.Server, error) {
	udp, err := net.ListenPacket("udp4", listen)
	if err != nil {
		return nil, err
	}
	tcp, err := net.Listen("tcp4", listen)
	if err != nil {
		udp.Close()
		return nil, err
	}
	gen := func() turn.RelayAddressGenerator {
		return &turn.RelayAddressGeneratorPortRange{
			RelayAddress: net.ParseIP(publicIP), Address: "0.0.0.0", MinPort: minPort, MaxPort: maxPort,
		}
	}
	return turn.NewServer(turn.ServerConfig{
		Realm: realm,
		AuthHandler: func(username, realm string, _ net.Addr) ([]byte, bool) {
			if !turncred.Valid(username, time.Now()) {
				return nil, false
			}
			return turn.GenerateAuthKey(username, realm, turncred.Password(secret, username)), true
		},
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: udp, RelayAddressGenerator: gen()}},
		ListenerConfigs:   []turn.ListenerConfig{{Listener: tcp, RelayAddressGenerator: gen()}},
	})
}

func main() {
	secret := os.Getenv("TURN_SECRET")
	if secret == "" || (len(os.Args) > 1 && os.Args[1] == "-healthcheck") {
		server.Run("turn") // healthcheck probe path
		return
	}
	minP, _ := strconv.Atoi(getenv("TURN_MIN_PORT", "49152"))
	maxP, _ := strconv.Atoi(getenv("TURN_MAX_PORT", "49200"))
	srv, err := newServer(secret, getenv("PUBLIC_IP", "127.0.0.1"), getenv("TURN_LISTEN", "0.0.0.0:3478"), uint16(minP), uint16(maxP))
	if err != nil {
		log.Fatal(err)
	}
	server.RunWith("turn", nil, func(ctx context.Context, l *slog.Logger) {
		l.Info("turn listening", "listen", getenv("TURN_LISTEN", "0.0.0.0:3478"))
		<-ctx.Done()
		_ = srv.Close()
	})
}
