package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
	sharedversion "nyarelay/internal/shared/version"
)

func TestNodeReleaseRequiresBundledSignedManifest(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{cfg: Config{NodeBinaryDir: dir}}

	release := srv.nodeRelease()

	if release.UpdateEnabled {
		t.Fatal("expected node update to be disabled without bundled release files")
	}
	if release.DisabledReason != "node release manifest is not bundled" {
		t.Fatalf("disabled reason = %q", release.DisabledReason)
	}
}

func TestNodeReleaseEnablesSignedBundledManifest(t *testing.T) {
	dir := t.TempDir()
	publicKey, privateKey, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	restore := setUpdatePublicKeyForTest(t, publicKey)
	defer restore()

	content := []byte("node-linux-amd64")
	writeFile(t, filepath.Join(dir, "nyarelay-node-linux-amd64"), content)
	manifest := model.NodeReleaseManifest{
		Version: "v0.1.3",
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

	srv := &Server{cfg: Config{NodeBinaryDir: dir}}
	release := srv.nodeRelease()

	if !release.UpdateEnabled {
		t.Fatalf("expected release enabled, disabled reason = %q", release.DisabledReason)
	}
	if release.Manifest.Version != "v0.1.3" {
		t.Fatalf("version = %q", release.Manifest.Version)
	}
	if release.Signature != signature || release.SigningKeyID != publicKey {
		t.Fatal("signed release metadata was not returned")
	}
}

func TestNodeReleaseRejectsManifestThatDoesNotMatchBundledArtifact(t *testing.T) {
	dir := t.TempDir()
	publicKey, privateKey, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	restore := setUpdatePublicKeyForTest(t, publicKey)
	defer restore()

	writeFile(t, filepath.Join(dir, "nyarelay-node-linux-amd64"), []byte("actual-node-binary"))
	manifest := model.NodeReleaseManifest{
		Version: "v0.1.3",
		Artifacts: []model.NodeReleaseArtifact{{
			OS:     "linux",
			Arch:   "amd64",
			SHA256: sha256Hex([]byte("different-node-binary")),
			Size:   int64(len("different-node-binary")),
		}},
	}
	signature, err := sharedcrypto.SignJSON(privateKey, manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeReleaseFiles(t, dir, manifest, signature, publicKey)

	srv := &Server{cfg: Config{NodeBinaryDir: dir}}
	release := srv.nodeRelease()

	if release.UpdateEnabled {
		t.Fatal("expected mismatched bundled artifact to disable updates")
	}
	if release.DisabledReason != "node release artifact linux/amd64 does not match bundled manifest" {
		t.Fatalf("disabled reason = %q", release.DisabledReason)
	}
}

func TestDownloadNodeBinarySignatureReturnsRawSignature(t *testing.T) {
	dir := t.TempDir()
	publicKey, privateKey, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("node-linux-amd64")
	signature, err := sharedcrypto.SignBytes(privateKey, payload)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "nyarelay-node-linux-amd64.sig"), []byte(signature+"\n"))

	srv := &Server{cfg: Config{NodeBinaryDir: dir}}
	req := httptest.NewRequest(http.MethodGet, "/downloads/nyarelay-node/signature?os=linux&arch=amd64", nil)
	rec := httptest.NewRecorder()
	srv.handleDownloadNodeBinarySignature(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("signature download failed: %d %s", rec.Code, rec.Body.String())
	}
	rawSignature, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rec.Body.Bytes(), rawSignature) {
		t.Fatalf("signature body = %x, want %x", rec.Body.Bytes(), rawSignature)
	}
	if err := sharedcrypto.VerifyBytes(publicKey, payload, base64.RawURLEncoding.EncodeToString(rec.Body.Bytes())); err != nil {
		t.Fatal(err)
	}
}

func writeReleaseFiles(t *testing.T, dir string, manifest model.NodeReleaseManifest, signature, publicKey string) {
	t.Helper()
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, nodeReleaseManifestFilename), payload)
	writeFile(t, filepath.Join(dir, nodeReleaseSignatureFilename), []byte(signature+"\n"))
	writeFile(t, filepath.Join(dir, nodeReleasePublicKeyFilename), []byte(publicKey+"\n"))
}

func writeFile(t *testing.T, path string, payload []byte) {
	t.Helper()
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func setUpdatePublicKeyForTest(t *testing.T, value string) func() {
	t.Helper()
	old := sharedversion.UpdatePublicKey
	sharedversion.UpdatePublicKey = value
	return func() {
		sharedversion.UpdatePublicKey = old
	}
}
