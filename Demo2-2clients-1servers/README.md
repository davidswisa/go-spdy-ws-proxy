# Demo2: 2 clients / 1 server (hybrid SPDY + WebSocket)

## Use case

Run **one** server that can accept **either**:

- SPDY remotecommand streams (SPDY upgrade + `X-Stream-Protocol-Version` negotiation)
- WebSocket remotecommand streams (WebSocket upgrade + `Sec-WebSocket-Protocol` negotiation)

This mirrors a “dual-stack” endpoint that supports both transports.

## What’s in this folder

- `server3/`: hybrid server (listens on `:8080`, path `/spdy`)
- `client/`: SPDY remotecommand client
- `client2/`: WebSocket remotecommand client

The server performs a minimal echo:

- reads from **stdin channel** and writes to **stdout**

## How to run

1) Start the hybrid server:

```bash
go run ./Demo2-2clients-1servers/server3/server3.go
```

2) In another terminal, run the SPDY client:

```bash
go run ./Demo2-2clients-1servers/client/client.go
```

3) Or run the WebSocket client:

```bash
go run ./Demo2-2clients-1servers/client2/client2.go
```

Type some input, then press `Ctrl+D` to end stdin and let the stream finish.

## Troubleshooting

- `unable to upgrade connection: 404 page not found` usually means the server path isn’t `/spdy`.
- `invalid protocol ... got ""` usually means the request wasn’t a real WebSocket upgrade or the subprotocol wasn’t negotiated (use the included client2 and ensure the server is running).
