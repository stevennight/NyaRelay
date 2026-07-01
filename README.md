# NyaRelay

NyaRelay is a private multi-hop relay panel for personal VPS fleets. It is designed around a small controller and a single node service that contains both the agent and relay runtime.

## Architecture

```text
Caddy HTTPS
  -> controller Docker container
     -> SQLite volume

node VPS
  -> nyarelay-node systemd service
     -> agent pulls signed config
     -> relay forwards TCP/UDP payloads
```

The controller never stores SSH keys, never mounts the Docker socket, and never sends shell commands to nodes. Nodes connect outward to the controller and only apply signed structured route configuration.

## Route Shapes

- Single-node direct in/direct out: `client -> entry node -> target`
- Multi-hop: `client -> entry node -> link chain -> target`
- Transparent protocol payloads: VLESS, Shadowsocks, Trojan, HTTPS, SSH, databases, and other TCP/UDP payloads are forwarded without parsing.

## Link Types

- `direct`: raw TCP link between nodes.
- `tls`: TLS-encrypted node link.
- `mtls`: mutual TLS node link. The source node receives the client certificate, and the target node receives the server certificate.
- `ws-tls`: TLS with HTTP Upgrade, intended for Caddy/HTTPS-style cross-border links.

## Development

```powershell
$env:GOCACHE="D:\Projects\Self\Ops\NyaRelay\.gocache"
go test ./...
```

Build the web UI:

```powershell
cd web/app
npm install
npm run build
```

The build writes to `.tmp-webdist` at the repo root. The controller serves that directory at runtime when it is present, and the Docker image copies it into `/app/.tmp-webdist`.

Run controller:

```powershell
go run ./cmd/controller --listen :8080 --data ./data
go run ./cmd/controller --version
```

Run node:

```powershell
go run ./cmd/node --controller http://127.0.0.1:8080 --id <node-id> --token <node-token> --signing-key <controller-public-key>
go run ./cmd/node --version
```

The node caches the last valid signed configuration under its data directory. Existing routes continue to run when the controller is temporarily unavailable.

## Deployment

Recommended controller deployment uses the versioned Docker image published by GitHub Actions.

```bash
git clone https://github.com/<owner>/NyaRelay.git
cd NyaRelay
cp deploy/docker/.env.example deploy/docker/.env
```

Edit `deploy/docker/.env` on each server:

```dotenv
NYARELAY_IMAGE=ghcr.io/<owner>/nyarelay
NYARELAY_VERSION=v0.1.3
NYARELAY_PUBLIC_URL=https://relay.example.com
NYARELAY_BIND_ADDR=127.0.0.1
NYARELAY_HOST_PORT=8080
```

Run or update the controller:

```bash
docker compose --env-file deploy/docker/.env -f deploy/docker/docker-compose.yml pull
docker compose --env-file deploy/docker/.env -f deploy/docker/docker-compose.yml up -d
```

If the GHCR package is private, run `docker login ghcr.io` on the server first with a token that can read packages.

`deploy/docker/.env` stays local; the repository keeps only `deploy/docker/.env.example`. Server-specific values such as public URL, bind address, host port, image name, and version belong in `.env`, not in the committed compose file.

For local source builds, add the build override:

```bash
docker compose --env-file deploy/docker/.env -f deploy/docker/docker-compose.yml -f deploy/docker/docker-compose.build.yml up -d --build
```

Release a version by pushing a tag such as `v0.1.3`. The release workflow publishes `ghcr.io/<owner>/nyarelay:v0.1.3` and Linux node binaries for amd64 and arm64. Deploying a version is just changing `NYARELAY_VERSION` in the server `.env` and running the compose commands above.

The release workflow should be configured with these repository secrets when node update approval is needed from the panel:

```text
NYARELAY_UPDATE_SIGNING_KEY=<base64url Ed25519 private key>
NYARELAY_UPDATE_PUBLIC_KEY=<matching base64url Ed25519 public key>
```

Node installation is one command from the panel. Create a node, copy the generated install command, and run it on the node machine as a sudo-capable user. The script downloads itself from the controller, detects the node machine architecture, downloads the matching `nyarelay-node` binary from `/downloads/nyarelay-node` with gzip compression, prints each install stage, writes `/etc/nyarelay/node.env`, installs the systemd unit, and starts `nyarelay-node`. You do not need to manually download or copy the node binary.

Node binary updates are panel-approved and controller-bundled. The controller only offers the node binaries packaged inside its own image, with a signed release manifest and SHA-256 digest for each target. A node verifies the Ed25519 manifest signature against its embedded trusted update public key, downloads the matching gzip binary from the controller, checks the digest, replaces `/usr/local/bin/nyarelay-node`, and restarts the systemd service. Local or development images without the update signing key still run normally, but node auto update is shown as disabled.

## Database Choice

SQLite is the default store. It is enough for a personal relay panel when metrics are aggregated before writes. The store package is isolated so a future Postgres driver can be added without changing route or node logic.

Postgres is intentionally not required for the first version. Keeping the controller as one container plus one persistent volume reduces deployment work and removes a database service from the public-facing stack.

## Security Notes

- Do not mount `/var/run/docker.sock` into the controller container.
- Put the controller behind Caddy HTTPS.
- Use TOTP after initial setup.
- Prefer `mtls` for node-to-node links and `ws-tls` when you need HTTPS/Caddy-friendly transport.
- Routes are transparent payload forwarding; NyaRelay does not parse VLESS, Shadowsocks, Trojan, or application protocols.
