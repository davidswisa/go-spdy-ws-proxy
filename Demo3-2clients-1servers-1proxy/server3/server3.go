package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/httpstream/spdy"
	"k8s.io/apimachinery/pkg/util/httpstream/wsstream"
	"k8s.io/apimachinery/pkg/util/remotecommand"
)

func main() {
	http.Handle("/spdy", newHybridExecHandler())

	log.Println("Hybrid remotecommand server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// protocolHandler represents an upgraded connection (SPDY or WebSocket)
// with a small common API.
//
// ReadMessage returns the next chunk of data from a logical inbound channel.
// For this demo we primarily read from STDIN (channel 0).
type protocolHandler interface {
	Name() string
	ReadMessage() (channel byte, payload []byte, err error)
	WriteMessage(channel byte, payload []byte) error
	Close() error
}

type protocolUpgrader interface {
	Name() string
	CanHandle(*http.Request) bool
	Upgrade(http.ResponseWriter, *http.Request) (protocolHandler, error)
}

type handlerFactory struct {
	upgraders []protocolUpgrader
}

func (f handlerFactory) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, u := range f.upgraders {
		if !u.CanHandle(r) {
			continue
		}
		log.Printf("exec request: upgrader=%s method=%s path=%s", u.Name(), r.Method, r.URL.Path)

		conn, err := u.Upgrade(w, r)
		if err != nil {
			log.Printf("upgrade error (%s): %v", u.Name(), err)
			return
		}
		defer conn.Close()

		serveProxy(conn)
		return
	}

	http.Error(w, "no protocol upgrader matched request", http.StatusBadRequest)
}

func newHybridExecHandler() http.Handler {
	return handlerFactory{upgraders: []protocolUpgrader{
		wsRemotecommandUpgrader{},
		spdyRemotecommandUpgrader{},
	}}
}

func serveProxy(client protocolHandler) {
	backend, err := dialBackendWS("ws://localhost:8081/spdy")
	if err != nil {
		log.Printf("backend dial error: %v", err)
		_ = client.WriteMessage(remotecommand.StreamErr, []byte(fmt.Sprintf("backend dial error: %v", err)))
		return
	}
	defer backend.Close()

	errCh := make(chan error, 2)
	closeBoth := sync.OnceFunc(func() {
		_ = backend.Close()
		_ = client.Close()
	})

	// client -> backend
	go func() {
		defer closeBoth()
		for {
			ch, payload, err := client.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if len(payload) == 0 {
				continue
			}
			if err := backend.WriteMessage(ch, payload); err != nil {
				errCh <- err
				return
			}
		}
	}()

	// backend -> client
	go func() {
		defer closeBoth()
		for {
			ch, payload, err := backend.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if len(payload) == 0 {
				continue
			}
			if err := client.WriteMessage(ch, payload); err != nil {
				errCh <- err
				return
			}
		}
	}()

	err = <-errCh
	if err == nil || errors.Is(err, io.EOF) {
		return
	}
	log.Printf("proxy stream ended with error: %v", err)
}

type backendWSHandler struct {
	c                   *websocket.Conn
	supportsCloseSignal bool
}

func (h *backendWSHandler) Name() string { return "backend-websocket" }

func (h *backendWSHandler) ReadMessage() (byte, []byte, error) {
	for {
		mt, data, err := h.c.ReadMessage()
		if err != nil {
			return 0, nil, err
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		if len(data) == 0 {
			continue
		}
		if h.supportsCloseSignal && data[0] == remotecommand.StreamClose {
			// Close signal: [255, channel]
			if len(data) != 2 {
				continue
			}
			// Treat backend half-close as EOF from that stream.
			return data[1], nil, io.EOF
		}
		ch := data[0]
		payload := make([]byte, len(data)-1)
		copy(payload, data[1:])
		return ch, payload, nil
	}
}

func (h *backendWSHandler) WriteMessage(channel byte, payload []byte) error {
	frame := make([]byte, 1+len(payload))
	frame[0] = channel
	copy(frame[1:], payload)
	return h.c.WriteMessage(websocket.BinaryMessage, frame)
}

func (h *backendWSHandler) Close() error {
	if h.c != nil {
		return h.c.Close()
	}
	return nil
}

func dialBackendWS(rawURL string) (protocolHandler, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	d := websocket.Dialer{
		Proxy: http.ProxyFromEnvironment,
		Subprotocols: []string{
			remotecommand.StreamProtocolV5Name,
			remotecommand.StreamProtocolV4Name,
			remotecommand.StreamProtocolV3Name,
			remotecommand.StreamProtocolV2Name,
			remotecommand.StreamProtocolV1Name,
		},
	}

	c, _, err := d.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}

	negotiated := c.Subprotocol()
	if negotiated == "" {
		_ = c.Close()
		return nil, fmt.Errorf("backend did not negotiate a subprotocol")
	}

	log.Printf("connected to backend ws %s (subprotocol=%s)", u.String(), negotiated)
	return &backendWSHandler{c: c, supportsCloseSignal: negotiated == remotecommand.StreamProtocolV5Name}, nil
}

// -----------------------------
// WebSocket (remotecommand.NewWebSocketExecutor)
// -----------------------------

type wsRemotecommandUpgrader struct{}

func (wsRemotecommandUpgrader) Name() string { return "websocket" }

func (wsRemotecommandUpgrader) CanHandle(r *http.Request) bool {
	return wsstream.IsWebSocketRequest(r)
}

func (wsRemotecommandUpgrader) Upgrade(w http.ResponseWriter, r *http.Request) (protocolHandler, error) {
	protocols := map[string]wsstream.ChannelProtocolConfig{}
	channels := []wsstream.ChannelType{
		wsstream.ReadChannel,   // 0 stdin
		wsstream.WriteChannel,  // 1 stdout
		wsstream.WriteChannel,  // 2 stderr
		wsstream.WriteChannel,  // 3 error
		wsstream.IgnoreChannel, // 4 resize (ignore for this demo)
	}
	cfg := wsstream.ChannelProtocolConfig{Binary: true, Channels: channels}
	protocols[remotecommand.StreamProtocolV5Name] = cfg
	protocols[remotecommand.StreamProtocolV4Name] = cfg
	protocols[remotecommand.StreamProtocolV3Name] = cfg
	protocols[remotecommand.StreamProtocolV2Name] = cfg
	protocols[remotecommand.StreamProtocolV1Name] = cfg

	conn := wsstream.NewConn(protocols)
	conn.SetIdleTimeout(5 * time.Minute)

	negotiated, rwc, err := conn.Open(w, r)
	if err != nil {
		return nil, err
	}
	log.Printf("websocket connection established (subprotocol=%s)", negotiated)

	return &wsRemotecommandHandler{
		conn:      conn,
		stdin:     rwc[remotecommand.StreamStdIn],
		stdout:    rwc[remotecommand.StreamStdOut],
		stderr:    rwc[remotecommand.StreamStdErr],
		errStream: rwc[remotecommand.StreamErr],
	}, nil
}

type wsRemotecommandHandler struct {
	conn      *wsstream.Conn
	stdin     io.ReadWriteCloser
	stdout    io.ReadWriteCloser
	stderr    io.ReadWriteCloser
	errStream io.ReadWriteCloser
}

func (*wsRemotecommandHandler) Name() string { return "websocket" }

func (h *wsRemotecommandHandler) ReadMessage() (byte, []byte, error) {
	buf := make([]byte, 32*1024)
	n, err := h.stdin.Read(buf)
	if n > 0 {
		payload := make([]byte, n)
		copy(payload, buf[:n])
		return remotecommand.StreamStdIn, payload, nil
	}
	return remotecommand.StreamStdIn, nil, err
}

func (h *wsRemotecommandHandler) WriteMessage(channel byte, payload []byte) error {
	switch channel {
	case remotecommand.StreamStdOut:
		_, err := h.stdout.Write(payload)
		return err
	case remotecommand.StreamStdErr:
		_, err := h.stderr.Write(payload)
		return err
	case remotecommand.StreamErr:
		_, err := h.errStream.Write(payload)
		return err
	default:
		return nil
	}
}

func (h *wsRemotecommandHandler) Close() error {
	// Close underlying conn; this will close channels.
	if h.conn != nil {
		return h.conn.Close()
	}
	return nil
}

// -----------------------------
// SPDY (remotecommand.NewSPDYExecutor)
// -----------------------------

type spdyRemotecommandUpgrader struct{}

func (spdyRemotecommandUpgrader) Name() string { return "spdy" }

func (spdyRemotecommandUpgrader) CanHandle(r *http.Request) bool {
	// Anything not WebSocket falls back to SPDY.
	return true
}

func (spdyRemotecommandUpgrader) Upgrade(w http.ResponseWriter, r *http.Request) (protocolHandler, error) {
	serverProtocols := []string{
		remotecommand.StreamProtocolV5Name,
		remotecommand.StreamProtocolV4Name,
		remotecommand.StreamProtocolV3Name,
		remotecommand.StreamProtocolV2Name,
		remotecommand.StreamProtocolV1Name,
	}
	negotiated, err := httpstream.Handshake(r, w, serverProtocols)
	if err != nil {
		return nil, err
	}
	log.Printf("spdy handshake negotiated protocol=%s", negotiated)

	upgrader := spdy.NewResponseUpgrader()
	session := newSPDYSession()

	conn := upgrader.UpgradeResponse(w, r, session.onNewStream)
	log.Printf("spdy connection established")

	select {
	case <-session.ready:
		stdin, stdout, stderr, errStream := session.streamsForIO()
		return &spdyRemotecommandHandler{
			conn:      conn,
			stdin:     stdin,
			stdout:    stdout,
			stderr:    stderr,
			errStream: errStream,
		}, nil
	case <-time.After(30 * time.Second):
		_ = conn.Close()
		return nil, fmt.Errorf("timed out waiting for spdy streams")
	}
}

type spdyRemotecommandHandler struct {
	conn      httpstream.Connection
	stdin     httpstream.Stream
	stdout    httpstream.Stream
	stderr    httpstream.Stream
	errStream httpstream.Stream
}

func (*spdyRemotecommandHandler) Name() string { return "spdy" }

func (h *spdyRemotecommandHandler) ReadMessage() (byte, []byte, error) {
	buf := make([]byte, 32*1024)
	n, err := h.stdin.Read(buf)
	if n > 0 {
		payload := make([]byte, n)
		copy(payload, buf[:n])
		return remotecommand.StreamStdIn, payload, nil
	}
	return remotecommand.StreamStdIn, nil, err
}

func (h *spdyRemotecommandHandler) WriteMessage(channel byte, payload []byte) error {
	switch channel {
	case remotecommand.StreamStdOut:
		_, err := h.stdout.Write(payload)
		return err
	case remotecommand.StreamStdErr:
		if h.stderr == nil {
			return nil
		}
		_, err := h.stderr.Write(payload)
		return err
	case remotecommand.StreamErr:
		_, err := h.errStream.Write(payload)
		return err
	default:
		return nil
	}
}

func (h *spdyRemotecommandHandler) Close() error {
	if h.conn != nil {
		return h.conn.Close()
	}
	return nil
}

type spdySession struct {
	mu      sync.Mutex
	streams map[string]httpstream.Stream
	started bool
	ready   chan struct{}
}

func newSPDYSession() *spdySession {
	return &spdySession{streams: make(map[string]httpstream.Stream), ready: make(chan struct{})}
}

func (s *spdySession) onNewStream(stream httpstream.Stream, replySent <-chan struct{}) error {
	_ = replySent // not needed for this demo

	headers := stream.Headers()
	streamType := headers.Get(corev1.StreamType)
	if streamType == "" {
		log.Printf("spdy: stream missing %q header: %v", corev1.StreamType, headers)
		return fmt.Errorf("missing stream type")
	}

	s.mu.Lock()
	s.streams[streamType] = stream
	if !s.started && s.streams[corev1.StreamTypeError] != nil && s.streams[corev1.StreamTypeStdout] != nil && s.streams[corev1.StreamTypeStdin] != nil {
		s.started = true
		select {
		case <-s.ready:
			// already closed
		default:
			close(s.ready)
		}
	}
	s.mu.Unlock()

	return nil
}

func (s *spdySession) streamsForIO() (stdin, stdout, stderr, errStream httpstream.Stream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stdin = s.streams[corev1.StreamTypeStdin]
	stdout = s.streams[corev1.StreamTypeStdout]
	stderr = s.streams[corev1.StreamTypeStderr]
	errStream = s.streams[corev1.StreamTypeError]
	return
}
