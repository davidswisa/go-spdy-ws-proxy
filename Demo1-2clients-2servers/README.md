# Demo1: 2 clients / 2 servers (SPDY vs WebSocket)

## Use case

Compare Kubernetes-style remotecommand streaming over:

- **SPDY** (like `kubectl exec` historically used)
- **WebSocket** (newer transport)

Each client talks to its matching server.

## What’s in this folder

- `server/`: SPDY remotecommand server (listens on `:8080`, path `/spdy`)
- `client/`: SPDY remotecommand client (connects to `http://localhost:8080/spdy`)
- `server2/`: WebSocket remotecommand server (listens on `:8081`, path `/spdy`)
- `client2/`: WebSocket remotecommand client (connects to `http://localhost:8081/spdy`)

Both servers implement a minimal “echo” behavior: bytes you type on stdin are streamed back on stdout.

## How to run

In separate terminals:

1) Start the SPDY server:

```bash
go run ./Demo1-2clients-2servers/server/server.go
```

2) Start the WebSocket server:

```bash
go run ./Demo1-2clients-2servers/server2/server2.go
```

3) Run the SPDY client (type something, then Ctrl+D to end stdin):

```bash
go run ./Demo1-2clients-2servers/client/client.go
```

4) Run the WebSocket client (type something, then Ctrl+D):

```bash
go run ./Demo1-2clients-2servers/client2/client2.go
```

## Troubleshooting

- If you see `bind: address already in use`, something is already listening on `8080`/`8081`.
- If you see `404 page not found`, make sure the URL path is `/spdy`.
- If you see a protocol error, ensure you’re using the matching client for the server/port.
