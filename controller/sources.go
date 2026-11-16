package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	sourceLimit          = 8
	sourceByteLimit      = 256 * 1024
	sourceTotalByteLimit = 1024 * 1024
	prefixLimit          = 16384
	routingStorePath     = base + "/conf/routing.json"
)

type routingMode string

const (
	modeAll     routingMode = "all"
	modeExclude routingMode = "exclude"
	modeInclude routingMode = "include"
)

type sourceKind string

const (
	sourceRemote sourceKind = "url"
	sourceLocal  sourceKind = "file"
	sourceRU     sourceKind = "ru-direct"
)

type sourceState string

const (
	sourceReady   sourceState = "ready"
	sourceFailed  sourceState = "failed"
	sourceMissing sourceState = "missing"
)

type sourceID string
type sourceSpec struct {
	ID          sourceID   `json:"id"`
	Kind        sourceKind `json:"kind"`
	Name        string     `json:"name"`
	URL         string     `json:"url,omitempty"`
	ChecksumURL string     `json:"checksumUrl,omitempty"`
	EveryHours  uint16     `json:"everyHours,omitempty"`
}
type sourceCache struct {
	Raw       []byte      `json:"data,omitempty"`
	Hash      string      `json:"sha256,omitempty"`
	ETag      string      `json:"etag,omitempty"`
	Modified  string      `json:"lastModified,omitempty"`
	Attempted time.Time   `json:"attempted,omitempty"`
	Checked   time.Time   `json:"checked,omitempty"`
	State     sourceState `json:"state"`
}
type listSource struct {
	Spec  sourceSpec  `json:"source"`
	Cache sourceCache `json:"cache"`
}
type routingDocument struct {
	Version int          `json:"version"`
	Mode    routingMode  `json:"mode"`
	Sources []listSource `json:"sources"`
}

var errListsMissing = errors.New("import or update routing lists first")
var errRoutingConflict = errors.New("routing settings changed; reload the editor")

func newSourceID() (sourceID, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return sourceID(hex.EncodeToString(b[:])), nil
}
func validMode(mode routingMode) bool {
	return mode == modeAll || mode == modeExclude || mode == modeInclude
}
func parseIPv4Text(raw []byte) ([]netip.Prefix, error) {
	if len(raw) > sourceByteLimit || !utf8.Valid(raw) {
		return nil, errors.New("IPv4 list exceeds 256 KiB or is not UTF-8")
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	result := make([]netip.Prefix, 0)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		var prefix netip.Prefix
		if strings.Contains(line, "/") {
			p, err := netip.ParsePrefix(line)
			if err != nil || !p.Addr().Is4() {
				return nil, errors.New("list must contain only IPv4 addresses or CIDRs")
			}
			prefix = p.Masked()
		} else {
			ip, err := netip.ParseAddr(line)
			if err != nil || !ip.Is4() {
				return nil, errors.New("list must contain only IPv4 addresses or CIDRs")
			}
			prefix = netip.PrefixFrom(ip, 32)
		}
		result = append(result, prefix)
		if len(result) > 2*prefixLimit {
			return nil, errors.New("too many IPv4 entries")
		}
	}
	if len(result) == 0 {
		return nil, errors.New("IPv4 list is empty")
	}
	return normalizePrefixes(result)
}
func normalizePrefixes(prefixes []netip.Prefix) ([]netip.Prefix, error) {
	if len(prefixes) > sourceLimit*prefixLimit*2 {
		return nil, errors.New("too many IPv4 entries")
	}
	set := make(map[netip.Prefix]struct{}, len(prefixes))
	for _, p := range prefixes {
		if !p.IsValid() || !p.Addr().Is4() {
			return nil, errors.New("invalid IPv4 prefix")
		}
		set[p.Masked()] = struct{}{}
	}
	result := make([]netip.Prefix, 0, len(set))
	for p := range set {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		c := result[i].Addr().Compare(result[j].Addr())
		if c == 0 {
			return result[i].Bits() < result[j].Bits()
		}
		return c < 0
	})
	out := result[:0]
	for _, p := range result {
		if len(out) == 0 || !out[len(out)-1].Contains(p.Addr()) {
			out = append(out, p)
		}
	}
	if len(out) > prefixLimit {
		return nil, errors.New("routing lists exceed 16384 subnets")
	}
	return out, nil
}
func validateSourceURL(value string) error {
	if len(value) > 2048 || strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("invalid source URL")
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("source URLs must use HTTPS without embedded credentials or fragments")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !ip.Is4() {
		return errors.New("source URL must use IPv4 or a hostname")
	}
	return nil
}
func validateSourceSpec(s sourceSpec) error {
	if len(s.ID) == 0 || len(s.ID) > 32 {
		return errors.New("invalid source identity")
	}
	for _, c := range s.ID {
		if c != '-' && c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return errors.New("invalid source identity")
		}
	}
	if utf8.RuneCountInString(s.Name) > 48 || strings.IndexFunc(s.Name, unicode.IsControl) >= 0 {
		return errors.New("source name exceeds 48 characters or contains control characters")
	}
	switch s.Kind {
	case sourceRemote:
		if err := validateSourceURL(s.URL); err != nil {
			return err
		}
		if s.ChecksumURL != "" {
			if err := validateSourceURL(s.ChecksumURL); err != nil {
				return err
			}
		}
	case sourceLocal:
		if s.URL != "" || s.ChecksumURL != "" || s.EveryHours != 0 {
			return errors.New("local lists cannot have remote settings")
		}
		return nil
	case sourceRU:
		if s.URL != "" || s.ChecksumURL != "" {
			return errors.New("preset URLs cannot be overridden")
		}
	default:
		return errors.New("unknown source type")
	}
	if s.EveryHours < 1 || s.EveryHours > 168 {
		return errors.New("update interval must be between 1 and 168 hours")
	}
	return nil
}
func contentHash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func validCache(c sourceCache) bool {
	if len(c.Raw) == 0 || len(c.Raw) > sourceByteLimit || c.Hash != contentHash(c.Raw) {
		return false
	}
	_, err := parseIPv4Text(c.Raw)
	return err == nil
}
func defaultRoutingDocument() (routingDocument, error) {
	d, err := bundledDataset()
	if err != nil {
		return routingDocument{}, err
	}
	return routingDocument{Version: 1, Mode: modeAll, Sources: []listSource{{Spec: sourceSpec{ID: "ru-direct", Kind: sourceRU, Name: "RU Direct Standard", EveryHours: 24}, Cache: sourceCache{Raw: d.Source, Hash: d.Hash, Checked: d.Generated, State: sourceReady}}}}, nil
}
func decodeRoutingDocument(raw []byte) (routingDocument, error) {
	var d routingDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > 2*sourceTotalByteLimit || decoder.Decode(&d) != nil || d.Version != 1 || !validMode(d.Mode) || len(d.Sources) > sourceLimit {
		return d, errors.New("invalid routing settings")
	}
	seen := make(map[sourceID]struct{})
	presets := 0
	total := 0
	for i := range d.Sources {
		s := &d.Sources[i]
		if err := validateSourceSpec(s.Spec); err != nil {
			return d, err
		}
		if _, ok := seen[s.Spec.ID]; ok {
			return d, errors.New("duplicate source identity")
		}
		seen[s.Spec.ID] = struct{}{}
		if s.Spec.Kind == sourceRU {
			presets++
			if presets > 1 {
				return d, errors.New("duplicate RU Direct preset")
			}
		}
		total += len(s.Cache.Raw)
		if total > sourceTotalByteLimit {
			return d, errors.New("combined lists exceed 1 MiB")
		}
		if !validCache(s.Cache) {
			if s.Spec.Kind == sourceRU {
				bundled, err := bundledDataset()
				if err != nil {
					return d, err
				}
				attempted, state := s.Cache.Attempted, s.Cache.State
				s.Cache = sourceCache{Raw: bundled.Source, Hash: bundled.Hash, Checked: bundled.Generated, Attempted: attempted, State: sourceReady}
				if state == sourceFailed {
					s.Cache.State = sourceFailed
				}
			} else {
				attempted, state := s.Cache.Attempted, s.Cache.State
				s.Cache = sourceCache{Attempted: attempted, State: sourceMissing}
				if state == sourceFailed {
					s.Cache.State = sourceFailed
				}
			}
		} else if s.Cache.State != sourceReady && s.Cache.State != sourceFailed {
			return d, errors.New("invalid source update state")
		}
		if len(s.Cache.ETag) > 1024 || len(s.Cache.Modified) > 1024 || strings.ContainsAny(s.Cache.ETag+s.Cache.Modified, "\r\n") {
			return d, errors.New("invalid HTTP cache metadata")
		}
	}
	return d, nil
}
func readRoutingDocument() (routingDocument, error) {
	raw, err := readRegular(routingStorePath, 2*sourceTotalByteLimit)
	if err != nil {
		return routingDocument{}, err
	}
	if len(raw) == 0 {
		return defaultRoutingDocument()
	}
	return decodeRoutingDocument(raw)
}
func saveRoutingDocument(d routingDocument) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if _, err := decodeRoutingDocument(raw); err != nil {
		return err
	}
	for _, s := range d.Sources {
		if len(s.Cache.Raw) > 0 && !validCache(s.Cache) {
			return errors.New("invalid source cache")
		}
	}
	return atomicWrite(routingStorePath, raw)
}
func documentPrefixes(d routingDocument) ([]netip.Prefix, error) {
	all := make([]netip.Prefix, 0)
	total := 0
	for _, s := range d.Sources {
		if !validCache(s.Cache) {
			return nil, errListsMissing
		}
		p, err := parseIPv4Text(s.Cache.Raw)
		if err != nil {
			return nil, err
		}
		all = append(all, p...)
		total += len(s.Cache.Raw)
	}
	if total > sourceTotalByteLimit {
		return nil, errors.New("combined lists exceed 1 MiB")
	}
	if len(d.Sources) == 0 && d.Mode != modeAll {
		return nil, errListsMissing
	}
	return normalizePrefixes(all)
}
func policyRevision(d routingDocument) string {
	specs := make([]sourceSpec, 0, len(d.Sources))
	for _, s := range d.Sources {
		specs = append(specs, s.Spec)
	}
	raw, err := json.Marshal(struct {
		Mode    routingMode
		Sources []sourceSpec
	}{d.Mode, specs})
	if err != nil {
		panic("routing policy encoding failed")
	}
	return contentHash(raw)
}
func dataRevision(d routingDocument) string {
	hashes := make([]string, 0, len(d.Sources))
	for _, s := range d.Sources {
		hashes = append(hashes, s.Cache.Hash)
	}
	return contentHash([]byte(string(d.Mode) + "\n" + strings.Join(hashes, "\n")))
}
func sourceIsDue(hours uint16, attempted time.Time, state sourceState, now time.Time) bool {
	interval := time.Duration(hours) * time.Hour
	if state == sourceFailed {
		interval = time.Hour
	}
	return attempted.IsZero() || now.Before(attempted) || now.Sub(attempted) >= interval
}
func printRoutingStatus() error {
	d, err := readRoutingDocument()
	if err != nil {
		return err
	}
	prefixes, listErr := documentPrefixes(d)
	state := sourceReady
	var checked time.Time
	for _, s := range d.Sources {
		if !validCache(s.Cache) {
			state = sourceMissing
			break
		}
		if s.Cache.State == sourceFailed {
			state = sourceFailed
		}
		if checked.IsZero() || s.Cache.Checked.Before(checked) {
			checked = s.Cache.Checked
		}
	}
	if listErr != nil {
		state = sourceMissing
	}
	timestamp := int64(0)
	if !checked.IsZero() {
		timestamp = checked.Unix()
	}
	fmt.Printf("routing=%s\nrouting_sources=%d\nrouting_prefixes=%d\nrouting_updated=%d\nrouting_update=%s\n", d.Mode, len(d.Sources), len(prefixes), timestamp, state)
	return nil
}
