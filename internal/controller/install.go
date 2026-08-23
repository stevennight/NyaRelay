package controller

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"

	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
	sharedversion "nyarelay/internal/shared/version"
)

type NodeInstallInfo struct {
	Node      model.Node `json:"node"`
	Token     string     `json:"token"`
	ScriptURL string     `json:"script_url,omitempty"`
	BinaryURL string     `json:"binary_url,omitempty"`
	Command   string     `json:"command,omitempty"`
}

func buildNodeInstallInfo(controllerURL, signingKey, updateSigningKey string, node model.Node, token string) NodeInstallInfo {
	return NodeInstallInfo{
		Node:      node,
		Token:     token,
		ScriptURL: installScriptURL(controllerURL),
		BinaryURL: nodeBinaryURL(controllerURL),
		Command:   installCommand(controllerURL, node.ID, token, signingKey, updateSigningKey),
	}
}

func installScript() string {
	return strings.TrimLeft(`#!/bin/sh
set -eu

controller=""
node_id=""
node_token=""
signing_key=""
update_signing_key=""

usage() {
	cat <<'EOF'
Usage: install.sh --controller https://relay.example.com --id node_x --token token_x --signing-key controller_public_key --update-signing-key base64_public_key_pem
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--controller)
			controller="${2:-}"
			shift 2
			;;
		--id|--node-id)
			node_id="${2:-}"
			shift 2
			;;
		--token)
			node_token="${2:-}"
			shift 2
			;;
		--signing-key)
			signing_key="${2:-}"
			shift 2
			;;
		--update-signing-key)
			update_signing_key="${2:-}"
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "unknown argument: $1" >&2
			usage >&2
			exit 1
			;;
	esac
done

if [ -z "$controller" ] || [ -z "$node_id" ] || [ -z "$node_token" ] || [ -z "$signing_key" ]; then
	echo "missing required arguments" >&2
	usage >&2
	exit 1
fi

case "$controller" in
	https://*) ;;
	http://127.0.0.1|http://127.0.0.1:*|http://localhost|http://localhost:*) ;;
	*) echo "controller URL must use HTTPS unless it points to localhost" >&2; exit 1 ;;
esac

controller_base="${controller%/}"
if [ -z "$update_signing_key" ]; then
	case "$controller_base" in
		http://127.0.0.1|http://127.0.0.1:*|http://localhost|http://localhost:*|https://127.0.0.1|https://127.0.0.1:*|https://localhost|https://localhost:*) ;;
		*) echo "a signed update key is required for non-local controllers" >&2; exit 1 ;;
	esac
fi

if [ "$(id -u)" -ne 0 ]; then
	echo "please run as root" >&2
	exit 1
fi

command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
command -v gzip >/dev/null 2>&1 || { echo "gzip is required" >&2; exit 1; }
if [ -n "$update_signing_key" ]; then
	command -v base64 >/dev/null 2>&1 || { echo "base64 is required" >&2; exit 1; }
	command -v openssl >/dev/null 2>&1 || { echo "openssl is required" >&2; exit 1; }
fi
command -v systemctl >/dev/null 2>&1 || { echo "systemd is required" >&2; exit 1; }
command -v getent >/dev/null 2>&1 || { echo "getent is required" >&2; exit 1; }
command -v groupadd >/dev/null 2>&1 || { echo "groupadd is required" >&2; exit 1; }
command -v useradd >/dev/null 2>&1 || { echo "useradd is required" >&2; exit 1; }

if ! getent group nyarelay >/dev/null 2>&1; then
	groupadd --system nyarelay
fi
if ! id -u nyarelay >/dev/null 2>&1; then
	useradd --system --gid nyarelay --home-dir /var/lib/nyarelay --no-create-home --shell /usr/sbin/nologin nyarelay
fi

curl_progress=""
if curl --progress-meter --version >/dev/null 2>&1; then
	curl_progress="--progress-meter"
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
	linux) os="linux" ;;
	*) echo "unsupported operating system: $os" >&2; exit 1 ;;
esac

arch="$(uname -m)"
case "$arch" in
	x86_64|amd64) arch="amd64" ;;
	aarch64|arm64) arch="arm64" ;;
	*) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

binary_url="${controller_base}/downloads/nyarelay-node?os=${os}&arch=${arch}&compress=gzip"
echo "[1/6] Downloading nyarelay-node for ${os}/${arch}"
curl -fsS --compressed $curl_progress --retry 3 --retry-delay 2 "$binary_url" -o "$tmpdir/nyarelay-node.gz"
echo "[2/6] Decompressing nyarelay-node"
gzip -dc "$tmpdir/nyarelay-node.gz" > "$tmpdir/nyarelay-node"
if [ -n "$update_signing_key" ]; then
	public_key_path="$tmpdir/nyarelay-node.pub"
	signature_url="${controller_base}/downloads/nyarelay-node/signature?os=${os}&arch=${arch}"
	echo "[3/6] Downloading nyarelay-node signature"
	curl -fsS --compressed $curl_progress --retry 3 --retry-delay 2 "$signature_url" -o "$tmpdir/nyarelay-node.sig"
	echo "[4/6] Verifying nyarelay-node signature"
	if ! printf '%s' "$update_signing_key" | base64 -d > "$public_key_path" 2>/dev/null; then
		echo "invalid update signing key" >&2
		exit 1
	fi
	if ! openssl pkeyutl -verify -pubin -inkey "$public_key_path" -rawin -in "$tmpdir/nyarelay-node" -sigfile "$tmpdir/nyarelay-node.sig" >/dev/null 2>&1; then
		echo "node binary signature verification failed" >&2
		exit 1
	fi
else
	echo "[3/6] Skipping signature verification for local development controller"
fi
echo "[5/6] Installing nyarelay-node"
install -m 0755 "$tmpdir/nyarelay-node" /usr/local/bin/nyarelay-node

echo "[6/6] Writing node configuration"
install -d -m 0755 /etc/nyarelay
install -d -o nyarelay -g nyarelay -m 0700 /var/lib/nyarelay
chown -R nyarelay:nyarelay /var/lib/nyarelay

cat > /etc/nyarelay/node.env <<EOF
NYARELAY_CONTROLLER=$controller
NYARELAY_NODE_ID=$node_id
NYARELAY_NODE_TOKEN=$node_token
NYARELAY_SIGNING_KEY=$signing_key
NYARELAY_DATA=/var/lib/nyarelay
NYARELAY_LOG_LEVEL=info
EOF
chmod 600 /etc/nyarelay/node.env

echo "systemd service installed and started"
cat > /etc/systemd/system/nyarelay-node.service <<'EOF'
[Unit]
Description=NyaRelay node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/nyarelay/node.env
ExecStart=/usr/local/bin/nyarelay-node
User=nyarelay
Group=nyarelay
Restart=always
RestartSec=3
NoNewPrivileges=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
UMask=0077
ReadWritePaths=/var/lib/nyarelay
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/nyarelay-node-update.service <<'EOF'
[Unit]
Description=NyaRelay node updater
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
EnvironmentFile=/etc/nyarelay/node.env
ExecStart=/usr/local/bin/nyarelay-node update
User=root
Group=root
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
UMask=0077
ReadWritePaths=/var/lib/nyarelay /usr/local/bin
PrivateTmp=true

EOF

cat > /etc/systemd/system/nyarelay-node-update.path <<'EOF'
[Unit]
Description=Watch NyaRelay node update requests

[Path]
PathExists=/var/lib/nyarelay/update/request.json
Unit=nyarelay-node-update.service

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable nyarelay-node
systemctl restart nyarelay-node
systemctl enable --now nyarelay-node-update.path
echo "nyarelay node installed"
`, "\n")
}

func installCommand(controllerURL, nodeID, token, signingKey, updateSigningKey string) string {
	controllerURL = strings.TrimRight(controllerURL, "/")
	if controllerURL == "" {
		return ""
	}
	return fmt.Sprintf(
		"curl -fsS %s | sudo sh -s -- --controller %s --id %s --token %s --signing-key %s --update-signing-key %s",
		shellQuote(installScriptURL(controllerURL)),
		shellQuote(controllerURL),
		shellQuote(nodeID),
		shellQuote(token),
		shellQuote(signingKey),
		shellQuote(updateSigningKey),
	)
}

func installUpdateSigningKey(controllerURL string) (string, error) {
	encodedKey := strings.Join(strings.Fields(sharedversion.UpdatePublicKey), "")
	if encodedKey == "" {
		if controllerURL == "" || loopbackControllerURL(controllerURL) {
			return "", nil
		}
		return "", errors.New("node installation requires a configured update public key")
	}
	publicKey, err := sharedcrypto.DecodePublicKey(encodedKey)
	if err != nil {
		return "", fmt.Errorf("invalid update public key: %w", err)
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("marshal update public key: %w", err)
	}
	publicKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return base64.StdEncoding.EncodeToString(publicKeyPEM), nil
}

func loopbackControllerURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Hostname() == "" {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && isLoopbackURLHost(u.Hostname())
}

func installScriptURL(controllerURL string) string {
	controllerURL = strings.TrimRight(controllerURL, "/")
	if controllerURL == "" {
		return "/install.sh"
	}
	return controllerURL + "/install.sh"
}

func nodeBinaryURL(controllerURL string) string {
	controllerURL = strings.TrimRight(controllerURL, "/")
	if controllerURL == "" {
		return "/downloads/nyarelay-node"
	}
	return controllerURL + "/downloads/nyarelay-node"
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
