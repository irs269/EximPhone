package main

import (
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/irs269/eximphone/services/pkg/crypto"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// recorderFromEnv enables call recording when its key and the object storage are configured.
func recorderFromEnv(log *slog.Logger) *recorderEnv {
	key, endpoint := os.Getenv("RECORDING_KEY"), os.Getenv("S3_ENDPOINT")
	if key == "" || endpoint == "" {
		log.Info("call recording disabled (RECORDING_KEY or S3_ENDPOINT not set)")
		return nil
	}
	box, err := crypto.NewBox(key)
	if err != nil {
		log.Error("RECORDING_KEY invalid, recording disabled", "err", err.Error())
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil
	}
	s3, err := minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(os.Getenv("S3_ACCESS_KEY_ID"), os.Getenv("S3_SECRET_ACCESS_KEY"), ""),
		Secure:       u.Scheme == "https",
		Region:       "garage",
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil
	}
	api := os.Getenv("API_URL")
	if api == "" {
		api = "http://api:8080"
	}
	return &recorderEnv{box: box, s3: s3, bucket: "recordings", apiURL: api, token: os.Getenv("INTERNAL_TOKEN"),
		tmpDir: os.TempDir(), log: log, httpc: http.Client{Timeout: 10 * time.Second}}
}
