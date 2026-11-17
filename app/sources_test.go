package main

import (
	"net/netip"
	"testing"
	"time"
)

func TestIPv4TextAcceptsHostsCommentsAndNormalizesNetworks(t *testing.T) {
	raw := []byte("# example\r\n5.45.192.5/24 # network\n5.45.192.1\n10.42.0.1\n")

	prefixes, err := parseIPv4Text(raw)

	if err != nil || len(prefixes) != 2 || prefixes[0] != netip.MustParsePrefix("5.45.192.0/24") || prefixes[1] != netip.MustParsePrefix("10.42.0.1/32") {
		t.Fatal("incorrect IPv4 list", err)
	}
}

func TestIPv4TextRejectsMalformedIPv6EmptyAndOversizedLists(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(""),
		[]byte("# empty"),
		[]byte("example.org"),
		[]byte("::/0"),
		[]byte("1.2.3.999"),
		make([]byte, sourceByteLimit+1),
	} {
		if _, err := parseIPv4Text(raw); err == nil {
			t.Fatal("invalid list accepted")
		}
	}
}

func TestIncludeAndExcludeProtectLANAndUseOppositePaths(t *testing.T) {
	network := physicalNetwork{
		Index:   2,
		Gateway: netip.MustParseAddr("192.168.0.1"),
		LAN: []netip.Prefix{
			netip.MustParsePrefix("192.168.0.0/24"),
		},
	}
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("5.45.192.0/18"),
		netip.MustParsePrefix("192.168.0.64/32"),
	}

	include, err := compileDataRoutes(modeInclude, prefixes, network, 7)
	if err != nil {
		t.Fatal(err)
	}
	exclude, err := compileDataRoutes(modeExclude, prefixes, network, 7)
	if err != nil {
		t.Fatal(err)
	}

	if len(include) != 1 || include[0].Interface != 7 || include[0].Gateway.IsValid() {
		t.Fatal("include must contain only selected non-LAN VPN routes")
	}
	direct := 0
	full := 0
	for _, route := range exclude {
		if route.Prefix == prefixes[0] && route.Interface == 2 && route.Gateway == network.Gateway {
			direct++
		}
		if route.Prefix.Bits() == 1 && route.Interface == 7 {
			full++
		}
		if route.Prefix == prefixes[1] {
			t.Fatal("LAN route must not be overridden")
		}
	}
	if direct != 1 || full != 2 {
		t.Fatal("exclude must retain full VPN routing and selected direct route")
	}
}

func TestPerSourceIntervalsHaveIndependentDueTimes(t *testing.T) {
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		hours uint16
		age   time.Duration
		want  bool
	}{
		{
			6,
			7 * time.Hour,
			true,
		},
		{
			24,
			7 * time.Hour,
			false,
		},
		{
			24,
			25 * time.Hour,
			true,
		},
	} {
		if sourceIsDue(item.hours, now.Add(-item.age), sourceReady, now) != item.want {
			t.Fatal("incorrect update interval")
		}
	}
}

func TestSourceFailureBackoffDoesNotDependOnConfiguredInterval(t *testing.T) {
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	if sourceIsDue(24, now.Add(-59*time.Minute), sourceFailed, now) {
		t.Fatal("failure retried too early")
	}
	if !sourceIsDue(24, now.Add(-61*time.Minute), sourceFailed, now) {
		t.Fatal("failure does not retry after an hour")
	}
}
