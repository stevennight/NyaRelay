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
```

Run node:

```powershell
go run ./cmd/node --controller http://127.0.0.1:8080 --id <node-id> --token <node-token> --signing-key <controller-public-key>
```

The node caches the last valid signed configuration under its data directory. Existing routes continue to run when the controller is temporarily unavailable.

## Deployment

Controller:

```bash
docker compose -f deploy/docker/docker-compose.yml up -d --build
```

Node:

```bash
install -m 755 nyarelay-node /usr/local/bin/nyarelay-node
install -d -m 700 /etc/nyarelay /var/lib/nyarelay
cp deploy/systemd/node.env.example /etc/nyarelay/node.env
systemctl enable --now nyarelay-node
```

## Database Choice

SQLite is the default store. It is enough for a personal relay panel when metrics are aggregated before writes. The store package is isolated so a future Postgres driver can be added without changing route or node logic.

Postgres is intentionally not required for the first version. Keeping the controller as one container plus one persistent volume reduces deployment work and removes a database service from the public-facing stack.

## Security Notes

- Do not mount `/var/run/docker.sock` into the controller container.
- Put the controller behind Caddy HTTPS.
- Use TOTP after initial setup.
- Prefer `mtls` for node-to-node links and `ws-tls` when you need HTTPS/Caddy-friendly transport.
- Routes are transparent payload forwarding; NyaRelay does not parse VLESS, Shadowsocks, Trojan, or application protocols.
