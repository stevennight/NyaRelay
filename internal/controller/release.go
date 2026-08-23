package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
	sharedversion "nyarelay/internal/shared/version"
)

const nodeReleaseCacheTTL = time.Minute

const (
	maxNodeReleaseManifestBytes = 1 << 20
	maxNodeReleaseMetadataBytes = 16 << 10
	maxNodeReleaseArtifactBytes = 128 << 20
)

const (
	nodeReleaseManifestFilename  = "node-release-manifest.json"
	nodeReleaseSignatureFilename = "node-release-manifest.sig"
	nodeReleasePublicKeyFilename = "node-release-public.key"
)

func (s *Server) nodeRelease() model.SignedNodeRelease {
	s.releaseMu.Lock()
	defer s.releaseMu.Unlock()
	if !s.releaseCachedAt.IsZero() && time.Since(s.releaseCachedAt) < nodeReleaseCacheTTL {
		return s.releaseCache
	}
	release := s.nodeReleaseUncached()
	s.releaseCache = release
	s.releaseCachedAt = time.Now()
	return release
}

func (s *Server) nodeReleaseUncached() model.SignedNodeRelease {
	release := model.SignedNodeRelease{
		UpdateEnabled: false,
	}
	if strings.TrimSpace(s.cfg.NodeBinaryDir) == "" {
		release.DisabledReason = "node release directory is not configured"
		return release
	}
	manifest, err := s.nodeReleaseManifest()
	if err != nil {
		release.DisabledReason = err.Error()
		return release
	}
	release.Manifest = manifest

	signature, err := readTrimmedFile(filepath.Join(s.cfg.NodeBinaryDir, nodeReleaseSignatureFilename), "node release signature is not bundled")
	if err != nil {
		release.DisabledReason = err.Error()
		return release
	}
	signingKeyID, err := readTrimmedFile(filepath.Join(s.cfg.NodeBinaryDir, nodeReleasePublicKeyFilename), "node release public key is not bundled")
	if err != nil {
		release.DisabledReason = err.Error()
		return release
	}
	release.Signature = signature
	release.SigningKeyID = signingKeyID

	if len(manifest.Artifacts) == 0 {
		release.DisabledReason = "node release artifacts are not available"
		return release
	}
	if strings.TrimSpace(sharedversion.UpdatePublicKey) == "" {
		release.DisabledReason = "node update public key is not configured"
		return release
	}
	if release.SigningKeyID == "" || signature == "" {
		release.DisabledReason = "node release manifest signature is not configured"
		return release
	}
	if release.SigningKeyID != sharedversion.UpdatePublicKey {
		release.DisabledReason = "node release signing key does not match trusted key"
		return release
	}
	if err := sharedcrypto.VerifyJSON(sharedversion.UpdatePublicKey, manifest, signature); err != nil {
		release.DisabledReason = "node release manifest signature is invalid"
		return release
	}
	if err := s.verifyNodeReleaseArtifacts(manifest); err != nil {
		release.DisabledReason = err.Error()
		return release
	}
	release.UpdateEnabled = true
	return release
}

func (s *Server) nodeReleaseManifest() (model.NodeReleaseManifest, error) {
	path := filepath.Join(s.cfg.NodeBinaryDir, nodeReleaseManifestFilename)
	payload, err := readNodeReleaseFile(path, maxNodeReleaseManifestBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return model.NodeReleaseManifest{}, errors.New("node release manifest is not bundled")
		}
		return model.NodeReleaseManifest{}, err
	}
	var manifest model.NodeReleaseManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return model.NodeReleaseManifest{}, fmt.Errorf("read node release manifest: %w", err)
	}
	return manifest, nil
}

func nodeReleaseArtifact(path, targetOS, targetArch string) (model.NodeReleaseArtifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return model.NodeReleaseArtifact{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return model.NodeReleaseArtifact{}, err
	}
	if info.IsDir() || info.Size() <= 0 || info.Size() > maxNodeReleaseArtifactBytes {
		return model.NodeReleaseArtifact{}, errors.New("node release artifact has invalid size")
	}
	hash := sha256.New()
	readBytes, err := io.Copy(hash, io.LimitReader(file, maxNodeReleaseArtifactBytes+1))
	if err != nil {
		return model.NodeReleaseArtifact{}, err
	}
	if readBytes <= 0 || readBytes > maxNodeReleaseArtifactBytes || readBytes != info.Size() {
		return model.NodeReleaseArtifact{}, errors.New("node release artifact changed while reading")
	}
	sum := hash.Sum(nil)
	return model.NodeReleaseArtifact{
		OS:     targetOS,
		Arch:   targetArch,
		SHA256: hex.EncodeToString(sum),
		Size:   readBytes,
	}, nil
}

func (s *Server) verifyNodeReleaseArtifacts(manifest model.NodeReleaseManifest) error {
	for _, artifact := range manifest.Artifacts {
		targetOS, targetArch, err := normalizeNodeBinaryTarget(artifact.OS, artifact.Arch)
		if err != nil || targetOS != artifact.OS || targetArch != artifact.Arch {
			return fmt.Errorf("node release artifact %s/%s has invalid target", artifact.OS, artifact.Arch)
		}
		if artifact.Size <= 0 || artifact.Size > maxNodeReleaseArtifactBytes {
			return fmt.Errorf("node release artifact %s/%s has invalid size", artifact.OS, artifact.Arch)
		}
		path := filepath.Join(s.cfg.NodeBinaryDir, fmt.Sprintf("nyarelay-node-%s-%s", targetOS, targetArch))
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return fmt.Errorf("node release artifact %s/%s is not available", artifact.OS, artifact.Arch)
		}
		if info.Size() > maxNodeReleaseArtifactBytes {
			return fmt.Errorf("node release artifact %s/%s is too large", artifact.OS, artifact.Arch)
		}
		actual, err := nodeReleaseArtifact(path, artifact.OS, artifact.Arch)
		if err != nil {
			return fmt.Errorf("node release artifact %s/%s is not available", artifact.OS, artifact.Arch)
		}
		if !strings.EqualFold(actual.SHA256, artifact.SHA256) || actual.Size != artifact.Size {
			return fmt.Errorf("node release artifact %s/%s does not match bundled manifest", artifact.OS, artifact.Arch)
		}
	}
	return nil
}

func readTrimmedFile(path, missingMessage string) (string, error) {
	payload, err := readNodeReleaseFile(path, maxNodeReleaseMetadataBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New(missingMessage)
		}
		return "", err
	}
	return strings.TrimSpace(string(payload)), nil
}

func readNodeReleaseFile(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	payload, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxBytes {
		return nil, fmt.Errorf("file %s is too large", path)
	}
	return payload, nil
}

func nodeNeedsUpdate(currentVersion, desiredVersion string) bool {
	return sharedversion.NeedsUpdate(currentVersion, desiredVersion)
}
