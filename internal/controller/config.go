package controller

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	ListenAddr       string
	DataDir          string
	DBPath           string
	NodeBinaryPath   string
	NodeBinaryDir    string
	LogLevel         string
	SessionLifetime  time.Duration
	PublicURL        string
	MetricsRetention time.Duration
	AuditRetention   time.Duration
	CleanupInterval  time.Duration
	FailureCooldown  time.Duration
}

const (
	defaultMetricsRetention = 7 * 24 * time.Hour
	defaultAuditRetention   = 90 * 24 * time.Hour
	defaultCleanupInterval  = time.Hour
	defaultFailureCooldown  = 5 * time.Second
)

func parseConfig(args []string) Config {
	cfg := Config{
		ListenAddr:       env("NYARELAY_LISTEN", ":8080"),
		DataDir:          env("NYARELAY_DATA", "./data"),
		NodeBinaryPath:   env("NYARELAY_NODE_BINARY", "/usr/local/bin/nyarelay-node"),
		NodeBinaryDir:    env("NYARELAY_NODE_BINARY_DIR", "/usr/local/lib/nyarelay"),
		LogLevel:         env("NYARELAY_LOG_LEVEL", "info"),
		SessionLifetime:  24 * time.Hour,
		PublicURL:        env("NYARELAY_PUBLIC_URL", ""),
		MetricsRetention: durationEnv("NYARELAY_METRICS_RETENTION", defaultMetricsRetention),
		AuditRetention:   durationEnv("NYARELAY_AUDIT_RETENTION", defaultAuditRetention),
		CleanupInterval:  durationEnv("NYARELAY_CLEANUP_INTERVAL", defaultCleanupInterval),
		FailureCooldown:  positiveDurationEnv("NYARELAY_FAILURE_COOLDOWN", defaultFailureCooldown),
	}
	fs := flag.NewFlagSet("controller", flag.ExitOnError)
	fs.StringVar(&cfg.ListenAddr, "listen", cfg.ListenAddr, "controller listen address")
	fs.StringVar(&cfg.DataDir, "data", cfg.DataDir, "controller data directory")
	fs.StringVar(&cfg.NodeBinaryPath, "node-binary", cfg.NodeBinaryPath, "node binary path")
	fs.StringVar(&cfg.NodeBinaryDir, "node-binary-dir", cfg.NodeBinaryDir, "directory for platform-specific node binaries")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level")
	fs.StringVar(&cfg.PublicURL, "public-url", cfg.PublicURL, "public URL shown in setup docs")
	fs.DurationVar(&cfg.MetricsRetention, "metrics-retention", cfg.MetricsRetention, "metrics retention duration; 0 disables cleanup")
	fs.DurationVar(&cfg.AuditRetention, "audit-retention", cfg.AuditRetention, "audit retention duration; 0 disables cleanup")
	fs.DurationVar(&cfg.CleanupInterval, "cleanup-interval", cfg.CleanupInterval, "history cleanup interval; 0 disables cleanup")
	fs.DurationVar(&cfg.FailureCooldown, "failure-cooldown", cfg.FailureCooldown, "candidate failure cooldown duration")
	_ = fs.Parse(args)
	cfg.DBPath = filepath.Join(cfg.DataDir, "nyarelay.db")
	return cfg
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := parseNonNegativeDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func positiveDurationEnv(key string, fallback time.Duration) time.Duration {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := parsePositiveDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parsePositiveDuration(value string) (time.Duration, error) {
	parsed, err := parseNonNegativeDuration(value)
	if err != nil {
		return 0, err
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("duration must be greater than zero")
	}
	if parsed%time.Second != 0 {
		return 0, fmt.Errorf("duration must use whole seconds")
	}
	return parsed, nil
}

func parseNonNegativeDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "0" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if parsed < 0 {
		return 0, fmt.Errorf("duration must not be negative")
	}
	return parsed, nil
}

func formatNonNegativeDuration(value time.Duration) string {
	if value == 0 {
		return "0s"
	}
	if value%time.Hour == 0 {
		return fmt.Sprintf("%dh", value/time.Hour)
	}
	if value%time.Minute == 0 {
		return fmt.Sprintf("%dm", value/time.Minute)
	}
	if value%time.Second == 0 {
		return fmt.Sprintf("%ds", value/time.Second)
	}
	return value.String()
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
