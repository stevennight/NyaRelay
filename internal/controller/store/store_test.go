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
