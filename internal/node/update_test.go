package node

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
	sharedversion "nyarelay/internal/shared/version"
)

func TestHandleUpdateCommandWritesVerifiedRequest(t *testing.T) {
	publicKey, privateKey, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	restore := setUpdatePublicKeyForTest(t, publicKey)
	defer restore()

	dir := t.TempDir()
	manifest := model.NodeReleaseManifest{
		Version: "v0.1.4",
		Artifacts: []model.NodeReleaseArtifact{{
			OS:     runtime.GOOS,
			Arch:   runtime.GOARCH,
			SHA256: "abc123",
			Size:   12,
		}},
	}
	signature, err := sharedcrypto.SignJSON(privateKey, manifest)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		ControllerURL:     "https://relay.example.com",
		NodeID:            "node-1",
		NodeToken:         "node-token",
		UpdateRequestPath: filepath.Join(dir, "request.json"),
		UpdateStatusPath:  filepath.Join(dir, "status.json"),
	}

	if err := handleUpdateCommand(cfg, model.NodeUpdateCommand{
		Version:      "v0.1.4",
		Manifest:     manifest,
		Signature:    signature,
		SigningKeyID: publicKey,
	}); err != nil {
		t.Fatal(err)
	}

	req, err := loadUpdateRequest(cfg.UpdateRequestPath)
	if err != nil {
		t.Fatal(err)
	}
	if req.TargetVersion != "v0.1.4" || req.SHA256 != "abc123" {
		t.Fatalf("request = %#v", req)
	}
	report, err := loadUpdateStatus(cfg.UpdateStatusPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != model.NodeUpdateRequested || report.Version != "v0.1.4" {
		t.Fatalf("status = %#v", report)
	}
}

func TestHandleUpdateCommandRejectsUntrustedSigner(t *testing.T) {
	publicKey, _, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	otherPublicKey, otherPrivateKey, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	restore := setUpdatePublicKeyForTest(t, publicKey)
	defer restore()

	manifest := model.NodeReleaseManifest{Version: "v0.1.4"}
	signature, err := sharedcrypto.SignJSON(otherPrivateKey, manifest)
	if err != nil {
		t.Fatal(err)
	}

	err = handleUpdateCommand(Config{}, model.NodeUpdateCommand{
		Version:      "v0.1.4",
		Manifest:     manifest,
		Signature:    signature,
		SigningKeyID: otherPublicKey,
	})
	if err == nil {
		t.Fatal("expected untrusted update signer to fail")
	}
}

func TestPerformUpdateVerifiesManifestAndReplacesBinary(t *testing.T) {
	publicKey, privateKey, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	restore := setUpdatePublicKeyForTest(t, publicKey)
	defer restore()

	updatePayload := []byte("new-node-binary")
	artifact := model.NodeReleaseArtifact{
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		SHA256: sha256Hex(updatePayload),
		Size:   int64(len(updatePayload)),
	}
	manifest := model.NodeReleaseManifest{
		Version:   "v0.1.4",
		Artifacts: []model.NodeReleaseArtifact{artifact},
	}
	signature, err := sharedcrypto.SignJSON(privateKey, manifest)
	if err != nil {
		t.Fatal(err)
	}
	release := model.SignedNodeRelease{
		Manifest:      manifest,
		Signature:     signature,
		SigningKeyID:  publicKey,
		UpdateEnabled: true,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/downloads/nyarelay-node/manifest":
			_ = json.NewEncoder(w).Encode(release)
		case "/downloads/nyarelay-node":
			w.Header().Set("Content-Type", "application/gzip")
			gz := gzip.NewWriter(w)
			_, _ = gz.Write(updatePayload)
			_ = gz.Close()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "nyarelay-node")
	if err := os.WriteFile(binaryPath, []byte("old-node-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	err = performUpdate(context.Background(), model.NodeUpdateRequest{
		ControllerURL: server.URL,
		NodeID:        "node-1",
		NodeToken:     "node-token",
		TargetVersion: "v0.1.4",
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		SHA256:        artifact.SHA256,
	}, binaryPath)
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(updatePayload) {
		t.Fatalf("binary = %q", got)
	}
}

func TestValidateUpdateRequestBindsRequestToNodeConfiguration(t *testing.T) {
	cfg := Config{
		ControllerURL: "https://relay.example.com/",
		NodeID:        "node-1",
		NodeToken:     "node-token",
	}
	base := model.NodeUpdateRequest{
		ControllerURL: cfg.ControllerURL,
		NodeID:        cfg.NodeID,
		NodeToken:     cfg.NodeToken,
		TargetVersion: "v0.1.4",
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		SHA256:        strings.Repeat("a", sha256.Size*2),
	}
	if err := validateUpdateRequest(cfg, base); err != nil {
		t.Fatalf("valid update request rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*model.NodeUpdateRequest)
	}{
		{name: "controller", mutate: func(req *model.NodeUpdateRequest) { req.ControllerURL = "https://attacker.example.com" }},
		{name: "node id", mutate: func(req *model.NodeUpdateRequest) { req.NodeID = "node-2" }},
		{name: "node token", mutate: func(req *model.NodeUpdateRequest) { req.NodeToken = "other-token" }},
		{name: "platform", mutate: func(req *model.NodeUpdateRequest) { req.Arch = "other-arch" }},
		{name: "digest", mutate: func(req *model.NodeUpdateRequest) { req.SHA256 = "not-a-digest" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := base
			tt.mutate(&req)
			if err := validateUpdateRequest(cfg, req); err == nil {
				t.Fatal("expected update request to be rejected")
			}
		})
	}
}

func setUpdatePublicKeyForTest(t *testing.T, value string) func() {
	t.Helper()
	old := sharedversion.UpdatePublicKey
	sharedversion.UpdatePublicKey = value
	return func() {
		sharedversion.UpdatePublicKey = old
	}
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
