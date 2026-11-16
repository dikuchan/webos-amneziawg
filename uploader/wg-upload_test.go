package main

import (
	"strings"
	"testing"
)

func TestImportPreservesDNSForVPNStartup(t *testing.T) {
	// Arrange: ordinary provider config, including CRLF and an inline comment.
	input := "[Interface]\r\nPrivateKey = test-key\r\nAddress = 10.20.0.2/32\r\nDNS = 10.20.0.1, 1.1.1.1 # VPN resolvers\r\n\r\n[Peer]\r\nPublicKey = test-peer\r\nAllowedIPs = 0.0.0.0/0\r\n"

	// Act.
	converted, address, err := convertConfig(input)

	// Assert: the saved config must retain DNS; wg setconf strips it at startup.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(converted, "DNS = 10.20.0.1, 1.1.1.1\n") {
		t.Fatalf("VPN DNS was not preserved: %q", converted)
	}
	if address != "10.20.0.2/32" {
		t.Fatalf("address = %q", address)
	}
}

func TestImportRejectsUnsupportedDNS(t *testing.T) {
	for _, value := range []string{"", "300.1.1.1", "::1", "fd00::1", "vpn.example", "127.0.0.1", "0.0.0.0", "0.1.2.3", "240.0.0.1", "1.1.1.1,,8.8.8.8", "1.1.1.1,8.8.8.8,9.9.9.9,10.0.0.1"} {
		t.Run(value, func(t *testing.T) {
			// Arrange.
			input := "[Interface]\nAddress = 10.20.0.2/32\nDNS = " + value + "\n"

			// Act.
			_, _, err := convertConfig(input)

			// Assert: no silently ignored DNS, unsupported address or search domain.
			if err == nil || !strings.Contains(err.Error(), "DNS") {
				t.Fatalf("expected a DNS error, got %v", err)
			}
		})
	}
}

func TestImportRejectsDNSOutsideInterface(t *testing.T) {
	// Arrange.
	input := "[Interface]\nAddress = 10.20.0.2/32\n[Peer]\nDNS = 1.1.1.1\n"

	// Act.
	_, _, err := convertConfig(input)

	// Assert.
	if err == nil || !strings.Contains(err.Error(), "DNS") {
		t.Fatalf("expected a DNS section error, got %v", err)
	}
}

func TestImportWithoutDNSKeepsExistingBehavior(t *testing.T) {
	// Arrange.
	input := "[Interface]\nPrivateKey = test-key\nAddress = 10.20.0.2/32\n[Peer]\nAllowedIPs = 0.0.0.0/0\n"

	// Act.
	converted, address, err := convertConfig(input)

	// Assert.
	if err != nil || address != "10.20.0.2/32" || strings.Contains(converted, "DNS") {
		t.Fatalf("unexpected conversion: address=%q, error=%v, config=%q", address, err, converted)
	}
}
