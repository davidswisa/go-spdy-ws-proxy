package main

import (
	"io"
	"log"
	"net/http"

	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/httpstream/spdy"
)

func main() {
	http.HandleFunc("/spdy", spdyHandler)

	log.Println("SPDY server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func spdyHandler(w http.ResponseWriter, r *http.Request) {
	upgrader := spdy.NewResponseUpgrader()

	// streamHandler := func(stream httpstream.Stream) error {
	streamHandler := func(stream httpstream.Stream, replySent <-chan struct{}) error {
		log.Printf("New stream opened: %v", stream.Headers())

		go handleStream(stream)
		return nil
	}

	conn := upgrader.UpgradeResponse(w, r, streamHandler)
	// conn := upgrader.UpgradeResponse(w, r, spdy.NewResponseUpgraderOptions())

	defer conn.Close()

	log.Println("SPDY connection established")

	<-conn.CloseChan()
	log.Printf("client SPDY connection closed")
}

func handleStream(stream httpstream.Stream) {
	defer stream.Close()

	log.Printf("New stream: %v", stream.Headers())

	data, err := io.ReadAll(stream)
	if err != nil {
		log.Printf("read error: %v", err)
		return
	}

	log.Printf("Received: %s", string(data))

	// Echo back
	_, _ = stream.Write([]byte("server received: " + string(data)))
}
