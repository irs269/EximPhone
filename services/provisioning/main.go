// provisioning serves auto-configuration files to desk phones (Yealink, Grandstream, Fanvil) by MAC address.
// Files carry the SIP password, so they must only be served over HTTPS and only to the voice VLAN.
package main

import (
	"context"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/irs269/eximphone/services/pkg/crypto"
	"github.com/irs269/eximphone/services/pkg/server"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	yealinkFile     = regexp.MustCompile(`^([0-9a-f]{12})\.cfg$`)
	grandstreamFile = regexp.MustCompile(`^cfg([0-9a-f]{12})\.xml$`)
)

// lookup finds the provisioning data of a device (nil if the MAC is unknown or the device is disabled).
type lookup func(ctx context.Context, mac string) (*phone, error)

// limiter is a small per-IP fixed-window limiter: provisioning is a secret-bearing endpoint.
type limiter struct {
	mu    sync.Mutex
	count map[string]int
	reset time.Time
	max   int
}

func (l *limiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Now().After(l.reset) {
		l.count, l.reset = map[string]int{}, time.Now().Add(time.Minute)
	}
	l.count[ip]++
	return l.count[ip] <= l.max
}

func handler(find lookup, log *slog.Logger) http.HandlerFunc {
	lim := &limiter{count: map[string]int{}, reset: time.Now().Add(time.Minute), max: 30}
	return func(w http.ResponseWriter, r *http.Request) {
		ip := r.Header.Get("X-Real-IP")
		if ip == "" {
			ip, _, _ = net.SplitHostPort(r.RemoteAddr)
		}
		if !lim.allow(ip) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		name := chi.URLParam(r, "file")
		mac := ""
		if m := yealinkFile.FindStringSubmatch(name); m != nil {
			mac = m[1]
		} else if m := grandstreamFile.FindStringSubmatch(name); m != nil {
			mac = m[1]
		}
		if mac == "" {
			http.NotFound(w, r) // includes the generic boot files phones ask for first
			return
		}
		p, err := find(r.Context(), mac)
		if err != nil || p == nil {
			log.Info("provisioning request for unknown device", "mac", mac, "ip", ip)
			http.NotFound(w, r)
			return
		}
		body, err := render(*p)
		if err != nil {
			http.Error(w, "unsupported device", http.StatusInternalServerError)
			return
		}
		log.Info("provisioned", "mac", mac, "ext", p.Extension, "vendor", p.Vendor, "ip", ip)
		w.Header().Set("Content-Type", contentType(p.Vendor))
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(body))
	}
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || (len(os.Args) > 1 && os.Args[1] == "-healthcheck") {
		server.Run("provisioning") // healthcheck probe path
		return
	}
	box, err := crypto.NewBox(os.Getenv("SIP_KEY"))
	if err != nil {
		log.Fatalf("SIP_KEY: %v", err)
	}
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatal(err)
	}
	server_ := os.Getenv("PUBLIC_IP")
	port, _ := strconv.Atoi(os.Getenv("SIP_PORT"))
	if port == 0 {
		port = 5060
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "provisioning")

	find := func(ctx context.Context, mac string) (*phone, error) {
		var p phone
		var sealed []byte
		err := db.QueryRow(ctx, `SELECT d.vendor, COALESCE(u.extension,''), u.display_name, d.sip_username, d.sip_secret_enc
			FROM devices d JOIN users u ON u.id=d.user_id WHERE d.mac=$1 AND d.active AND u.active AND d.type='desk_phone'`, mac).
			Scan(&p.Vendor, &p.Extension, &p.DisplayName, &p.SIPUser, &sealed)
		if err != nil {
			return nil, nil //nolint:nilerr // unknown device: handled as 404
		}
		plain, err := box.Open(sealed, []byte(p.SIPUser))
		if err != nil {
			return nil, err
		}
		p.Password, p.Server, p.Port = string(plain), server_, port
		if os.Getenv("SIP_REQUIRE_TLS") == "true" {
			p.TLS, p.Port = true, 5061
		}
		p.SRTP = os.Getenv("SIP_REQUIRE_SRTP") == "true"
		return &p, nil
	}

	server.RunWith("provisioning", func(r chi.Router) { r.Get("/{file}", handler(find, logger)) }, nil)
}
