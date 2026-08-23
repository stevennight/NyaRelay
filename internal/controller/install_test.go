package controller

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sharedcrypto "nyarelay/internal/shared/crypto"
	sharedversion "nyarelay/internal/shared/version"
)

func TestInstallScriptRestartsNodeServiceAfterOverwrite(t *testing.T) {
	script := installScript()

	if !strings.Contains(script, "systemctl enable nyarelay-node\nsystemctl restart nyarelay-node") {
		t.Fatalf("install script should restart nyarelay-node after overwriting the binary:\n%s", script)
	}
	if strings.Contains(script, "systemctl enable --now nyarelay-node\n") {
		t.Fatalf("install script should not rely on enable --now for existing running node service")
	}
}

func TestInstallScriptRunsNodeWithoutRootPrivileges(t *testing.T) {
	script := installScript()

	for _, fragment := range []string{
		"groupadd --system nyarelay",
		"useradd --system --gid nyarelay",
		"install -d -o nyarelay -g nyarelay -m 0700 /var/lib/nyarelay",
		"User=nyarelay\nGroup=nyarelay",
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"ReadWritePaths=/var/lib/nyarelay /usr/local/bin",
	} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("install script is missing hardening fragment %q:\n%s", fragment, script)
		}
	}
}

func TestInstallScriptVerifiesBinaryBeforeInstalling(t *testing.T) {
	script := installScript()
	verifyIndex := strings.Index(script, "openssl pkeyutl -verify")
	installIndex := strings.Index(script, "install -m 0755 \"$tmpdir/nyarelay-node\"")
	if verifyIndex < 0 || installIndex < 0 || verifyIndex > installIndex {
		t.Fatalf("install script must verify the binary before installing it:\n%s", script)
	}
	for _, fragment := range []string{
		"--update-signing-key)",
		"/downloads/nyarelay-node/signature?os=${os}&arch=${arch}",
		"a signed update key is required for non-local controllers",
	} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("install script is missing signature hardening fragment %q:\n%s", fragment, script)
		}
	}
}

func TestInstallScriptHasValidShellSyntax(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell subprocesses are unavailable in the Windows test sandbox")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not available")
	}
	cmd := exec.Command(shell, "-n")
	cmd.Stdin = strings.NewReader(installScript())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install script has invalid shell syntax: %v\n%s", err, output)
	}
}

func TestInstallSignatureCommandVerifiesEd25519Binary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("OpenSSL subprocesses are unavailable in the Windows test sandbox")
	}
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("OpenSSL is not available")
	}
	publicKey, privateKey, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	publicKeyBytes := mustDecodePublicKey(t, publicKey)
	privateKeyBytes, err := sharedcrypto.DecodePrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("signed node binary")
	signature := ed25519.Sign(privateKeyBytes, payload)
	der, err := x509.MarshalPKIXPublicKey(publicKeyBytes)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	publicKeyPath := filepath.Join(dir, "node.pub")
	payloadPath := filepath.Join(dir, "node")
	signaturePath := filepath.Join(dir, "node.sig")
	if err := os.WriteFile(publicKeyPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payloadPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signaturePath, signature, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(openssl, "pkeyutl", "-verify", "-pubin", "-inkey", publicKeyPath, "-rawin", "-in", payloadPath, "-sigfile", signaturePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("OpenSSL rejected Ed25519 binary signature: %v\n%s", err, output)
	}
}

func TestInstallUpdateSigningKeyEncodesTrustedEd25519PEM(t *testing.T) {
	publicKey, _, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	old := sharedversion.UpdatePublicKey
	sharedversion.UpdatePublicKey = publicKey
	t.Cleanup(func() { sharedversion.UpdatePublicKey = old })

	encoded, err := installUpdateSigningKey("https://relay.example.com")
	if err != nil {
		t.Fatal(err)
	}
	pemPayload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(pemPayload)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 || block.Type != "PUBLIC KEY" {
		t.Fatalf("encoded key is not a single public-key PEM block: %q", pemPayload)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	parsedPublic, ok := parsed.(ed25519.PublicKey)
	if !ok || string(parsedPublic) != string(mustDecodePublicKey(t, publicKey)) {
		t.Fatal("encoded install key does not match trusted update key")
	}
}

func TestInstallUpdateSigningKeyAllowsOnlyLoopbackWithoutKey(t *testing.T) {
	old := sharedversion.UpdatePublicKey
	sharedversion.UpdatePublicKey = ""
	t.Cleanup(func() { sharedversion.UpdatePublicKey = old })

	if got, err := installUpdateSigningKey("http://127.0.0.1:8080"); err != nil || got != "" {
		t.Fatalf("loopback install key = %q, err = %v", got, err)
	}
	if _, err := installUpdateSigningKey("https://relay.example.com"); err == nil {
		t.Fatal("expected remote install without update key to fail")
	}
}

func mustDecodePublicKey(t *testing.T, encoded string) ed25519.PublicKey {
	t.Helper()
	key, err := sharedcrypto.DecodePublicKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
