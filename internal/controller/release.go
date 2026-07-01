package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
	sharedversion "nyarelay/internal/shared/version"
)

const (
	nodeReleaseManifestFilename  = "node-release-manifest.json"
	nodeReleaseSignatureFilename = "node-release-manifest.sig"
	nodeReleasePublicKeyFilename = "node-release-public.key"
)

func (s *Server) nodeRelease() model.SignedNodeRelease {
	release := model.SignedNodeRelease{
		UpdateEnabled: false,
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
	payload, err := os.ReadFile(path)
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
	payload, err := os.ReadFile(path)
	if err != nil {
		return model.NodeReleaseArtifact{}, err
	}
	sum := sha256.Sum256(payload)
	return model.NodeReleaseArtifact{
		OS:     targetOS,
		Arch:   targetArch,
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(payload)),
	}, nil
}

func (s *Server) verifyNodeReleaseArtifacts(manifest model.NodeReleaseManifest) error {
	for _, artifact := range manifest.Artifacts {
		path := filepath.Join(s.cfg.NodeBinaryDir, fmt.Sprintf("nyarelay-node-%s-%s", artifact.OS, artifact.Arch))
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
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New(missingMessage)
		}
		return "", err
	}
	return strings.TrimSpace(string(payload)), nil
}

func nodeNeedsUpdate(currentVersion, desiredVersion string) bool {
	return sharedversion.NeedsUpdate(currentVersion, desiredVersion)
}
