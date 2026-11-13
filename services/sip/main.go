// sip is the SIP registrar and call controller for the desk phones (RFC 3261): digest-authenticated
// registration, calls to and from PCs and other phones, forking, and media anchoring in the media service.
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"strconv"
	"sync"

	"github.com/irs269/eximphone/services/pkg/crypto"
	"github.com/irs269/eximphone/services/pkg/server"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type config struct {
	domain, publicIP string
	nonceSecret      []byte
	requireTLS       bool // refuse REGISTER and INVITE that do not arrive over TLS
	requireSRTP      bool // phones must use SRTP (SDES); plain RTP offers are refused
}

type service struct {
	cfg config
	st  *store
	api *apiClient
	log *slog.Logger

	srv       *sipgo.Server
	client    *sipgo.Client
	dialogSrv *sipgo.DialogServerCache
	dialogCli *sipgo.DialogClientCache

	mu       sync.Mutex
	sessions map[string]*session
	byCallID map[string]*session
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func must(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing required environment variable %s", k)
	}
	return v
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" || os.Getenv("DATABASE_URL") == "" {
		server.Run("sip") // healthcheck probe path
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "sip")
	ctx := context.Background()

	db, err := pgxpool.New(ctx, must("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	ropt, err := redis.ParseURL(must("REDIS_URL"))
	if err != nil {
		log.Fatal(err)
	}
	box, err := crypto.NewBox(must("SIP_KEY"))
	if err != nil {
		log.Fatalf("SIP_KEY: %v", err)
	}
	secret := must("JWT_SECRET")

	s := &service{
		cfg: config{domain: env("SIP_DOMAIN", "exim.local"), publicIP: env("PUBLIC_IP", "127.0.0.1"), nonceSecret: []byte(secret),
			requireTLS: env("SIP_REQUIRE_TLS", "") == "true", requireSRTP: env("SIP_REQUIRE_SRTP", "") == "true"},
		st:  &store{db: db, rdb: redis.NewClient(ropt), box: box},
		api: newAPIClient(env("API_URL", "http://api:8080"), env("MEDIA_URL", "http://media:8080"), must("INTERNAL_TOKEN")),
		log: logger, sessions: map[string]*session{}, byCallID: map[string]*session{},
	}

	ua, err := sipgo.NewUA(sipgo.WithUserAgent("EximPhone-SIP/0.5"), sipgo.WithUserAgentHostname(s.cfg.domain))
	if err != nil {
		log.Fatal(err)
	}
	if s.srv, err = sipgo.NewServer(ua); err != nil {
		log.Fatal(err)
	}
	if s.client, err = sipgo.NewClient(ua); err != nil {
		log.Fatal(err)
	}
	port, _ := strconv.Atoi(env("SIP_PORT", "5060"))
	contact := sip.ContactHeader{Address: sip.Uri{Scheme: "sip", User: "eximphone", Host: s.cfg.publicIP, Port: port}}
	s.dialogSrv = sipgo.NewDialogServerCache(s.client, contact)
	s.dialogCli = sipgo.NewDialogClientCache(s.client, contact)

	s.srv.OnRegister(s.onRegister)
	s.srv.OnInvite(s.onInvite)
	s.srv.OnAck(s.onAck)
	s.srv.OnBye(s.onBye)
	s.srv.OnCancel(s.onCancel)
	s.srv.OnOptions(s.onOptions)

	server.RunWith("sip", nil, func(ctx context.Context, l *slog.Logger) {
		go s.listen(ctx)
		for _, network := range []string{"udp", "tcp"} {
			network := network
			go func() {
				addr := "0.0.0.0:" + strconv.Itoa(port)
				l.Info("sip listening", "network", network, "addr", addr)
				if err := s.srv.ListenAndServe(ctx, network, addr); err != nil && ctx.Err() == nil {
					l.Error("sip listener stopped", "network", network, "err", err)
				}
			}()
		}
		if cert, key := env("SIP_TLS_CERT", ""), env("SIP_TLS_KEY", ""); cert != "" && key != "" {
			if conf, err := sipgo.GenerateTLSConfig(cert, key, nil); err == nil {
				go func() {
					l.Info("sips listening", "addr", "0.0.0.0:5061")
					if err := s.srv.ListenAndServeTLS(ctx, "tcp", "0.0.0.0:5061", conf); err != nil && ctx.Err() == nil {
						l.Error("sips listener stopped", "err", err)
					}
				}()
			} else {
				l.Warn("TLS certificate not usable, SIP over TLS disabled", "err", err)
			}
		}
		<-ctx.Done()
		_ = s.srv.Close()
	})
}
