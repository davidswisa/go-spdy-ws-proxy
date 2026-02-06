# spdy-example

Small Go demos showing Kubernetes-style **remotecommand** streaming over:

- **SPDY** (HTTP Upgrade + `X-Stream-Protocol-Version` negotiation)
- **WebSocket** (WebSocket Upgrade + `Sec-WebSocket-Protocol` negotiation)

The repository contains three progressively more complex demos.

## Prereqs

- Go (1.20+ should be fine)
- Linux/macOS recommended (works on Windows too)

## Demos

- **Demo1**: Two standalone servers (one SPDY, one WebSocket) and matching clients.
  - See `Demo1-2clients-2servers/README.md`

- **Demo2**: One hybrid server that accepts **either** SPDY or WebSocket on the same `/spdy` endpoint.
  - See `Demo2-2clients-1servers/README.md`

- **Demo3**: A front-door proxy that accepts SPDY or WebSocket from clients and forwards frames to a backend WebSocket server.
  - See `Demo3-2clients-1servers-1proxy/README.md`

## Quickstart

Pick a demo and run the commands from its README.

Example (Demo1):

```bash
# Terminal A
go run ./Demo1-2clients-2servers/server/server.go

# Terminal B
go run ./Demo1-2clients-2servers/server2/server2.go

# Terminal C
go run ./Demo1-2clients-2servers/client/client.go

# Terminal D
go run ./Demo1-2clients-2servers/client2/client2.go
```

Type some input and then press `Ctrl+D` to end stdin and let the stream finish.

## Common errors

- **404 page not found**: you’re hitting the wrong path (these demos use `/spdy`).
- **bind: address already in use**: another process is already listening on that port (commonly `8080`/`8081`).
- **invalid protocol ... got ""** (WebSocket): the request wasn’t a proper WebSocket upgrade or no subprotocol was negotiated (use the provided WebSocket client and ensure the server supports `v5.channel.k8s.io`).

## Notes

- These demos are intentionally minimal and not hardened.
- Some handlers allow all origins for WebSocket upgrades; don’t copy that into production.
