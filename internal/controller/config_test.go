package controller

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseConfigHistoryCleanupDefaults(t *testing.T) {
	t.Setenv("NYARELAY_METRICS_RETENTION", "")
	t.Setenv("NYARELAY_AUDIT_RETENTION", "")
	t.Setenv("NYARELAY_CLEANUP_INTERVAL", "")
	t.Setenv("NYARELAY_FAILURE_COOLDOWN", "")

	cfg := parseConfig(nil)
	if cfg.MetricsRetention != defaultMetricsRetention {
		t.Fatalf("metrics retention = %s, want %s", cfg.MetricsRetention, defaultMetricsRetention)
	}
	if cfg.AuditRetention != defaultAuditRetention {
		t.Fatalf("audit retention = %s, want %s", cfg.AuditRetention, defaultAuditRetention)
	}
	if cfg.CleanupInterval != defaultCleanupInterval {
		t.Fatalf("cleanup interval = %s, want %s", cfg.CleanupInterval, defaultCleanupInterval)
	}
	if cfg.FailureCooldown != defaultFailureCooldown {
		t.Fatalf("failure cooldown = %s, want %s", cfg.FailureCooldown, defaultFailureCooldown)
	}
}

func TestParseConfigHistoryCleanupEnvironment(t *testing.T) {
	t.Setenv("NYARELAY_METRICS_RETENTION", "12h")
	t.Setenv("NYARELAY_AUDIT_RETENTION", "0s")
	t.Setenv("NYARELAY_CLEANUP_INTERVAL", "30m")
	t.Setenv("NYARELAY_FAILURE_COOLDOWN", "5s")

	cfg := parseConfig(nil)
	if cfg.MetricsRetention != 12*time.Hour || cfg.AuditRetention != 0 || cfg.CleanupInterval != 30*time.Minute || cfg.FailureCooldown != 5*time.Second {
		t.Fatalf("config = %s/%s/%s/%s", cfg.MetricsRetention, cfg.AuditRetention, cfg.CleanupInterval, cfg.FailureCooldown)
	}
}

func TestParseConfigRejectsTooFrequentCleanupInterval(t *testing.T) {
	t.Setenv("NYARELAY_CLEANUP_INTERVAL", "1s")
	cfg := parseConfig(nil)
	if cfg.CleanupInterval != defaultCleanupInterval {
		t.Fatalf("cleanup interval = %s, want default %s", cfg.CleanupInterval, defaultCleanupInterval)
	}
}

func TestParseConfigValidatesCleanupIntervalFlag(t *testing.T) {
	t.Setenv("NYARELAY_CLEANUP_INTERVAL", "30m")

	if cfg := parseConfig([]string{"-cleanup-interval=1s"}); cfg.CleanupInterval != defaultCleanupInterval {
		t.Fatalf("too-frequent cleanup interval = %s, want default %s", cfg.CleanupInterval, defaultCleanupInterval)
	}
	if cfg := parseConfig([]string{"-cleanup-interval=0"}); cfg.CleanupInterval != 0 {
		t.Fatalf("disabled cleanup interval = %s, want 0", cfg.CleanupInterval)
	}
	if cfg := parseConfig([]string{"-cleanup-interval=1m"}); cfg.CleanupInterval != time.Minute {
		t.Fatalf("minimum cleanup interval = %s, want %s", cfg.CleanupInterval, time.Minute)
	}
}

func TestParseCleanupIntervalAllowsDisableAndRejectsSubminuteValues(t *testing.T) {
	if got, err := parseCleanupInterval("0s"); err != nil || got != 0 {
		t.Fatalf("disabled cleanup interval = %s/%v, want 0/nil", got, err)
	}
	if _, err := parseCleanupInterval("59s"); err == nil {
		t.Fatal("expected subminute cleanup interval to be rejected")
	}
	if _, err := parseCleanupInterval("60.5s"); err == nil {
		t.Fatal("expected non-whole-second cleanup interval to be rejected")
	}
}

func TestLoadControllerSecretsKeyFromValue(t *testing.T) {
	expected := []byte("0123456789abcdef0123456789abcdef")
	cfg := Config{SecretsKey: base64.RawURLEncoding.EncodeToString(expected)}
	got, err := loadControllerSecretsKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(expected) {
		t.Fatalf("key = %q, want %q", got, expected)
	}
}

func TestLoadControllerSecretsKeyFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller-secrets.key")
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadControllerSecretsKey(Config{SecretsKeyFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != controllerSecretsKeyBytes {
		t.Fatalf("key length = %d, want %d", len(got), controllerSecretsKeyBytes)
	}
}

func TestLoadControllerSecretsKeyRequiresConfiguration(t *testing.T) {
	if _, err := loadControllerSecretsKey(Config{}); err == nil {
		t.Fatal("expected missing controller secrets key to fail")
	}
}
