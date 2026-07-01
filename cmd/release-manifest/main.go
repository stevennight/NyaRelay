package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
)

func main() {
	var nodeDir, version, commit, buildDate, privateKey, privateKeyFile, expectedPublicKey, manifestOut, signatureOut, publicKeyOut, githubOutput string
	flag.StringVar(&nodeDir, "node-dir", "dist", "directory containing node binaries")
	flag.StringVar(&version, "version", "", "release version")
	flag.StringVar(&commit, "commit", "", "git commit")
	flag.StringVar(&buildDate, "build-date", "", "build date")
	flag.StringVar(&privateKey, "private-key", "", "base64url Ed25519 private key")
	flag.StringVar(&privateKeyFile, "private-key-file", "", "file containing a base64url Ed25519 private key")
	flag.StringVar(&expectedPublicKey, "expected-public-key", "", "expected base64url Ed25519 public key")
	flag.StringVar(&manifestOut, "manifest", "", "manifest output path")
	flag.StringVar(&signatureOut, "signature", "", "signature output path")
	flag.StringVar(&publicKeyOut, "public-key", "", "public key output path")
	flag.StringVar(&githubOutput, "github-output", "", "GitHub Actions output file")
	flag.Parse()

	if version == "" {
		fatal("version is required")
	}
	manifest := model.NodeReleaseManifest{
		Version:   version,
		Commit:    commit,
		BuildDate: buildDate,
	}
	for _, target := range []struct {
		os   string
		arch string
	}{
		{os: "linux", arch: "amd64"},
		{os: "linux", arch: "arm64"},
	} {
		path := filepath.Join(nodeDir, fmt.Sprintf("nyarelay-node-%s-%s", target.os, target.arch))
		payload, err := os.ReadFile(path)
		if err != nil {
			fatal(err.Error())
		}
		sum := sha256.Sum256(payload)
		manifest.Artifacts = append(manifest.Artifacts, model.NodeReleaseArtifact{
			OS:     target.os,
			Arch:   target.arch,
			SHA256: hex.EncodeToString(sum[:]),
			Size:   int64(len(payload)),
		})
	}

	if manifestOut != "" {
		writeJSON(manifestOut, manifest)
	}

	if privateKeyFile != "" {
		payload, err := os.ReadFile(privateKeyFile)
		if err != nil {
			fatal(err.Error())
		}
		privateKey = string(payload)
	}
	privateKey = compactToken(privateKey)
	expectedPublicKey = compactToken(expectedPublicKey)
	if (privateKey == "") != (expectedPublicKey == "") {
		fatal("update signing private key and public key must be configured together")
	}

	var signature, publicKey string
	if privateKey != "" {
		priv, err := sharedcrypto.DecodePrivateKey(privateKey)
		if err != nil {
			fatal(err.Error())
		}
		pub, ok := priv.Public().(ed25519.PublicKey)
		if !ok {
			fatal("invalid Ed25519 private key")
		}
		publicKey = sharedcrypto.EncodeKey(pub)
		if expectedPublicKey != "" && publicKey != expectedPublicKey {
			fatal("update signing private key does not match update public key")
		}
		signature, err = sharedcrypto.SignJSON(privateKey, manifest)
		if err != nil {
			fatal(err.Error())
		}
		if signatureOut != "" {
			writeText(signatureOut, signature+"\n")
		}
		if publicKeyOut != "" {
			writeText(publicKeyOut, publicKey+"\n")
		}
	}
	if githubOutput != "" {
		appendText(githubOutput, fmt.Sprintf("signature=%s\npublic_key=%s\n", signature, publicKey))
	}
}

func writeJSON(path string, value any) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fatal(err.Error())
	}
	writeText(path, string(payload)+"\n")
}

func writeText(path, value string) {
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		fatal(err.Error())
	}
}

func appendText(path, value string) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fatal(err.Error())
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(value); err != nil {
		fatal(err.Error())
	}
}

func compactToken(value string) string {
	return strings.Join(strings.Fields(value), "")
}

func fatal(message string) {
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
