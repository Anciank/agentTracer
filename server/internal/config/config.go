package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr     string
	DatabaseURL    string
	APIKey         string
	CORSOrigins    []string
	S3Endpoint     string
	S3Region       string
	S3Bucket       string
	S3AccessKey    string
	S3SecretKey    string
	S3UsePathStyle bool
	HotRetention   time.Duration
	ColdRetention  time.Duration
	// ColdEnabled gates the background archiver. Default false; at-m2 test override only.
	ColdEnabled bool
	// ColdArchiveInterval / Batch size for the ticker (defaults 1h / 50).
	ColdArchiveInterval time.Duration
	ColdArchiveBatch    int
}

func Load() (Config, error) {
	apiKey := strings.TrimSpace(os.Getenv("AGENTTRACER_API_KEY"))
	if apiKey == "" {
		if path := strings.TrimSpace(os.Getenv("AGENTTRACER_API_KEY_FILE")); path != "" {
			if b, err := os.ReadFile(path); err == nil {
				apiKey = strings.TrimSpace(string(b))
			}
		}
	}
	cfg := Config{
		ListenAddr:     env("AGENTTRACER_LISTEN", ":1984"),
		DatabaseURL:    env("AGENTTRACER_DATABASE_URL", "postgres://agenttracer:agenttracer@localhost:5432/agenttracer?sslmode=disable"),
		APIKey:         apiKey,
		CORSOrigins:    splitCSV(env("AGENTTRACER_CORS_ORIGINS", "*")),
		S3Endpoint:     env("AGENTTRACER_S3_ENDPOINT", "http://localhost:9000"),
		S3Region:       env("AGENTTRACER_S3_REGION", "us-east-1"),
		S3Bucket:       env("AGENTTRACER_S3_BUCKET", "agenttracer"),
		S3AccessKey:    env("AGENTTRACER_S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey:    env("AGENTTRACER_S3_SECRET_KEY", "minioadmin"),
		S3UsePathStyle: envBool("AGENTTRACER_S3_PATH_STYLE", true),
		HotRetention:        envDurationDays("AGENTTRACER_HOT_RETENTION_DAYS", 14),
		ColdRetention:       envDurationDays("AGENTTRACER_COLD_RETENTION_DAYS", 180),
		ColdEnabled:         envBool("AGENTTRACER_COLD_ENABLED", false),
		ColdArchiveInterval: envDuration("AGENTTRACER_COLD_ARCHIVE_INTERVAL", time.Hour),
		ColdArchiveBatch:    envInt("AGENTTRACER_COLD_ARCHIVE_BATCH", 50),
	}
	if cfg.APIKey == "" {
		return cfg, fmt.Errorf("AGENTTRACER_API_KEY is required")
	}
	return cfg, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envDurationDays(k string, defDays int) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return time.Duration(defDays) * 24 * time.Hour
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return time.Duration(defDays) * 24 * time.Hour
	}
	return time.Duration(n) * 24 * time.Hour
}


func envDuration(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func envInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
