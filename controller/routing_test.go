package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestRoutingListCanonicalizesPublicIPv4WithoutBroadeningIt(t *testing.T) {
	// Arrange / Act: duplicates, nesting and host bits from an external list.
	got, err := parseRoutingList([]byte("5.45.192.5/24\n5.45.192.1/32\n77.88.0.0/18\n5.45.192.0/24\n"))
	// Assert: normalized networks, no invented ranges.
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != netip.MustParsePrefix("5.45.192.0/24") || got[1] != netip.MustParsePrefix("77.88.0.0/18") {
		t.Fatal("incorrect normalized list")
	}
}

func TestRoutingListRejectsDangerousOrUnboundedInput(t *testing.T) {
	for _, input := range []string{"", "0.0.0.0/0", "5.0.0.0/4", "10.0.0.0/8", "127.0.0.1/32", "100.64.0.0/10", "192.168.1.0/24", "198.51.100.0/24", "::/0", "<html>error</html>", strings.Repeat("5.45.192.0/24\n", 5000)} {
		// Act / Assert: bad input never becomes a route or executable command.
		if _, err := parseRoutingList([]byte(input)); err == nil {
			t.Fatal("unsafe routing list accepted")
		}
	}
}

func TestFeedRequiresMatchingManifestSizeHashAndCount(t *testing.T) {
	// Arrange: a complete published list and its manifest.
	data := []byte("5.45.192.0/24\n77.88.0.0/18\n")
	sum := sha256.Sum256(data)
	manifest, err := json.Marshal(map[string]any{"schema": 1, "generatedAt": "2026-11-16T10:00:00Z", "tiers": map[string]any{"ru-standard": map[string]any{"ipv4": 2, "files": []map[string]any{{"name": "ru-standard.ipv4.txt", "size": len(data), "sha256": hex.EncodeToString(sum[:])}}}}})
	if err != nil {
		t.Fatal(err)
	}
	// Act / Assert: valid data is accepted; a truncated but syntactically valid list is not.
	if _, err := decodeRoutingFeed(manifest, data); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeRoutingFeed(manifest, []byte("5.45.192.0/24\n")); err == nil {
		t.Fatal("truncated feed accepted")
	}
	if _, err := decodeRoutingFeed([]byte(`{"schema":1}`), data); err == nil {
		t.Fatal("missing manifest accepted")
	}
}

// Route ownership is an external safety contract: an unrelated matching route
// must survive enabling/disabling split routing, regardless of implementation.
func TestSplitRoutePlanDoesNotAdoptExistingUnownedRoutes(t *testing.T) {
	route := directRoute{Prefix: netip.MustParsePrefix("5.45.192.0/24"), Interface: 2, Gateway: netip.MustParseAddr("192.168.0.1")}
	next := &splitSession{Interface: route.Interface, Gateway: route.Gateway, Prefixes: []netip.Prefix{route.Prefix}}

	_, _, err := planDirectRoutes([]directRoute{route}, nil, next)

	if err == nil {
		t.Fatal("existing unowned route was adopted")
	}
}

func TestSplitRoutePlanRemovesOnlyRecordedExactTuples(t *testing.T) {
	owned := directRoute{Prefix: netip.MustParsePrefix("5.45.192.0/24"), Interface: 2, Gateway: netip.MustParseAddr("192.168.0.1")}
	foreign := owned
	foreign.Gateway = netip.MustParseAddr("192.168.0.2")
	previous := &splitSession{Interface: owned.Interface, Gateway: owned.Gateway, Prefixes: []netip.Prefix{owned.Prefix}}

	add, remove, err := planDirectRoutes([]directRoute{owned, foreign}, previous, nil)

	if err != nil || len(add) != 0 || len(remove) != 1 || remove[0] != owned {
		t.Fatal("plan did not preserve foreign route", err)
	}
}

// The unmanaged HTTP boundary is local and deterministic: a source can return
// HTTP 200 with a partial or swapped file. Neither becomes a usable dataset.
func TestRoutingDownloadChecksPublishedContentBeforeReturningIt(t *testing.T) {
	data := []byte("5.45.192.0/24\n77.88.0.0/18\n")
	sum := sha256.Sum256(data)
	manifest, err := json.Marshal(map[string]any{"schema": 1, "generatedAt": "2026-11-16T10:00:00Z", "tiers": map[string]any{"ru-standard": map[string]any{"ipv4": 2, "files": []map[string]any{{"name": "ru-standard.ipv4.txt", "size": len(data), "sha256": hex.EncodeToString(sum[:])}}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "truncated"}[broken], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := manifest
				if r.URL.Path == "/ru-standard.ipv4.txt" {
					body = data
					if broken {
						body = data[:len(data)/2]
					}
				}
				if _, err := w.Write(body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()

			result, err := downloadRoutingDataset(context.Background(), server.Client(), server.URL)

			if broken && err == nil {
				t.Fatal("partial source accepted")
			}
			if !broken && (err != nil || len(result.Prefixes) != 2) {
				t.Fatal("valid source rejected", err)
			}
		})
	}
}

func TestCachedListCannotSilentlyChangeWhileKeepingItsChecksum(t *testing.T) {
	// Arrange: downloaded data and a valid, but different public CIDR.
	original, err := bundledDataset()
	if err != nil {
		t.Fatal(err)
	}
	original.Source = []byte("5.45.192.0/24\n")
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	_, err = decodeRoutingCache(raw)

	if err == nil {
		t.Fatal("damaged saved list accepted")
	}
}

func TestRoutingUpdatesRespectDailyChecksAndHourlyFailureBackoff(t *testing.T) {
	now := time.Date(2026, 11, 16, 18, 0, 0, 0, time.UTC)
	for _, scenario := range []struct {
		name       string
		state      routingUpdateState
		ago        time.Duration
		invocation updateInvocation
		due        bool
	}{
		{"daily-wait", updateCurrent, 23 * time.Hour, updateScheduled, false},
		{"daily-due", updateCurrent, 25 * time.Hour, updateScheduled, true},
		{"failure-wait", updateFailed, 59 * time.Minute, updateScheduled, false},
		{"failure-due", updateFailed, 61 * time.Minute, updateScheduled, true},
		{"active-download", updateRunning, time.Minute, updateScheduled, false},
		{"interrupted-download", updateRunning, 3 * time.Minute, updateScheduled, true},
		{"manual", updateFailed, time.Minute, updateManual, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			previous := routingUpdate{Attempted: now.Add(-scenario.ago), State: scenario.state}

			actual := routingUpdateDue(previous, now, scenario.invocation)

			if actual != scenario.due {
				t.Fatal("unexpected update schedule")
			}
		})
	}
}
