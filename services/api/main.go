package main

import (
	"context"
	"log"
	"os"
	"time"

	_ "time/tzdata" // site timezones must work on distroless

	"github.com/irs269/eximphone/services/pkg/server"

	"github.com/jackc/pgx/v5/pgxpool"
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

	a := &app{
		st:        store{db: db},
		rdb:       redis.NewClient(ropt),
		jwtSecret: []byte(secret),
		now:       func() time.Time { return time.Now().UTC() },
		ldap: ldapClient{
			url: env("LDAP_URL"), baseDN: env("LDAP_BASE_DN"), userAttr: env("LDAP_USER_ATTR"),
			bindDN: env("LDAP_BIND_DN"), bindPassword: env("LDAP_BIND_PASSWORD"),
		},
	}
	server.RunWith("api", a.routes, nil)
}
