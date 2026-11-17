//go:build browser

package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestLANBrowserServer is the bounded, local-only fixture used by Playwright.
// It runs the real handler and template; only disk/network mutations are replaced.
func TestLANBrowserServer(t *testing.T) {
	const address = "127.0.0.1:8090"
	var mu sync.Mutex
	var handler *uploadHandler
	reset := func() {
		document := routingDocument{
			Version: 1,
			Mode:    modeAll,
		}
		handler = newLANHandler(uploadOptions{
			Host:     address,
			Code:     "12345678",
			Token:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Language: "en",
			Expires:  time.Now().Add(10 * time.Minute),
			Now:      time.Now,
		}, lanBackend{
			Load: func() (routingDocument, error) { return document, nil },
			Save: func(_ context.Context, revision string, next routingDocument) error {
				if revision != policyRevision(document) {
					return errRoutingConflict
				}
				document = next
				return nil
			},
			Import: func([]byte, candidateConfig) error { return nil },
		}, func(uploadPhase) {})
	}
	reset()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if r.Method == http.MethodGet && r.URL.Query().Get("reset") == "1" {
				reset()
			}
			handler.ServeHTTP(w, r)
		}),
	}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := server.Shutdown(shutdown); err != nil {
			t.Errorf("fixture shutdown: %v", err)
		}
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
}
