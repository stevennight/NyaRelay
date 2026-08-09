package store

import (
	"context"
	"testing"
	"time"

	"nyarelay/internal/shared/model"
)

func TestMarkNodeSeenCompletesPendingUpdateWhenReportedVersionIsNewer(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	node := model.Node{
		ID:        "node-1",
		Name:      "node-1",
		Status:    model.NodeOffline,
		Version:   "v0.1.5",
		Approved:  true,
		CreatedAt: time.Now().UTC(),
	}
	if err := st.UpsertNode(ctx, node, "token"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RequestNodeUpdate(ctx, node.ID, "v0.1.6"); err != nil {
		t.Fatal(err)
	}

	if err := st.MarkNodeSeen(ctx, node.ID, model.NodeSystem{OS: "linux", Arch: "amd64"}, "v0.1.7"); err != nil {
		t.Fatal(err)
	}

	updated, err := st.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != "v0.1.7" {
		t.Fatalf("version = %q", updated.Version)
	}
	if updated.DesiredVersion != "v0.1.7" {
		t.Fatalf("desired version = %q", updated.DesiredVersion)
	}
	if updated.UpdateStatus != model.NodeUpdateSucceeded {
		t.Fatalf("update status = %q", updated.UpdateStatus)
	}
	if updated.UpdateFinishedAt.IsZero() {
		t.Fatal("expected update finished time")
	}
}

func TestMarkNodeSeenKeepsPendingUpdateWhenReportedVersionIsOlder(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	node := model.Node{
		ID:        "node-1",
		Name:      "node-1",
		Status:    model.NodeOffline,
		Version:   "v0.1.5",
		Approved:  true,
		CreatedAt: time.Now().UTC(),
	}
	if err := st.UpsertNode(ctx, node, "token"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RequestNodeUpdate(ctx, node.ID, "v0.1.7"); err != nil {
		t.Fatal(err)
	}

	if err := st.MarkNodeSeen(ctx, node.ID, model.NodeSystem{OS: "linux", Arch: "amd64"}, "v0.1.6"); err != nil {
		t.Fatal(err)
	}

	updated, err := st.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DesiredVersion != "v0.1.7" {
		t.Fatalf("desired version = %q", updated.DesiredVersion)
	}
	if updated.UpdateStatus != model.NodeUpdateRequested {
		t.Fatalf("update status = %q", updated.UpdateStatus)
	}
	if !updated.UpdateFinishedAt.IsZero() {
		t.Fatalf("unexpected update finished time: %s", updated.UpdateFinishedAt)
	}
}

func TestSaveForwardRoundTripsTargetPoolAndStrategies(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	forward := model.Forward{
		ID:          "fwd-pool",
		Name:        "pool",
		TunnelID:    "tun-1",
		Protocols:   []model.ForwardProtocol{model.ForwardProtocolTCP, model.ForwardProtocolUDP},
		Listen:      "127.0.0.1:8443",
		Target:      "127.0.0.1:443",
		Strategy:    "failover",
		TCPStrategy: "round_robin",
		UDPStrategy: "failover",
		Targets: []model.ForwardTarget{
			{ID: "target-a", Address: "127.0.0.1:443", Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP}, Weight: 2, Enabled: true},
			{ID: "target-b", Address: "127.0.0.1:5353", Protocols: []model.ForwardProtocol{model.ForwardProtocolUDP}, Weight: 1, Enabled: true},
		},
		Enabled: true,
	}
	if _, err := st.SaveForward(ctx, forward, nil); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetForward(ctx, forward.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Target != "127.0.0.1:443" || got.Strategy != "failover" || got.TCPStrategy != "round_robin" || got.UDPStrategy != "failover" {
		t.Fatalf("forward compatibility fields = %#v", got)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("target count = %d, want 2", len(got.Targets))
	}
	if got.Targets[0].ID != "target-a" || got.Targets[0].ForwardID != forward.ID || got.Targets[0].Weight != 2 {
		t.Fatalf("first target = %#v", got.Targets[0])
	}
	if got.Targets[1].ID != "target-b" || got.Targets[1].Position != 1 || len(got.Targets[1].Protocols) != 1 || got.Targets[1].Protocols[0] != model.ForwardProtocolUDP {
		t.Fatalf("second target = %#v", got.Targets[1])
	}
}

func TestSaveForwardKeepsLegacyTargetReadable(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	forward := model.Forward{
		ID:        "fwd-legacy",
		Name:      "legacy",
		TunnelID:  "tun-1",
		Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP},
		Listen:    "127.0.0.1:8444",
		Target:    "127.0.0.1:444",
		Enabled:   true,
	}
	if _, err := st.SaveForward(ctx, forward, nil); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetForward(ctx, forward.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Targets) != 1 || got.Targets[0].Address != forward.Target || got.Targets[0].ID != "legacy:"+forward.ID {
		t.Fatalf("legacy target = %#v", got.Targets)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return st
}

func TestPruneHistoryDeletesOnlyExpiredRows(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour).Format(time.RFC3339Nano)
	recent := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO metrics (node_id, stat_id, stat_kind, bytes_in, bytes_out, connections, observed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?)
	`, "node-1", "old", "forward", 1, 2, 3, old, "node-1", "recent", "forward", 4, 5, 6, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO audit (actor, action, target, detail, created_at)
		VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)
	`, "admin", "old", "controller", "{}", old, "admin", "recent", "controller", "{}", recent); err != nil {
		t.Fatal(err)
	}

	metricsDeleted, auditDeleted, err := st.PruneHistory(ctx, now.Add(-24*time.Hour), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if metricsDeleted != 1 || auditDeleted != 1 {
		t.Fatalf("deleted metrics/audit = %d/%d, want 1/1", metricsDeleted, auditDeleted)
	}

	var metricsCount, auditCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metrics`).Scan(&metricsCount); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if metricsCount != 1 || auditCount != 1 {
		t.Fatalf("remaining metrics/audit = %d/%d, want 1/1", metricsCount, auditCount)
	}
}

func TestMigrateSingleCandidateStrategies(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if _, err := st.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, singleCandidateStrategyMigrationSetting); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO forwards
			(id, name, tunnel_id, protocols_json, listen, target, strategy, tcp_strategy, udp_strategy, enabled, created_at, updated_at)
		VALUES
			('f-single', 'single', 'tun-1', '[]', '', '', 'failover', 'round_robin', 'random', 1, ?, ?),
			('f-multi', 'multi', 'tun-1', '[]', '', '', 'failover', 'round_robin', 'random', 1, ?, ?),
			('f-disabled', 'disabled', 'tun-1', '[]', '', '', 'failover', 'round_robin', 'random', 1, ?, ?)
	`, now, now, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO forward_targets
			(id, forward_id, position, address, protocols_json, weight, enabled, created_at, updated_at)
		VALUES
			('ft-single', 'f-single', 0, '127.0.0.1:1', '[]', 1, 1, ?, ?),
			('ft-multi-a', 'f-multi', 0, '127.0.0.1:1', '[]', 1, 1, ?, ?),
			('ft-multi-b', 'f-multi', 1, '127.0.0.1:2', '[]', 1, 1, ?, ?),
			('ft-disabled', 'f-disabled', 0, '127.0.0.1:3', '[]', 1, 0, ?, ?)
	`, now, now, now, now, now, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO tunnel_stages
			(id, tunnel_id, stage_index, role, strategy, tcp_strategy, udp_strategy, created_at, updated_at)
		VALUES
			('stage-single', 'tun-1', 1, 'middle', 'failover', 'round_robin', 'random', ?, ?),
			('stage-multi', 'tun-1', 2, 'middle', 'failover', 'round_robin', 'random', ?, ?)
	`, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO tunnel_stage_nodes
			(id, tunnel_id, stage_id, node_id, created_at, updated_at)
		VALUES
			('sn-single', 'tun-1', 'stage-single', 'node-a', ?, ?),
			('sn-multi-a', 'tun-1', 'stage-multi', 'node-a', ?, ?),
			('sn-multi-b', 'tun-1', 'stage-multi', 'node-b', ?, ?)
	`, now, now, now, now, now, now); err != nil {
		t.Fatal(err)
	}

	if err := st.migrateSingleCandidateStrategies(ctx); err != nil {
		t.Fatal(err)
	}

	var strategy, tcpStrategy, udpStrategy string
	if err := st.db.QueryRowContext(ctx, `SELECT strategy, tcp_strategy, udp_strategy FROM forwards WHERE id = 'f-single'`).Scan(&strategy, &tcpStrategy, &udpStrategy); err != nil {
		t.Fatal(err)
	}
	if strategy != "single" || tcpStrategy != "single" || udpStrategy != "single" {
		t.Fatalf("single forward strategies = %q/%q/%q", strategy, tcpStrategy, udpStrategy)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT strategy, tcp_strategy, udp_strategy FROM forwards WHERE id = 'f-multi'`).Scan(&strategy, &tcpStrategy, &udpStrategy); err != nil {
		t.Fatal(err)
	}
	if strategy != "failover" || tcpStrategy != "round_robin" || udpStrategy != "random" {
		t.Fatalf("multi forward strategies changed = %q/%q/%q", strategy, tcpStrategy, udpStrategy)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT strategy, tcp_strategy, udp_strategy FROM forwards WHERE id = 'f-disabled'`).Scan(&strategy, &tcpStrategy, &udpStrategy); err != nil {
		t.Fatal(err)
	}
	if strategy != "failover" || tcpStrategy != "round_robin" || udpStrategy != "random" {
		t.Fatalf("disabled forward strategies changed = %q/%q/%q", strategy, tcpStrategy, udpStrategy)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT strategy, tcp_strategy, udp_strategy FROM tunnel_stages WHERE id = 'stage-single'`).Scan(&strategy, &tcpStrategy, &udpStrategy); err != nil {
		t.Fatal(err)
	}
	if strategy != "single" || tcpStrategy != "single" || udpStrategy != "single" {
		t.Fatalf("single stage strategies = %q/%q/%q", strategy, tcpStrategy, udpStrategy)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT strategy, tcp_strategy, udp_strategy FROM tunnel_stages WHERE id = 'stage-multi'`).Scan(&strategy, &tcpStrategy, &udpStrategy); err != nil {
		t.Fatal(err)
	}
	if strategy != "failover" || tcpStrategy != "round_robin" || udpStrategy != "random" {
		t.Fatalf("multi stage strategies changed = %q/%q/%q", strategy, tcpStrategy, udpStrategy)
	}
}
