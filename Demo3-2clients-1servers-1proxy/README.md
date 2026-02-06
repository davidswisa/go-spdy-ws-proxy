# Demo3: 2 clients / 1 proxy / 1 backend server

## Use case

Demonstrate a **proxy** that accepts remotecommand streams from clients over **either**:

- SPDY
- WebSocket

…and forwards them to a **backend** server that speaks **WebSocket remotecommand**.

This is useful for experimenting with protocol bridging / front-door proxies.

## What’s in this folder

- `server3/`: the **front-door proxy** (listens on `:8080`, path `/spdy`)
  - accepts SPDY or WebSocket from clients
  - dials backend at `ws://localhost:8081/spdy`
  - forwards channel frames both directions
- `proxy/`: the **backend WebSocket server** (listens on `:8081`, path `/spdy`)
  - minimal echo: stdin -> stdout
- `client/`: SPDY remotecommand client (connects to `http://localhost:8080/spdy`)
- `client2/`: WebSocket remotecommand client (connects to `http://localhost:8080/spdy`)

## How to run

1) Start the backend WebSocket server (port 8081):

```bash
go run ./Demo3-2clients-1servers-1proxy/backend/backend.go
```

2) Start the front-door proxy (port 8080):

```bash
go run ./Demo3-2clients-1servers-1proxy/server3/server3.go
```

3) Run either client against the proxy:

SPDY client:

```bash
go run ./Demo3-2clients-1servers-1proxy/client/client.go
```

WebSocket client:

```bash
go run ./Demo3-2clients-1servers-1proxy/client2/client2.go
```

Type some input and then `Ctrl+D`.

## Troubleshooting

- If the proxy logs `backend dial error`, ensure the backend (`:8081`) is running.
- If you get `404 page not found`, make sure you’re using the `/spdy` path.
- If you see a WebSocket subprotocol error, verify the backend is negotiating `v5.channel.k8s.io` (or another supported version).
