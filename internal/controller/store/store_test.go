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
