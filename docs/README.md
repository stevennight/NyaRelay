# Docs

## Runtime

- `controller`: Docker container behind Caddy, stores state in SQLite, serves the admin UI.
- `node`: systemd service on each relay VPS, runs the agent and relay runtime together.

## Link Types

- `direct`: raw node-to-node forwarding.
- `tls`: TLS-encrypted forwarding.
- `mtls`: mutual TLS forwarding.
- `ws-tls`: WebSocket over TLS for Caddy-friendly cross-border links.

## Frontend

The admin UI is a multi-route React app with TanStack Router and TanStack Query.

Main routes:

- `/dashboard`
- `/nodes`
- `/nodes/new`
- `/nodes/:nodeId`
- `/links`
- `/links/new`
- `/links/:linkId`
- `/routes`
- `/routes/new`
- `/routes/:routeId`
- `/traffic`
- `/audit`
- `/settings/security`
- `/settings/controller`

## Build

- `cd web/app && npm run build` writes to `.tmp-webdist` at the repo root.
- The controller serves `.tmp-webdist` when present.
- The Docker image copies `.tmp-webdist` into `/app/.tmp-webdist`.

## History Retention

The controller stores configuration, audit events, and traffic metrics in `/data/nyarelay.db`. It prunes old history periodically by default:

- metrics: 7 days (`NYARELAY_METRICS_RETENTION=168h`)
- audit events: 90 days (`NYARELAY_AUDIT_RETENTION=2160h`)
- cleanup interval: 1 hour (`NYARELAY_CLEANUP_INTERVAL=1h`)

Durations use Go duration syntax. Set an individual retention to `0s` to disable that cleanup, or set the cleanup interval to `0s` to disable the cleanup loop entirely. After deleting rows, the controller checkpoints the SQLite WAL so the `-wal` file does not grow indefinitely.

These values can also be changed from Settings > Controller. Saved controller settings take precedence over environment defaults, are persisted in `/data/nyarelay.db`, and take effect immediately without restarting the controller.

Relay candidate failure cooldown defaults to 5 seconds (NYARELAY_FAILURE_COOLDOWN=5s). It controls how long a failed multi-candidate tunnel node or forward target is skipped before being retried. The value can also be changed from Settings > Controller; it is persisted and pushed to connected nodes without a restart. Values use whole-second Go duration syntax and must be greater than zero.

On first startup after this migration, Forward and Tunnel stage rules with exactly one enabled candidate are converted to single. Rules with multiple candidates keep their existing strategies.
