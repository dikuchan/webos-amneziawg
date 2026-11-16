package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

const maxConfigBytes = 64 * 1024

type setting struct{ Key, Value string }

// Protocol settings are opaque to this controller. Their final validation belongs
// to the official awg parser, before any traffic routes or DNS are changed.
type candidateConfig struct {
	Addresses []netip.Prefix
	DNS       []netip.Addr
	MTU       uint16
	Host      string
	Port      uint16
	Interface []setting
	Peer      []setting
	Features  []string
}

var interfaceNames = strings.Fields("PrivateKey ListenPort FwMark Jc Jmin Jmax S1 S2 S3 S4 H1 H2 H3 H4 I1 I2 I3 I4 I5 HeaderProtectionKey ContentPaddingAddition RekeyAfterTime RekeyTimeout RejectAfterTime KeepaliveTimeout MaxHandshakeAttempts RandomTrailers DisableCookies Address DNS MTU")
var peerNames = strings.Fields("PublicKey PresharedKey AllowedIPs Endpoint PersistentKeepalive AdvancedSecurity")

func canonical(key string, names []string) string {
	for _, name := range names {
		if strings.EqualFold(key, name) {
			return name
		}
	}
	return ""
}

func validKey(value string) bool {
	b, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(b) == 32
}

func uintRange(value string, bits int) (uint64, uint64, error) {
	parts := strings.Split(value, "-")
	if len(parts) < 1 || len(parts) > 2 {
		return 0, 0, errors.New("invalid numeric range")
	}
	lo, err := strconv.ParseUint(parts[0], 10, bits)
	if err != nil {
		return 0, 0, errors.New("invalid numeric range")
	}
	hi := lo
	if len(parts) == 2 {
		hi, err = strconv.ParseUint(parts[1], 10, bits)
	}
	if err != nil || lo > hi {
		return 0, 0, errors.New("invalid numeric range")
	}
	return lo, hi, nil
}

func validateProtocol(s setting) error {
	bad := func() error { return fmt.Errorf("invalid %s (value omitted)", s.Key) }
	switch s.Key {
	case "PrivateKey", "PublicKey", "PresharedKey", "HeaderProtectionKey":
		if !validKey(s.Value) {
			return bad()
		}
	case "ListenPort":
		if _, err := strconv.ParseUint(s.Value, 10, 16); err != nil {
			return bad()
		}
	case "FwMark":
		if _, err := strconv.ParseUint(s.Value, 0, 32); err != nil {
			return bad()
		}
	case "Jc", "Jmin", "Jmax", "S1", "S2", "S3", "S4":
		if _, err := strconv.ParseUint(s.Value, 10, 32); err != nil {
			return bad()
		}
	case "H1", "H2", "H3", "H4", "ContentPaddingAddition", "RekeyAfterTime", "RekeyTimeout", "RejectAfterTime", "KeepaliveTimeout", "MaxHandshakeAttempts", "PersistentKeepalive":
		if _, _, err := uintRange(s.Value, 32); err != nil {
			return bad()
		}
	case "RandomTrailers", "DisableCookies", "AdvancedSecurity":
		if !strings.EqualFold(s.Value, "on") && !strings.EqualFold(s.Value, "off") {
			if _, err := strconv.ParseUint(s.Value, 10, 32); err != nil {
				return bad()
			}
		}
	case "I1", "I2", "I3", "I4", "I5":
		// The upstream CPS interpreter validates its tag language at setconf.
		if len(s.Value) > 8192 || !strings.HasPrefix(s.Value, "<") || !strings.HasSuffix(s.Value, ">") {
			return bad()
		}
	}
	return nil
}

func parseConfig(input []byte) (candidateConfig, error) {
	c := candidateConfig{MTU: 1280}
	if len(input) > maxConfigBytes {
		return c, errors.New("config exceeds 64 KiB")
	}
	section := ""
	seen := make(map[string]string)
	peers := 0
	scanner := bufio.NewScanner(bytes.NewReader(input))
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			switch strings.ToLower(line) {
			case "[interface]":
				if section != "" {
					return c, errors.New("only one Interface followed by one Peer is supported")
				}
				section = "interface"
			case "[peer]":
				peers++
				if section != "interface" || peers != 1 {
					return c, errors.New("the test kit supports exactly one Peer")
				}
				section = "peer"
			default:
				return c, fmt.Errorf("unsupported section at line %d", lineNo)
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section == "" {
			return c, fmt.Errorf("invalid config line %d (contents omitted)", lineNo)
		}
		names := interfaceNames
		if section == "peer" {
			names = peerNames
		}
		key = canonical(strings.TrimSpace(key), names)
		value = strings.TrimSpace(value)
		if key == "" || value == "" || strings.ContainsAny(value, "\x00\r") {
			return c, fmt.Errorf("unsupported or empty option at line %d (contents omitted)", lineNo)
		}
		if _, exists := seen[key]; exists {
			return c, fmt.Errorf("duplicate %s", key)
		}
		seen[key] = value
		s := setting{key, value}
		switch key {
		case "AdvancedSecurity":
			return c, errors.New("AdvancedSecurity is a kernel-only peer switch, unsupported by official userspace awg; export a userspace AWG config")
		case "Address":
			for _, text := range strings.Split(value, ",") {
				prefix, err := netip.ParsePrefix(strings.TrimSpace(text))
				if err != nil {
					return c, errors.New("invalid Address (value omitted)")
				}
				if prefix.Addr().Is4() {
					c.Addresses = append(c.Addresses, prefix)
				}
			}
		case "DNS":
			for _, text := range strings.Split(value, ",") {
				ip, err := netip.ParseAddr(strings.TrimSpace(text))
				if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.As4()[0] == 0 || ip.As4()[0] >= 224 {
					return c, errors.New("this test kit requires unicast IPv4 DNS servers")
				}
				c.DNS = append(c.DNS, ip)
			}
			if len(c.DNS) > 3 {
				return c, errors.New("at most three DNS servers are supported")
			}
		case "MTU":
			n, err := strconv.ParseUint(value, 10, 16)
			if err != nil || n < 576 || n > 9000 {
				return c, errors.New("MTU must be between 576 and 9000")
			}
			c.MTU = uint16(n)
		case "Endpoint":
			host, port, err := net.SplitHostPort(value)
			p, portErr := strconv.ParseUint(port, 10, 16)
			if err != nil || portErr != nil || p == 0 || host == "" || strings.ContainsAny(host, " \t/:\\") {
				return c, errors.New("an IPv4 or hostname Endpoint with a port is required")
			}
			c.Host, c.Port = host, uint16(p)
			c.Peer = append(c.Peer, s)
		case "AllowedIPs":
			full := false
			for _, text := range strings.Split(value, ",") {
				prefix, err := netip.ParsePrefix(strings.TrimSpace(text))
				if err != nil {
					return c, errors.New("invalid AllowedIPs")
				}
				if prefix == netip.MustParsePrefix("0.0.0.0/0") {
					full = true
				}
			}
			if !full {
				return c, errors.New("this test kit requires full-tunnel AllowedIPs = 0.0.0.0/0")
			}
			c.Peer = append(c.Peer, s)
		default:
			if err := validateProtocol(s); err != nil {
				return c, err
			}
			if section == "interface" {
				c.Interface = append(c.Interface, s)
			} else {
				c.Peer = append(c.Peer, s)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return c, errors.New("cannot read config")
	}
	if len(c.Addresses) == 0 || seen["PrivateKey"] == "" || seen["PublicKey"] == "" || c.Host == "" || seen["AllowedIPs"] == "" {
		return c, errors.New("config requires an IPv4 Address, PrivateKey, PublicKey, Endpoint and AllowedIPs")
	}
	if a, ok := seen["Jmin"]; ok {
		lo, _ := strconv.ParseUint(a, 10, 32)
		hi, _ := strconv.ParseUint(seen["Jmax"], 10, 32)
		if lo > hi {
			return c, errors.New("Jmin exceeds Jmax")
		}
	}
	if seen["HeaderProtectionKey"] != "" {
		for _, key := range []string{"S1", "S2", "S3", "S4"} {
			n, _ := strconv.ParseUint(seen[key], 10, 32)
			if n < 12 {
				return c, fmt.Errorf("HeaderProtectionKey requires %s >= 12", key)
			}
		}
		c.Features = append(c.Features, "header-protection")
	}
	for _, key := range []string{"ContentPaddingAddition", "RandomTrailers", "DisableCookies"} {
		if v := seen[key]; v != "" && !strings.EqualFold(v, "off") && v != "0" && v != "0-0" {
			c.Features = append(c.Features, key)
		}
	}
	return c, nil
}

func (c candidateConfig) setconf(endpoint string) []byte {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	for _, s := range c.Interface {
		fmt.Fprintf(&b, "%s = %s\n", s.Key, s.Value)
	}
	b.WriteString("\n[Peer]\n")
	for _, s := range c.Peer {
		value := s.Value
		if s.Key == "Endpoint" {
			value = endpoint
		}
		fmt.Fprintf(&b, "%s = %s\n", s.Key, value)
	}
	return []byte(b.String())
}
