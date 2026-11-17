package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func exampleConfig() string {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	return "[Interface]\nPrivateKey = " + key + "\nAddress = 10.203.7.2/32, fd00::2/128\nDNS = 10.203.7.1\nMTU = 1360\n" +
		"Jc = 4\nJmin = 40\nJmax = 80\nS1 = 16\nS2 = 16\nS3 = 16\nS4 = 16\n" +
		"H1 = 1\nH2 = 2\nH3 = 3\nH4 = 4\nI1 = <b 0x01020304><r 12>\n" +
		"HeaderProtectionKey = " + key + "\nContentPaddingAddition = 0-64\nRekeyAfterTime = 120-150\n" +
		"RekeyTimeout = 5-8\nRejectAfterTime = 180-200\nKeepaliveTimeout = 10-15\nMaxHandshakeAttempts = 10-20\n" +
		"RandomTrailers = on\nDisableCookies = on\n\n[Peer]\nPublicKey = " + key + "\n" +
		"AllowedIPs = 0.0.0.0/0, ::/0\nEndpoint = vpn.example.test:31820\nPersistentKeepalive = 20-30\n"
}

func TestProviderConfigPreservesAWG31AndMTU(t *testing.T) {
	// Arrange: a provider-style dual-stack file with every AWG 3.1 feature.
	input := exampleConfig()
	// Act.
	c, err := parseConfig([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	wire := string(c.setconf("192.0.2.55:31820"))
	// Assert: network-only settings are consumed, protocol settings are preserved.
	if c.MTU != 1360 || c.Addresses[0].String() != "10.203.7.2/32" || c.DNS[0].String() != "10.203.7.1" {
		t.Fatal("provider network settings were lost")
	}
	for _, setting := range []string{
		"HeaderProtectionKey = ",
		"RandomTrailers = on",
		"DisableCookies = on",
		"ContentPaddingAddition = 0-64",
		"PersistentKeepalive = 20-30",
		"I1 = <b 0x01020304><r 12>",
		"Endpoint = 192.0.2.55:31820",
	} {
		if !strings.Contains(wire, setting) {
			t.Errorf("lost protocol setting %q", setting)
		}
	}
	for _, setting := range []string{
		"Address =",
		"DNS =",
		"MTU =",
		"vpn.example.test",
	} {
		if strings.Contains(wire, setting) {
			t.Errorf("network-only setting reached awg: %q", setting)
		}
	}
}

func TestUnsafeOrIncompatibleInputIsRejectedWithoutEchoingSecrets(t *testing.T) {
	for name, transform := range map[string]func(string) string{
		"shell hook":     func(s string) string { return strings.Replace(s, "MTU = 1360", "PostUp = secret-shell-command", 1) },
		"unknown option": func(s string) string { return strings.Replace(s, "MTU = 1360", "UnrecognizedKey = secret-option", 1) },
		"invalid key":    func(s string) string { return strings.Replace(s, "PrivateKey = ", "PrivateKey = secret-bad-key", 1) },
		"IPv6 resolver":  func(s string) string { return strings.Replace(s, "DNS = 10.203.7.1", "DNS = fd00::1", 1) },
		"split tunnel":   func(s string) string { return strings.Replace(s, "0.0.0.0/0", "10.0.0.0/8", 1) },
		"missing nonce":  func(s string) string { return strings.Replace(s, "S4 = 16", "S4 = 4", 1) },
		"junk underflow": func(s string) string { return strings.Replace(s, "Jmin = 40", "Jmin = 90", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange / Act.
			_, err := parseConfig([]byte(transform(exampleConfig())))
			// Assert: invalid input is rejected at import, without printing its value.
			if err == nil {
				t.Fatal("invalid config accepted")
			}
			if strings.Contains(err.Error(), "secret-") {
				t.Fatal("error leaked config contents")
			}
		})
	}
}

func TestConfigDoesNotDependOnLineEndingsOrKeyCapitalization(t *testing.T) {
	// Arrange.
	input := strings.ReplaceAll(strings.ReplaceAll(exampleConfig(), "DNS =", "dns ="), "\n", "\r\n")
	// Act.
	c, err := parseConfig([]byte(input))
	// Assert.
	if err != nil || c.MTU != 1360 || len(c.DNS) != 1 {
		t.Fatal("valid provider formatting rejected", err)
	}
}

func TestProviderNumericSwitches(t *testing.T) {
	// Arrange: awg-tools accepts the provider's numeric switch spelling.
	input := strings.ReplaceAll(exampleConfig(), "RandomTrailers = on", "RandomTrailers = 1")
	input = strings.ReplaceAll(input, "DisableCookies = on", "DisableCookies = 1")
	// Act.
	c, err := parseConfig([]byte(input))
	// Assert: these provider options reach the official parser unchanged.
	if err != nil {
		t.Fatal(err)
	}
	wire := string(c.setconf("192.0.2.55:31820"))
	for _, option := range []string{
		"RandomTrailers = 1",
		"DisableCookies = 1",
	} {
		if !strings.Contains(wire, option) {
			t.Errorf("provider option lost: %s", option)
		}
	}
}

func TestKernelOnlySwitchIsRejectedBeforeChangingNetwork(t *testing.T) {
	// The official awg UAPI backend rejects WGPEER_HAS_AWG with EINVAL.
	// Reject it at import instead of failing after creating the test interface.
	_, err := parseConfig([]byte(exampleConfig() + "AdvancedSecurity = on\n"))
	if err == nil || !strings.Contains(err.Error(), "kernel-only") {
		t.Fatal("kernel-only switch was not diagnosed at import", err)
	}
}
