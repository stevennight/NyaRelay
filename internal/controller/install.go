package controller

import (
	"fmt"
	"strings"

	"nyarelay/internal/shared/model"
)

type NodeInstallInfo struct {
	Node      model.Node `json:"node"`
	Token     string     `json:"token"`
	ScriptURL string     `json:"script_url,omitempty"`
	BinaryURL string     `json:"binary_url,omitempty"`
	Command   string     `json:"command,omitempty"`
}

func buildNodeInstallInfo(controllerURL, signingKey string, node model.Node, token string) NodeInstallInfo {
	return NodeInstallInfo{
		Node:      node,
		Token:     token,
		ScriptURL: installScriptURL(controllerURL),
		BinaryURL: nodeBinaryURL(controllerURL),
		Command:   installCommand(controllerURL, node.ID, token, signingKey),
	}
}

func installScript() string {
	return strings.TrimLeft(`#!/bin/sh
set -eu

controller=""
node_id=""
node_token=""
signing_key=""

usage() {
	cat <<'EOF'
Usage: install.sh --controller https://relay.example.com --id node_x --token token_x --signing-key controller_public_key
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

if [ "$(id -u)" -ne 0 ]; then
	echo "please run as root" >&2
	exit 1
fi

command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
command -v gzip >/dev/null 2>&1 || { echo "gzip is required" >&2; exit 1; }
command -v systemctl >/dev/null 2>&1 || { echo "systemd is required" >&2; exit 1; }

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

binary_url="${controller%/}/downloads/nyarelay-node?os=${os}&arch=${arch}&compress=gzip"
echo "[1/5] Downloading nyarelay-node for ${os}/${arch}"
curl -fL $curl_progress --retry 3 --retry-delay 2 "$binary_url" -o "$tmpdir/nyarelay-node.gz"
echo "[2/5] Decompressing nyarelay-node"
gzip -dc "$tmpdir/nyarelay-node.gz" > "$tmpdir/nyarelay-node"
echo "[3/5] Installing nyarelay-node"
install -m 0755 "$tmpdir/nyarelay-node" /usr/local/bin/nyarelay-node

echo "[4/5] Writing node configuration"
install -d -m 0755 /etc/nyarelay
install -d -m 0755 /var/lib/nyarelay

cat > /etc/nyarelay/node.env <<EOF
NYARELAY_CONTROLLER=$controller
NYARELAY_NODE_ID=$node_id
NYARELAY_NODE_TOKEN=$node_token
NYARELAY_SIGNING_KEY=$signing_key
NYARELAY_DATA=/var/lib/nyarelay
NYARELAY_LOG_LEVEL=info
EOF
chmod 600 /etc/nyarelay/node.env

echo "[5/5] Installing and starting systemd service"
cat > /etc/systemd/system/nyarelay-node.service <<'EOF'
[Unit]
Description=NyaRelay node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/nyarelay/node.env
ExecStart=/usr/local/bin/nyarelay-node
Restart=always
RestartSec=3
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
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

func installCommand(controllerURL, nodeID, token, signingKey string) string {
	controllerURL = strings.TrimRight(controllerURL, "/")
	if controllerURL == "" {
		return ""
	}
	return fmt.Sprintf(
		"curl -fsSL %s | sudo sh -s -- --controller %s --id %s --token %s --signing-key %s",
		shellQuote(installScriptURL(controllerURL)),
		shellQuote(controllerURL),
		shellQuote(nodeID),
		shellQuote(token),
		shellQuote(signingKey),
	)
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
