package main

import (
	"context"
	"log"
	"log/slog"
	"net/url"
	"os"
	"time"

	_ "time/tzdata" // site timezones must work on distroless

	"github.com/irs269/eximphone/services/pkg/crypto"
	"github.com/irs269/eximphone/services/pkg/directory"
	"github.com/irs269/eximphone/services/pkg/server"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
)

func env(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing required environment variable %s", k)
	}
	return v
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		server.Run("api") // handles the probe and exits
		return
	}
	ctx := context.Background()

	db, err := pgxpool.New(ctx, env("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	ropt, err := redis.ParseURL(env("REDIS_URL"))
	if err != nil {
		log.Fatal(err)
	}
	secret := env("JWT_SECRET")
	if len(secret) < 32 {
		log.Fatal("JWT_SECRET must be at least 32 characters")
	}

	rdb := redis.NewClient(ropt)
	box, err := crypto.NewBox(env("MESSAGE_KEY"))
	if err != nil {
		log.Fatalf("MESSAGE_KEY: %v", err)
	}
	sipBox, err := crypto.NewBox(env("SIP_KEY"))
	if err != nil {
		log.Fatalf("SIP_KEY: %v", err)
	}
	recBox, err := crypto.NewBox(env("RECORDING_KEY"))
	if err != nil {
		log.Fatalf("RECORDING_KEY: %v", err)
	}
	s3u, err := url.Parse(env("S3_ENDPOINT"))
	if err != nil {
		log.Fatal(err)
	}
	s3, err := minio.New(s3u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(env("S3_ACCESS_KEY_ID"), env("S3_SECRET_ACCESS_KEY"), ""),
		Secure:       s3u.Scheme == "https",
		Region:       "garage",
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		log.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "api")

	a := &app{
		st:        store{db: db},
		rdb:       rdb,
		hub:       newHub(rdb, logger),
		box:       box,
		sipBox:    sipBox,
		recBox:    recBox,
		s3:        s3,
		jwtSecret: []byte(secret),
		now:       func() time.Time { return time.Now().UTC() },
		ldap: directory.Client{
			URL: env("LDAP_URL"), BaseDN: env("LDAP_BASE_DN"), UserAttr: env("LDAP_USER_ATTR"),
			BindDN: env("LDAP_BIND_DN"), BindPassword: env("LDAP_BIND_PASSWORD"),
		},
	}
	server.RunWith("api", a.routes, func(ctx context.Context, _ *slog.Logger) { a.hub.run(ctx) })
}
