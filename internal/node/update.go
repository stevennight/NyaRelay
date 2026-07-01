package node

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
	sharedversion "nyarelay/internal/shared/version"
)

const defaultNodeBinaryPath = "/usr/local/bin/nyarelay-node"

type updateOptions struct {
	requestPath string
	statusPath  string
	binaryPath  string
	skipRestart bool
}

func parseUpdateOptions(args []string, cfg Config) updateOptions {
	opts := updateOptions{
		requestPath: cfg.UpdateRequestPath,
		statusPath:  cfg.UpdateStatusPath,
		binaryPath:  defaultNodeBinaryPath,
	}
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	fs.StringVar(&opts.requestPath, "request", opts.requestPath, "update request path")
	fs.StringVar(&opts.statusPath, "status", opts.statusPath, "update status path")
	fs.StringVar(&opts.binaryPath, "binary", opts.binaryPath, "node binary path to replace")
	fs.BoolVar(&opts.skipRestart, "skip-restart", false, "skip systemd restart after update")
	_ = fs.Parse(args)
	return opts
}

func writeUpdateRequest(path string, req model.NodeUpdateRequest) error {
	if strings.TrimSpace(req.TargetVersion) == "" {
		return errors.New("target version is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadUpdateRequest(path string) (model.NodeUpdateRequest, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return model.NodeUpdateRequest{}, err
	}
	var req model.NodeUpdateRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return model.NodeUpdateRequest{}, err
	}
	return req, nil
}

func saveUpdateStatus(path string, report model.NodeUpdateReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if report.CompletedAt.IsZero() && (report.Status == model.NodeUpdateSucceeded || report.Status == model.NodeUpdateFailed) {
		report.CompletedAt = time.Now().UTC()
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

func loadUpdateStatus(path string) (model.NodeUpdateReport, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return model.NodeUpdateReport{}, err
	}
	var report model.NodeUpdateReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return model.NodeUpdateReport{}, err
	}
	return report, nil
}

func handleUpdateCommand(cfg Config, cmd model.NodeUpdateCommand) error {
	if strings.TrimSpace(sharedversion.UpdatePublicKey) == "" {
		return errors.New("update public key is not configured")
	}
	if cmd.SigningKeyID != sharedversion.UpdatePublicKey {
		return errors.New("update signing key does not match trusted key")
	}
	if err := sharedcrypto.VerifyJSON(sharedversion.UpdatePublicKey, cmd.Manifest, cmd.Signature); err != nil {
		return fmt.Errorf("verify update manifest: %w", err)
	}
	targetOS, targetArch := runtime.GOOS, runtime.GOARCH
	artifact, ok := findUpdateArtifact(cmd.Manifest, targetOS, targetArch)
	if !ok {
		return fmt.Errorf("no update artifact for %s/%s", targetOS, targetArch)
	}
	if !nodeNeedsUpdate(sharedversion.Version, cmd.Version) {
		return nil
	}
	if err := saveUpdateStatus(cfg.UpdateStatusPath, model.NodeUpdateReport{Status: model.NodeUpdateRequested, Version: cmd.Version}); err != nil {
		return err
	}
	return writeUpdateRequest(cfg.UpdateRequestPath, model.NodeUpdateRequest{
		ControllerURL: cfg.ControllerURL,
		NodeID:        cfg.NodeID,
		NodeToken:     cfg.NodeToken,
		TargetVersion: cmd.Version,
		OS:            targetOS,
		Arch:          targetArch,
		SHA256:        artifact.SHA256,
		RequestedAt:   time.Now().UTC(),
	})
}

func findUpdateArtifact(manifest model.NodeReleaseManifest, targetOS, targetArch string) (model.NodeReleaseArtifact, bool) {
	for _, artifact := range manifest.Artifacts {
		if artifact.OS == targetOS && artifact.Arch == targetArch {
			return artifact, true
		}
	}
	return model.NodeReleaseArtifact{}, false
}

func runUpdate(ctx context.Context, cfg Config, requestPath, statusPath, binaryPath string) error {
	req, err := loadUpdateRequest(requestPath)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(requestPath) }()
	if req.ControllerURL == "" {
		req.ControllerURL = cfg.ControllerURL
	}
	if req.NodeID == "" {
		req.NodeID = cfg.NodeID
	}
	if req.NodeToken == "" {
		req.NodeToken = cfg.NodeToken
	}
	if err := saveUpdateStatus(statusPath, model.NodeUpdateReport{Status: model.NodeUpdateRunning, Version: req.TargetVersion}); err != nil {
		return err
	}
	if err := performUpdate(ctx, req, binaryPath); err != nil {
		_ = saveUpdateStatus(statusPath, model.NodeUpdateReport{Status: model.NodeUpdateFailed, Version: req.TargetVersion, Error: err.Error()})
		return err
	}
	if err := saveUpdateStatus(statusPath, model.NodeUpdateReport{Status: model.NodeUpdateSucceeded, Version: req.TargetVersion}); err != nil {
		return err
	}
	return nil
}

func performUpdate(ctx context.Context, req model.NodeUpdateRequest, binaryPath string) error {
	release, err := fetchNodeRelease(ctx, req)
	if err != nil {
		return err
	}
	if !release.UpdateEnabled {
		return fmt.Errorf("node update disabled: %s", release.DisabledReason)
	}
	if release.SigningKeyID != sharedversion.UpdatePublicKey {
		return errors.New("release signing key does not match trusted key")
	}
	if release.Manifest.Version != req.TargetVersion {
		return fmt.Errorf("release version %s does not match requested %s", release.Manifest.Version, req.TargetVersion)
	}
	if err := sharedcrypto.VerifyJSON(sharedversion.UpdatePublicKey, release.Manifest, release.Signature); err != nil {
		return fmt.Errorf("verify release manifest: %w", err)
	}
	artifact, ok := findUpdateArtifact(release.Manifest, req.OS, req.Arch)
	if !ok {
		return fmt.Errorf("no artifact for %s/%s", req.OS, req.Arch)
	}
	if req.SHA256 != "" && !strings.EqualFold(req.SHA256, artifact.SHA256) {
		return errors.New("requested artifact digest does not match release manifest")
	}
	payload, err := downloadUpdateBinary(ctx, req)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), artifact.SHA256) {
		return errors.New("downloaded node binary sha256 mismatch")
	}
	return replaceBinary(binaryPath, payload)
}

func fetchNodeRelease(ctx context.Context, req model.NodeUpdateRequest) (model.SignedNodeRelease, error) {
	var release model.SignedNodeRelease
	if err := doUpdateJSON(ctx, req, http.MethodGet, "/downloads/nyarelay-node/manifest", nil, &release); err != nil {
		return model.SignedNodeRelease{}, err
	}
	return release, nil
}

func downloadUpdateBinary(ctx context.Context, req model.NodeUpdateRequest) ([]byte, error) {
	path := fmt.Sprintf("/downloads/nyarelay-node?os=%s&arch=%s&compress=gzip", url.QueryEscape(req.OS), url.QueryEscape(req.Arch))
	httpReq, err := newUpdateRequest(ctx, req, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download node binary failed: %s", resp.Status)
	}
	reader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	return io.ReadAll(reader)
}

func doUpdateJSON(ctx context.Context, req model.NodeUpdateRequest, method, path string, body io.Reader, dest any) error {
	httpReq, err := newUpdateRequest(ctx, req, method, path, body)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("controller request failed: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

func newUpdateRequest(ctx context.Context, req model.NodeUpdateRequest, method, path string, body io.Reader) (*http.Request, error) {
	base := strings.TrimRight(req.ControllerURL, "/")
	httpReq, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("X-NyaRelay-Node-ID", req.NodeID)
	httpReq.Header.Set("X-NyaRelay-Node-Token", req.NodeToken)
	return httpReq, nil
}

func replaceBinary(path string, payload []byte) error {
	if path == "" {
		return errors.New("binary path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nyarelay-node-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func nodeNeedsUpdate(currentVersion, desiredVersion string) bool {
	return sharedversion.NeedsUpdate(currentVersion, desiredVersion)
}
