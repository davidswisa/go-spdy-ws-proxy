package main

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"k8s.io/apimachinery/pkg/util/remotecommand"
)

var upgrader = websocket.Upgrader{
	Subprotocols: []string{
		remotecommand.StreamProtocolV5Name,
		remotecommand.StreamProtocolV4Name,
		remotecommand.StreamProtocolV3Name,
		remotecommand.StreamProtocolV2Name,
		remotecommand.StreamProtocolV1Name,
	},
	CheckOrigin: func(r *http.Request) bool {
		return true // DO NOT DO THIS IN PROD
	},
}

func main() {
	http.HandleFunc("/spdy", execHandler)

	log.Println("WebSocket remotecommand server listening on :8081")
	log.Fatal(http.ListenAndServe(":8081", nil))
}

func execHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade error: %v", err)
		return
	}
	defer conn.Close()

	negotiated := conn.Subprotocol()
	if negotiated == "" {
		log.Printf("websocket connection established but no subprotocol was negotiated")
		return
	}

	supportsCloseSignal := negotiated == remotecommand.StreamProtocolV5Name
	log.Printf("WebSocket connection established (subprotocol=%s)", negotiated)

	writer := &wsChannelWriter{conn: conn}

	// Minimal demo implementation:
	// - Read STDIN (channel 0) from client.
	// - Echo to STDOUT (channel 1).
	// - When client closes STDIN (v5 CLOSE signal), close output channels and return.
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("read error: %v", err)
			return
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		if len(data) == 0 {
			continue
		}

		if supportsCloseSignal && data[0] == remotecommand.StreamClose {
			if len(data) != 2 {
				log.Printf("invalid CLOSE signal length: %d", len(data))
				continue
			}
			ch := data[1]
			log.Printf("client closed channel %d", ch)
			if ch == remotecommand.StreamStdIn {
				_ = writer.sendClose(remotecommand.StreamStdOut)
				_ = writer.sendClose(remotecommand.StreamStdErr)
				_ = writer.sendClose(remotecommand.StreamErr)
				return
			}
			continue
		}

		channel := data[0]
		payload := data[1:]

		switch channel {
		case remotecommand.StreamStdIn:
			if len(payload) == 0 {
				continue
			}
			log.Printf("stdin: %q", string(payload))
			out := append([]byte("server received: "), payload...)
			if err := writer.send(remotecommand.StreamStdOut, out); err != nil {
				log.Printf("write stdout error: %v", err)
				return
			}
		case remotecommand.StreamResize:
			// Ignored for this example (TTY=false in the client by default).
			log.Printf("resize event (%d bytes)", len(payload))
		default:
			log.Printf("unexpected inbound channel=%d (%d bytes)", channel, len(payload))
		}
	}
}

type wsChannelWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *wsChannelWriter) send(channel byte, payload []byte) error {
	buf := make([]byte, 1+len(payload))
	buf[0] = channel
	copy(buf[1:], payload)

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.BinaryMessage, buf)
}

func (w *wsChannelWriter) sendClose(channel byte) error {
	buf := []byte{remotecommand.StreamClose, channel}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.BinaryMessage, buf)
}
