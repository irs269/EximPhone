// Package server provides the minimal HTTP skeleton shared by every EximPhone
// Go service: structured JSON logs, /healthz, /metrics and graceful shutdown.
package server

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter returns the base router exposing /healthz and /metrics.
func NewRouter(name string) chi.Router {
	started := time.Now()
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ok","service":%q}`, name)
	})

	// Prometheus text exposition format (no external dependency).
	r.Get("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(w, "# TYPE eximphone_up gauge\neximphone_up{service=%q} 1\n", name)
		_, _ = fmt.Fprintf(w, "# TYPE eximphone_uptime_seconds gauge\neximphone_uptime_seconds{service=%q} %.0f\n",
			name, time.Since(started).Seconds())
	})
	return r
}

// Run starts the service. With -healthcheck it probes the local /healthz and
// exits (used by Docker HEALTHCHECK, since distroless images ship no shell).
func Run(name string) {
	healthcheck := flag.Bool("healthcheck", false, "probe /healthz and exit")
	flag.Parse()

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	if *healthcheck {
		os.Exit(probe(addr))
	}

	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With("service", name)

	srv := &http.Server{
		Addr:              addr,
		Handler:           NewRouter(name),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Info("stopped")
}

func probe(addr string) int {
	port := addr
	if i := len(addr) - 1; i >= 0 {
		for j := i; j >= 0; j-- {
			if addr[j] == ':' {
				port = addr[j+1:]
				break
			}
		}
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
