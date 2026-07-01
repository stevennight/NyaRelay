package controller

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"nyarelay/internal/controller/auth"
	"nyarelay/internal/controller/nodehub"
	"nyarelay/internal/controller/store"
	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
)

func TestUpdateNodeBinaryRequestsBundledReleaseVersion(t *testing.T) {
	ctx := context.Background()
	srv, session := testUpdateServer(t)
	publicKey, privateKey, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	restore := setUpdatePublicKeyForTest(t, publicKey)
	defer restore()
	writeSignedNodeReleaseForTest(t, srv.cfg.NodeBinaryDir, "v0.1.3", publicKey, privateKey)

	node := model.Node{
		ID:        "node-1",
		Name:      "node-1",
		Status:    model.NodeOffline,
		Version:   "v0.1.2",
		Approved:  true,
		CreatedAt: time.Now().UTC(),
	}
	if err := srv.store.UpsertNode(ctx, node, "node-token"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/update", bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("id", "node-1")
	rec := httptest.NewRecorder()
	srv.handleUpdateNodeBinary(rec, req, session)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	updated, err := srv.store.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.UpdateStatus != model.NodeUpdateRequested || updated.DesiredVersion != "v0.1.3" {
		t.Fatalf("node update state = %s/%s", updated.UpdateStatus, updated.DesiredVersion)
	}
}

func TestUpdateNodeBinaryRejectsDisabledRelease(t *testing.T) {
	srv, session := testUpdateServer(t)
	node := model.Node{
		ID:        "node-1",
		Name:      "node-1",
		Status:    model.NodeOffline,
		Version:   "v0.1.2",
		Approved:  true,
		CreatedAt: time.Now().UTC(),
	}
	if err := srv.store.UpsertNode(context.Background(), node, "node-token"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/update", bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("id", "node-1")
	rec := httptest.NewRecorder()
	srv.handleUpdateNodeBinary(rec, req, session)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func testUpdateServer(t *testing.T) (*Server, auth.Session) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return &Server{
			cfg:   Config{NodeBinaryDir: dir},
			log:   discardLogger(),
			hub:   nodehub.New(),
			store: st,
		}, auth.Session{
			UserID:   1,
			Username: "admin",
		}
}

func writeSignedNodeReleaseForTest(t *testing.T, dir, version, publicKey, privateKey string) {
	t.Helper()
	content := []byte("node-binary-" + version)
	writeFile(t, filepath.Join(dir, "nyarelay-node-linux-amd64"), content)
	manifest := model.NodeReleaseManifest{
		Version: version,
		Artifacts: []model.NodeReleaseArtifact{{
			OS:     "linux",
			Arch:   "amd64",
			SHA256: sha256Hex(content),
			Size:   int64(len(content)),
		}},
	}
	signature, err := sharedcrypto.SignJSON(privateKey, manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeReleaseFiles(t, dir, manifest, signature, publicKey)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
