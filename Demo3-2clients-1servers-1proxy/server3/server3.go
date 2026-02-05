package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

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

// protocolHandler is a tiny interface so we can route to SPDY vs WebSocket via a factory.
type protocolHandler interface {
	Name() string
	CanHandle(*http.Request) bool
	ServeHTTP(http.ResponseWriter, *http.Request)
}

type handlerFactory struct {
	handlers []protocolHandler
}

func (f handlerFactory) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, h := range f.handlers {
		if h.CanHandle(r) {
			log.Printf("exec request: protocol=%s method=%s path=%s", h.Name(), r.Method, r.URL.Path)
			h.ServeHTTP(w, r)
			return
		}
	}
	http.Error(w, "no handler for request", http.StatusBadRequest)
}

func newHybridExecHandler() http.Handler {
	return handlerFactory{handlers: []protocolHandler{
		wsRemotecommandHandler{},
		spdyRemotecommandHandler{},
	}}
}

// -----------------------------
// WebSocket (remotecommand.NewWebSocketExecutor)
// -----------------------------

type wsRemotecommandHandler struct{}

func (wsRemotecommandHandler) Name() string { return "websocket" }

func (wsRemotecommandHandler) CanHandle(r *http.Request) bool {
	return wsstream.IsWebSocketRequest(r)
}

func (wsRemotecommandHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		log.Printf("websocket open error: %v", err)
		return
	}
	log.Printf("websocket connection established (subprotocol=%s)", negotiated)

	// rwc is indexed by channel.
	stdin := rwc[remotecommand.StreamStdIn]
	stdout := rwc[remotecommand.StreamStdOut]
	stderr := rwc[remotecommand.StreamStdErr]
	errStream := rwc[remotecommand.StreamErr]

	// For this demo:
	// - Echo stdin -> stdout.
	// - Never write stderr.
	// - Close error stream when stdin closes (signals success).
	_ = stderr

	if _, err := io.Copy(stdout, stdin); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("websocket copy error: %v", err)
		// best-effort error reporting; client may ignore.
		_, _ = errStream.Write([]byte(fmt.Sprintf("copy error: %v", err)))
	}

	_ = stdout.Close()
	_ = errStream.Close()
	_ = conn.Close()
}

// -----------------------------
// SPDY (remotecommand.NewSPDYExecutor)
// -----------------------------

type spdyRemotecommandHandler struct{}

func (spdyRemotecommandHandler) Name() string { return "spdy" }

func (spdyRemotecommandHandler) CanHandle(r *http.Request) bool {
	// Anything not WebSocket falls back to SPDY for this demo.
	return true
}

func (spdyRemotecommandHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serverProtocols := []string{
		remotecommand.StreamProtocolV5Name,
		remotecommand.StreamProtocolV4Name,
		remotecommand.StreamProtocolV3Name,
		remotecommand.StreamProtocolV2Name,
		remotecommand.StreamProtocolV1Name,
	}
	negotiated, err := httpstream.Handshake(r, w, serverProtocols)
	if err != nil {
		log.Printf("spdy handshake error: %v", err)
		return
	}
	log.Printf("spdy handshake negotiated protocol=%s", negotiated)

	upgrader := spdy.NewResponseUpgrader()
	session := newSPDYSession()

	conn := upgrader.UpgradeResponse(w, r, session.onNewStream)
	defer conn.Close()
	log.Printf("spdy connection established")

	<-conn.CloseChan()
	log.Printf("spdy connection closed")
}

type spdySession struct {
	mu      sync.Mutex
	streams map[string]httpstream.Stream
	started bool
}

func newSPDYSession() *spdySession {
	return &spdySession{streams: make(map[string]httpstream.Stream)}
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
	canStart := !s.started && s.streams[corev1.StreamTypeError] != nil && s.streams[corev1.StreamTypeStdout] != nil
	// If the client requested stdin, wait for it so we don't wedge by reading too early.
	if !s.started && headers.Get(corev1.StreamType) == corev1.StreamTypeStdin {
		// no-op; stdin presence is handled by the generic readiness check below
	}
	// If stdin is expected, ensure it's present before starting.
	if canStart {
		if s.streams[corev1.StreamTypeStdin] == nil {
			canStart = false
		}
	}
	if canStart {
		s.started = true
		stdin := s.streams[corev1.StreamTypeStdin]
		stdout := s.streams[corev1.StreamTypeStdout]
		errStream := s.streams[corev1.StreamTypeError]
		s.mu.Unlock()

		go s.runEcho(stdin, stdout, errStream)
		return nil
	}
	s.mu.Unlock()

	return nil
}

func (s *spdySession) runEcho(stdin, stdout, errStream httpstream.Stream) {
	defer func() {
		_ = stdout.Close()
		_ = errStream.Close()
		_ = stdin.Close()
	}()

	if _, err := io.Copy(stdout, stdin); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("spdy copy error: %v", err)
		_, _ = errStream.Write([]byte(fmt.Sprintf("copy error: %v", err)))
	}
}
