package controller

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	ListenAddr        string
	DataDir           string
	DBPath            string
	NodeBinaryPath    string
	NodeBinaryDir     string
	SecretsKey        string
	SecretsKeyFile    string
	LogLevel          string
	SessionLifetime   time.Duration
	PublicURL         string
	TrustProxyHeaders bool
	TrustedProxyCIDRs string
	MetricsRetention  time.Duration
	AuditRetention    time.Duration
	CleanupInterval   time.Duration
	FailureCooldown   time.Duration
}

const (
	defaultMetricsRetention = 7 * 24 * time.Hour
	defaultAuditRetention   = 90 * 24 * time.Hour
	defaultCleanupInterval  = time.Hour
	defaultFailureCooldown  = 5 * time.Second
	minCleanupInterval      = time.Minute
)

func parseConfig(args []string) Config {
	cfg := Config{
		ListenAddr:        env("NYARELAY_LISTEN", ":8080"),
		DataDir:           env("NYARELAY_DATA", "./data"),
		NodeBinaryPath:    env("NYARELAY_NODE_BINARY", "/usr/local/bin/nyarelay-node"),
		NodeBinaryDir:     env("NYARELAY_NODE_BINARY_DIR", "/usr/local/lib/nyarelay"),
		SecretsKey:        env("NYARELAY_SECRETS_KEY", ""),
		SecretsKeyFile:    env("NYARELAY_SECRETS_KEY_FILE", ""),
		LogLevel:          env("NYARELAY_LOG_LEVEL", "info"),
		SessionLifetime:   24 * time.Hour,
		PublicURL:         env("NYARELAY_PUBLIC_URL", ""),
		TrustProxyHeaders: boolEnv("NYARELAY_TRUST_PROXY_HEADERS", false),
		TrustedProxyCIDRs: env("NYARELAY_TRUSTED_PROXY_CIDRS", ""),
		MetricsRetention:  durationEnv("NYARELAY_METRICS_RETENTION", defaultMetricsRetention),
		AuditRetention:    durationEnv("NYARELAY_AUDIT_RETENTION", defaultAuditRetention),
		CleanupInterval:   cleanupIntervalEnv("NYARELAY_CLEANUP_INTERVAL", defaultCleanupInterval),
		FailureCooldown:   positiveDurationEnv("NYARELAY_FAILURE_COOLDOWN", defaultFailureCooldown),
	}
	fs := flag.NewFlagSet("controller", flag.ExitOnError)
	fs.StringVar(&cfg.ListenAddr, "listen", cfg.ListenAddr, "controller listen address")
	fs.StringVar(&cfg.DataDir, "data", cfg.DataDir, "controller data directory")
	fs.StringVar(&cfg.NodeBinaryPath, "node-binary", cfg.NodeBinaryPath, "node binary path")
	fs.StringVar(&cfg.NodeBinaryDir, "node-binary-dir", cfg.NodeBinaryDir, "directory for platform-specific node binaries")
	fs.StringVar(&cfg.SecretsKey, "secrets-key", cfg.SecretsKey, "base64url controller secrets encryption key")
	fs.StringVar(&cfg.SecretsKeyFile, "secrets-key-file", cfg.SecretsKeyFile, "file containing the controller secrets encryption key")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level")
	fs.StringVar(&cfg.PublicURL, "public-url", cfg.PublicURL, "public URL shown in setup docs")
	fs.BoolVar(&cfg.TrustProxyHeaders, "trust-proxy-headers", cfg.TrustProxyHeaders, "trust X-Forwarded-* headers from the reverse proxy")
	fs.StringVar(&cfg.TrustedProxyCIDRs, "trusted-proxy-cidrs", cfg.TrustedProxyCIDRs, "comma-separated proxy IPs or CIDRs allowed to set X-Forwarded-* headers")
	fs.DurationVar(&cfg.MetricsRetention, "metrics-retention", cfg.MetricsRetention, "metrics retention duration; 0 disables cleanup")
	fs.DurationVar(&cfg.AuditRetention, "audit-retention", cfg.AuditRetention, "audit retention duration; 0 disables cleanup")
	fs.DurationVar(&cfg.CleanupInterval, "cleanup-interval", cfg.CleanupInterval, "history cleanup interval; 0 disables cleanup")
	fs.DurationVar(&cfg.FailureCooldown, "failure-cooldown", cfg.FailureCooldown, "candidate failure cooldown duration")
	_ = fs.Parse(args)
	if err := validateCleanupInterval(cfg.CleanupInterval); err != nil {
		cfg.CleanupInterval = defaultCleanupInterval
	}
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

func cleanupIntervalEnv(key string, fallback time.Duration) time.Duration {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := parseCleanupInterval(value)
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

func parseCleanupInterval(value string) (time.Duration, error) {
	parsed, err := parseNonNegativeDuration(value)
	if err != nil {
		return 0, err
	}
	if err := validateCleanupInterval(parsed); err != nil {
		return 0, err
	}
	return parsed, nil
}

func validateCleanupInterval(parsed time.Duration) error {
	if parsed != 0 && parsed < minCleanupInterval {
		return fmt.Errorf("cleanup interval must be zero or at least %s", minCleanupInterval)
	}
	if parsed%time.Second != 0 {
		return errors.New("cleanup interval must use whole seconds")
	}
	return nil
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

func boolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(env(key, "")))
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

const controllerSecretsKeyBytes = 32

func loadControllerSecretsKey(cfg Config) ([]byte, error) {
	if strings.TrimSpace(cfg.SecretsKey) != "" && strings.TrimSpace(cfg.SecretsKeyFile) != "" {
		return nil, errors.New("configure only one of NYARELAY_SECRETS_KEY and NYARELAY_SECRETS_KEY_FILE")
	}
	raw := strings.TrimSpace(cfg.SecretsKey)
	if raw == "" {
		path := strings.TrimSpace(cfg.SecretsKeyFile)
		if path == "" {
			return nil, errors.New("controller secrets key is required; set NYARELAY_SECRETS_KEY or NYARELAY_SECRETS_KEY_FILE")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("read controller secrets key file: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("controller secrets key file must be a regular file")
		}
		if info.Size() > 4096 {
			return nil, errors.New("controller secrets key file is too large")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("read controller secrets key file: %w", err)
		}
		payload, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read controller secrets key file: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close controller secrets key file: %w", closeErr)
		}
		if len(payload) > 4096 {
			return nil, errors.New("controller secrets key file is too large")
		}
		raw = strings.TrimSpace(string(payload))
	}
	raw = strings.Join(strings.Fields(raw), "")
	key, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		if padded, paddedErr := base64.URLEncoding.DecodeString(raw); paddedErr == nil {
			key = padded
		} else {
			if standard, standardErr := base64.StdEncoding.DecodeString(raw); standardErr == nil {
				key = standard
			} else if rawStandard, rawStandardErr := base64.RawStdEncoding.DecodeString(raw); rawStandardErr == nil {
				key = rawStandard
			} else {
				return nil, errors.New("controller secrets key must be base64 encoded")
			}
		}
	}
	if len(key) != controllerSecretsKeyBytes {
		return nil, fmt.Errorf("controller secrets key must decode to %d bytes", controllerSecretsKeyBytes)
	}
	return key, nil
}
