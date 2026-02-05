package main

import (
	"context"
	"log"
	"net/http"
	"net/url"

	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

func main() {
	serverURL, _ := url.Parse("http://localhost:8080/spdy")

	restConfig := &rest.Config{
		Host:      serverURL.String(),
		Transport: http.DefaultTransport,
		ContentConfig: rest.ContentConfig{
			NegotiatedSerializer: nil,
		},
	}

	exec, err := remotecommand.NewSPDYExecutor(
		restConfig,
		http.MethodPost,
		serverURL,
	)
	if err != nil {
		log.Fatal(err)
	}

	options := remotecommand.StreamOptions{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Tty:    false,
	}

	log.Println("Starting remotecommand stream")
	if err := exec.StreamWithContext(context.Background(), options); err != nil {
		log.Fatal(err)
	}
}
