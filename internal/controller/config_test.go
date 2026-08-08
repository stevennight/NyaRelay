package controller

import (
	"testing"
	"time"
)

func TestParseConfigHistoryCleanupDefaults(t *testing.T) {
	t.Setenv("NYARELAY_METRICS_RETENTION", "")
	t.Setenv("NYARELAY_AUDIT_RETENTION", "")
	t.Setenv("NYARELAY_CLEANUP_INTERVAL", "")

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
}

func TestParseConfigHistoryCleanupEnvironment(t *testing.T) {
	t.Setenv("NYARELAY_METRICS_RETENTION", "12h")
	t.Setenv("NYARELAY_AUDIT_RETENTION", "0s")
	t.Setenv("NYARELAY_CLEANUP_INTERVAL", "30m")

	cfg := parseConfig(nil)
	if cfg.MetricsRetention != 12*time.Hour || cfg.AuditRetention != 0 || cfg.CleanupInterval != 30*time.Minute {
		t.Fatalf("history cleanup config = %s/%s/%s", cfg.MetricsRetention, cfg.AuditRetention, cfg.CleanupInterval)
	}
}
