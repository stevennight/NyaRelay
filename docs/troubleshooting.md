# Troubleshooting

## Node is offline while existing forwards still work

### Symptoms

- The controller marks one node as `offline`.
- Forwarding rules that were already loaded still work.
- Editing a tunnel or forward does not produce a new `config applied` log on the node.
- A direct HTTP request to `/api/node/config` returns `200 OK`.
- The node log shows a successful WebSocket connection followed by a read failure.

### Log levels

The default `NYARELAY_LOG_LEVEL=info` is intended to be sufficient for normal operation checks. Control-plane failures are visible without enabling debug logging:

- `INFO`: controller/node startup, node online or offline transitions, WebSocket recovery, and successful configuration application.
- `WARN`: WebSocket connect, hello, heartbeat, read, configuration apply, configuration push, or update push failures. This includes `websocket: message too big`.
- `ERROR`: persistent failures writing node state or applying a configuration rollback.
- `DEBUG`: normal WebSocket close, periodic metrics details, UDP per-packet failures, and other high-frequency data-plane details.

Use `NYARELAY_LOG_LEVEL=debug` only when the warning context is not enough; it is not required to see a node control connection failure.

### Check the node log first

Look for this sequence in the node service log:

```text
control websocket connected
control websocket read failed error="failed to read JSON message: failed to read: websocket: message too big: read limited at 32769 bytes"
```

The `101 Switching Protocols` response only proves that the WebSocket upgrade succeeded. It does not prove that the node can read the first configuration message.

The `github.com/coder/websocket` client defaults to a 32 KiB limit for one WebSocket message. The controller sends the complete signed, node-scoped configuration as one JSON message. For example, an HTTP response body of `34706` bytes is already larger than the default limit, and the WebSocket envelope adds a little more data.

When this happens, the node can apply its initial configuration from HTTP, but it closes the control WebSocket when the controller sends the configuration. The controller then sees the connection disappear and later edits cannot be pushed over that connection.

### Confirm the configuration size

On the node, load its environment and measure the HTTP configuration response:

```bash
set -a
. /etc/nyarelay/node.env
set +a
curl -sS \
  -H "X-NyaRelay-Node-ID: $NYARELAY_NODE_ID" \
  -H "X-NyaRelay-Node-Token: $NYARELAY_NODE_TOKEN" \
  "$NYARELAY_CONTROLLER/api/node/config" | wc -c
```

This checks the HTTP bootstrap path only. A successful result does not rule out a WebSocket message-size failure.

### Why only one node is affected

The controller compiles configuration per node. Every response includes the active node list, but tunnels and forwards are included only when that node participates in them. A node used as a tunnel entry, middle stage, exit, and local forward host can therefore have a much larger configuration than other nodes.

### Resolution

The node control WebSocket read limit is set to `256 KiB`. This keeps a bounded memory limit while providing roughly seven times the capacity needed by the reported `34706`-byte configuration. Config chunking is not required for the current scale; if a configuration approaches `256 KiB`, measure the node-scoped payload and reassess before adding more relay roles.

After installing a node binary containing this fix:

1. Restart the node service.
2. Confirm `control websocket connected` remains without the `message too big` error.
3. Edit a small forward and confirm the node logs a new `config applied source=ws` entry.
4. Confirm the controller changes the node status back to online.

Restarting an older binary can reload the current snapshot through HTTP, so existing forwarding may appear healthy again, but it does not fix the WebSocket push path or the controller status.

### Unrelated hostname warning

```text
sudo: unable to resolve host <hostname>
```

This indicates that the local hostname is missing or mismatched in `/etc/hosts` and can be fixed separately. It is not the cause of the WebSocket `message too big` failure.
