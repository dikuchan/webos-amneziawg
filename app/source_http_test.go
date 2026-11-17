package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPSListUsesConditionalRequestsAndValidatesChecksum(t *testing.T) {
	raw := []byte("5.45.192.1\n")
	hash := contentHash(raw)
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/list.sha256" {
			if _, err := w.Write([]byte(hash + "  list.txt\n")); err != nil {
				t.Error(err)
			}
			return
		}
		requests++
		w.Header().Set("ETag", "\"one\"")
		if r.Header.Get("If-None-Match") == "\"one\"" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if _, err := w.Write(raw); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	source := listSource{
		Spec: sourceSpec{
			ID:          "one",
			Kind:        sourceRemote,
			URL:         server.URL + "/list.txt",
			ChecksumURL: server.URL + "/list.sha256",
			EveryHours:  24,
		},
	}

	cache, err := downloadSource(context.Background(), server.Client(), source, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	source.Cache = cache
	next, err := downloadSource(context.Background(), server.Client(), source, time.Now())

	if err != nil || string(next.Raw) != string(raw) || next.ETag != "\"one\"" || requests != 2 {
		t.Fatal("conditional list refresh failed", err)
	}
	hash = strings.Repeat("0", 64)
	if _, err := downloadSource(context.Background(), server.Client(), source, time.Now()); err == nil {
		t.Fatal("invalid SHA-256 accepted")
	}
	if string(source.Cache.Raw) != string(raw) {
		t.Fatal("previous snapshot changed")
	}
}

func TestLiteralIPv4SourceResolution(t *testing.T) {
	source := listSource{
		Spec: sourceSpec{
			Kind: sourceRemote,
			URL:  "https://5.45.192.1:8443/rules.txt",
		},
	}
	hosts, ips := resolveSourceHosts(context.Background(), []listSource{
		source,
	})
	if len(ips) != 1 || len(hosts["5.45.192.1"]) != 1 {
		t.Fatalf("literal IPv4 source was not resolved: %v", hosts)
	}
}
