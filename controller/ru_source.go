package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const (
	ruPrefixLimit = 2048
	ruByteLimit   = 128 * 1024
)

//go:embed routing-data/ru-standard.ipv4.txt
var bundledRoutingList []byte

//go:embed routing-data/manifest.json
var bundledRoutingManifest []byte

type routingDataset struct {
	Generated time.Time      `json:"generated"`
	Checked   time.Time      `json:"checked"`
	Hash      string         `json:"hash"`
	Prefixes  []netip.Prefix `json:"-"`
	Source    []byte         `json:"source"`
}
type routingFileInfo struct {
	Name string `json:"name"`
	Size int    `json:"size"`
	Hash string `json:"sha256"`
}
type routingFeedInfo struct {
	Generated time.Time
	Count     int
	File      routingFileInfo
}

var reservedRoutingPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/3"),
}

func validRoutingPrefix(prefix netip.Prefix) bool {
	if !prefix.IsValid() || !prefix.Addr().Is4() || prefix.Bits() < 8 {
		return false
	}
	for _, reserved := range reservedRoutingPrefixes {
		if prefix.Overlaps(reserved) {
			return false
		}
	}
	return true
}
func normalizeRoutingPrefixes(prefixes []netip.Prefix) ([]netip.Prefix, error) {
	if len(prefixes) == 0 || len(prefixes) > 4096 {
		return nil, errors.New("routing list has an invalid count")
	}
	result := append([]netip.Prefix(nil), prefixes...)
	for i, prefix := range result {
		if !validRoutingPrefix(prefix) {
			return nil, errors.New("routing list contains a non-public, invalid or overly broad prefix")
		}
		result[i] = prefix.Masked()
	}
	sort.Slice(result, func(i, j int) bool {
		comparison := result[i].Addr().Compare(result[j].Addr())
		if comparison == 0 {
			return result[i].Bits() < result[j].Bits()
		}
		return comparison < 0
	})
	normalized := result[:0]
	for _, prefix := range result {
		if len(normalized) == 0 || !normalized[len(normalized)-1].Contains(prefix.Addr()) {
			normalized = append(normalized, prefix)
		}
	}
	if len(normalized) > ruPrefixLimit {
		return nil, errors.New("routing list exceeds 2048 prefixes")
	}
	return normalized, nil
}
func parseRoutingList(raw []byte) ([]netip.Prefix, error) {
	if len(raw) > ruByteLimit {
		return nil, errors.New("routing list exceeds 128 KiB")
	}
	var prefixes []netip.Prefix
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		prefix, err := netip.ParsePrefix(line)
		if err != nil {
			return nil, errors.New("routing list contains an invalid CIDR")
		}
		prefixes = append(prefixes, prefix)
		if len(prefixes) > 4096 {
			return nil, errors.New("routing list has too many entries")
		}
	}
	return normalizeRoutingPrefixes(prefixes)
}
func decodeRoutingContent(info routingFeedInfo, raw []byte) (routingDataset, error) {
	if info.Generated.IsZero() || info.Count < 1 || info.Count > ruPrefixLimit || info.File.Name != "ru-standard.ipv4.txt" || info.File.Size != len(raw) || len(info.File.Hash) != 64 {
		return routingDataset{}, errors.New("routing manifest is invalid or incomplete")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != info.File.Hash {
		return routingDataset{}, errors.New("routing list checksum does not match its manifest")
	}
	prefixes, err := parseRoutingList(raw)
	if err != nil {
		return routingDataset{}, err
	}
	if len(prefixes) != info.Count {
		return routingDataset{}, errors.New("routing list count does not match its manifest")
	}
	return routingDataset{Generated: info.Generated, Hash: info.File.Hash, Prefixes: prefixes, Source: raw}, nil
}
func decodeRoutingFeed(manifest, raw []byte) (routingDataset, error) {
	var value struct {
		Schema    int       `json:"schema"`
		Generated time.Time `json:"generatedAt"`
		Tiers     map[string]struct {
			IPv4  int               `json:"ipv4"`
			Files []routingFileInfo `json:"files"`
		} `json:"tiers"`
	}
	if len(manifest) > 256*1024 || json.Unmarshal(manifest, &value) != nil || value.Schema != 1 {
		return routingDataset{}, errors.New("routing manifest is invalid")
	}
	tier, ok := value.Tiers["ru-standard"]
	if !ok {
		return routingDataset{}, errors.New("routing manifest has no standard list")
	}
	for _, file := range tier.Files {
		if file.Name == "ru-standard.ipv4.txt" {
			return decodeRoutingContent(routingFeedInfo{Generated: value.Generated, Count: tier.IPv4, File: file}, raw)
		}
	}
	return routingDataset{}, errors.New("routing manifest has no IPv4 list")
}
func bundledDataset() (routingDataset, error) {
	var value struct {
		Generated time.Time       `json:"generatedAt"`
		Count     int             `json:"ipv4"`
		File      routingFileInfo `json:"file"`
	}
	if err := json.Unmarshal(bundledRoutingManifest, &value); err != nil {
		return routingDataset{}, err
	}
	return decodeRoutingContent(routingFeedInfo{Generated: value.Generated, Count: value.Count, File: value.File}, bundledRoutingList)
}
func decodeRoutingCache(raw []byte) (routingDataset, error) {
	var cached routingDataset
	if len(raw) > 256*1024 || json.Unmarshal(raw, &cached) != nil || cached.Generated.IsZero() || len(cached.Hash) != 64 {
		return routingDataset{}, errors.New("invalid saved routing list")
	}
	normalized, err := parseRoutingList(cached.Source)
	checksum := sha256.Sum256(cached.Source)
	if err != nil || hex.EncodeToString(checksum[:]) != cached.Hash {
		return routingDataset{}, errors.New("saved routing list checksum mismatch")
	}
	cached.Prefixes = normalized
	return cached, nil
}
func fetchRouteFile(ctx context.Context, client *http.Client, url string, limit int) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("routing source is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("routing source returned an error")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil || len(raw) > limit {
		return nil, errors.New("routing source response is invalid or too large")
	}
	return raw, nil
}
func downloadRoutingDataset(ctx context.Context, client *http.Client, origin string) (routingDataset, error) {
	manifest, err := fetchRouteFile(ctx, client, origin+"/manifest.json", 256*1024)
	if err != nil {
		return routingDataset{}, err
	}
	raw, err := fetchRouteFile(ctx, client, origin+"/ru-standard.ipv4.txt", ruByteLimit)
	if err != nil {
		return routingDataset{}, err
	}
	return decodeRoutingFeed(manifest, raw)
}
