package controller

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nyarelay/internal/controller/auth"
	"nyarelay/internal/controller/nodehub"
	"nyarelay/internal/controller/store"
	"nyarelay/internal/shared/model"
)

func TestLoadHistoryCleanupConfigUsesStoredValues(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.SetSettings(ctx, map[string]string{
		historyMetricsRetentionSetting: "12h",
		historyAuditRetentionSetting:   "48h",
		historyCleanupIntervalSetting:  "15m",
	}); err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		cfg: Config{
			MetricsRetention: defaultMetricsRetention,
			AuditRetention:   defaultAuditRetention,
			CleanupInterval:  defaultCleanupInterval,
		},
		store: st,
	}
	if err := srv.loadHistoryCleanupConfig(ctx); err != nil {
		t.Fatal(err)
	}
	got := srv.currentHistoryCleanupConfig()
	if got.MetricsRetention != 12*time.Hour || got.AuditRetention != 48*time.Hour || got.CleanupInterval != 15*time.Minute {
		t.Fatalf("history cleanup config = %#v", got)
	}
}

func TestHistoryCleanupLoopReloadsConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := &Server{
		cfg:         Config{},
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:       st,
		cleanupWake: make(chan struct{}, 1),
	}
	go srv.historyCleanupLoop(ctx)

	if err := st.InsertMetrics(ctx, model.MetricsReport{
		NodeID:     "node-1",
		ObservedAt: time.Now().UTC().Add(-2 * time.Hour),
		ForwardStats: []model.TrafficStat{{
			ID: "old-stat",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	srv.setHistoryCleanupConfig(historyCleanupConfig{
		MetricsRetention: time.Hour,
		CleanupInterval:  time.Hour,
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		summary, err := st.MetricSummary(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(summary) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("history cleanup did not reload and prune metrics")
}

func TestUpdateHistoryCleanupSettingsAPI(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := &Server{
		cfg: Config{
			MetricsRetention: defaultMetricsRetention,
			AuditRetention:   defaultAuditRetention,
			CleanupInterval:  defaultCleanupInterval,
		},
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		store: st,
		cleanupConfig: historyCleanupConfigFromConfig(Config{
			MetricsRetention: defaultMetricsRetention,
			AuditRetention:   defaultAuditRetention,
			CleanupInterval:  defaultCleanupInterval,
		}),
		cleanupWake: make(chan struct{}, 1),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/controller/info", strings.NewReader("{\"metrics_retention\":\"24h\",\"audit_retention\":\"720h\",\"cleanup_interval\":\"15m\"}"))
	rec := httptest.NewRecorder()

	srv.handleUpdateControllerInfo(rec, req, auth.Session{Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	history, ok := response["history_cleanup"].(map[string]any)
	if !ok {
		t.Fatalf("history_cleanup response = %#v", response["history_cleanup"])
	}
	if history["metrics_retention"] != "24h" || history["audit_retention"] != "720h" || history["cleanup_interval"] != "15m" {
		t.Fatalf("history_cleanup response = %#v", history)
	}
	got := srv.currentHistoryCleanupConfig()
	if got.MetricsRetention != 24*time.Hour || got.AuditRetention != 720*time.Hour || got.CleanupInterval != 15*time.Minute {
		t.Fatalf("runtime history cleanup config = %#v", got)
	}
	stored, ok, err := st.GetSetting(ctx, historyMetricsRetentionSetting)
	if err != nil || !ok || stored != "24h" {
		t.Fatalf("stored metrics retention = %q, %t, %v", stored, ok, err)
	}
}

func TestUpdateFailureCooldownSettingsAPI(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := &Server{
		cfg:             Config{FailureCooldown: defaultFailureCooldown},
		log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:           st,
		hub:             nodehub.New(),
		cleanupConfig:   historyCleanupConfigFromConfig(Config{}),
		cleanupWake:     make(chan struct{}, 1),
		failureCooldown: defaultFailureCooldown,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/controller/info", strings.NewReader(`{"failure_cooldown":"15s"}`))
	rec := httptest.NewRecorder()

	srv.handleUpdateControllerInfo(rec, req, auth.Session{Username: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response["failure_cooldown"] != "15s" {
		t.Fatalf("failure cooldown response = %#v", response["failure_cooldown"])
	}
	if got := srv.currentFailureCooldown(); got != 15*time.Second {
		t.Fatalf("runtime failure cooldown = %s", got)
	}
	stored, ok, err := st.GetSetting(ctx, failureCooldownSetting)
	if err != nil || !ok || stored != "15s" {
		t.Fatalf("stored failure cooldown = %q, %t, %v", stored, ok, err)
	}
	if got := srv.hub.Revision(); got != 1 {
		t.Fatalf("revision = %d, want 1", got)
	}
}
