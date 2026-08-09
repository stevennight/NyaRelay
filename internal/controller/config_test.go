package controller

import (
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
