package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

const httpTimeout = 30 * time.Second

func closeWithWarning(name string, closer io.Closer) {
	err := closer.Close()
	if err != nil {
		slog.Warn("Failed to close resource", "name", name, "error", err.Error())
	}
}

func (app *App) printLines(lines ...string) {
	writer := app.outputWriter
	if writer == nil {
		writer = os.Stdout
	}

	for _, line := range lines {
		_, err := fmt.Fprintln(writer, line)
		if err != nil {
			slog.Warn("Failed to write output", "error", err.Error())

			return
		}
	}
}

func (app *App) failOnError(err error, msg string) {
	if err != nil {
		slog.Error(msg, "error", err.Error())
		os.Exit(1)
	}
}

func (app *App) getURL(rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", rawURL, err)
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: app.insecure},
	}

	client := &http.Client{
		Timeout:   httpTimeout,
		Transport: tr,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request to %s: %w", rawURL, err)
	}

	return resp, nil
}
