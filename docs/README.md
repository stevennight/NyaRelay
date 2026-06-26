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
