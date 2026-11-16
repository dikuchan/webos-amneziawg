package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	maxRoutingPrefixes = 2048
	maxRoutingBytes    = 128 * 1024
	routingPrefsPath   = base + "/conf/routing.json"
	routingCachePath   = base + "/conf/ru-direct.json"
	routingUpdatePath  = base + "/run/routing-update.json"
	splitStatePath     = base + "/run/split-session.json"
	splitRouteMetric   = 42432
)

type routingMode string

const (
	routingAll routingMode = "all"
	routingRU  routingMode = "ru-direct"
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
type routingUpdateState string

const (
	updateBundled routingUpdateState = "bundled"
	updateRunning routingUpdateState = "updating"
	updateCurrent routingUpdateState = "current"
	updateFailed  routingUpdateState = "failed"
)

type routingUpdate struct {
	Attempted time.Time          `json:"attempted"`
	State     routingUpdateState `json:"state"`
}
type updateInvocation uint8

const (
	updateScheduled updateInvocation = iota
	updateManual
)

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
	if len(normalized) > maxRoutingPrefixes {
		return nil, errors.New("routing list exceeds 2048 prefixes")
	}
	return normalized, nil
}
func parseRoutingList(raw []byte) ([]netip.Prefix, error) {
	if len(raw) > maxRoutingBytes {
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
	if info.Generated.IsZero() || info.Count < 1 || info.Count > maxRoutingPrefixes || info.File.Name != "ru-standard.ipv4.txt" || info.File.Size != len(raw) || len(info.File.Hash) != 64 {
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
func loadRoutingDataset() (routingDataset, error) {
	raw, err := readRegular(routingCachePath, 256*1024)
	if err == nil && len(raw) > 0 {
		if cached, decodeErr := decodeRoutingCache(raw); decodeErr == nil {
			return cached, nil
		}
	}
	// A damaged or missing downloaded cache cannot prevent use of the bundled list.
	return bundledDataset()
}
func readRoutingMode() (routingMode, error) {
	raw, err := readRegular(routingPrefsPath, 4096)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return routingAll, nil
	}
	var value struct {
		Mode routingMode `json:"mode"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Mode != routingAll && value.Mode != routingRU {
		return "", errors.New("invalid routing preference")
	}
	return value.Mode, nil
}
func readRoutingUpdate() (routingUpdate, error) {
	raw, err := readRegular(routingUpdatePath, 4096)
	if err != nil {
		return routingUpdate{}, err
	}
	if len(raw) == 0 {
		return routingUpdate{State: updateBundled}, nil
	}
	var value routingUpdate
	if json.Unmarshal(raw, &value) != nil || value.State != updateBundled && value.State != updateRunning && value.State != updateCurrent && value.State != updateFailed {
		return routingUpdate{}, errors.New("invalid routing update state")
	}
	return value, nil
}
func saveRoutingUpdate(value routingUpdate) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicWrite(routingUpdatePath, raw)
}

type directRoute struct {
	Prefix    netip.Prefix
	Interface int
	Gateway   netip.Addr
}
type splitSession struct {
	BootID    string         `json:"bootId"`
	Interface int            `json:"interface"`
	Gateway   netip.Addr     `json:"gateway"`
	Prefixes  []netip.Prefix `json:"prefixes"`
}

func readSplitSession() (*splitSession, error) {
	raw, err := readRegular(splitStatePath, 256*1024)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var state splitSession
	if json.Unmarshal(raw, &state) != nil || state.Interface < 1 || state.Interface > 1<<31-1 || state.BootID == "" || len(state.Prefixes) > 4096 || state.Gateway.IsValid() && (!state.Gateway.Is4() || !state.Gateway.IsGlobalUnicast()) {
		return nil, errors.New("invalid split routing recovery state")
	}
	for _, prefix := range state.Prefixes {
		if !validRoutingPrefix(prefix) || prefix != prefix.Masked() {
			return nil, errors.New("invalid split routing recovery prefix")
		}
	}
	return &state, nil
}
func saveSplitSession(value *splitSession) error {
	if value == nil {
		if err := os.Remove(splitStatePath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicWrite(splitStatePath, raw)
}
func splitRouteSet(state *splitSession) map[directRoute]struct{} {
	result := make(map[directRoute]struct{})
	if state != nil {
		for _, prefix := range state.Prefixes {
			result[directRoute{prefix, state.Interface, state.Gateway}] = struct{}{}
		}
	}
	return result
}
func planDirectRoutes(actual []directRoute, ownership *splitSession, desired *splitSession) ([]directRoute, []directRoute, error) {
	owned, wanted := splitRouteSet(ownership), splitRouteSet(desired)
	have := make(map[directRoute]struct{}, len(actual))
	for _, route := range actual {
		have[route] = struct{}{}
	}
	var add, remove []directRoute
	for route := range wanted {
		if _, exists := have[route]; !exists {
			add = append(add, route)
		} else if _, allowed := owned[route]; !allowed {
			return nil, nil, errors.New("an existing route is outside our split recovery state")
		}
	}
	for route := range owned {
		if _, exists := have[route]; exists {
			if _, keep := wanted[route]; !keep {
				remove = append(remove, route)
			}
		}
	}
	return add, remove, nil
}
func syncDirectRoutes(ctx context.Context, ownership *splitSession, desired *splitSession) error {
	actual, err := kernelDirectRoutes(ctx)
	if err != nil {
		return err
	}
	add, remove, err := planDirectRoutes(actual, ownership, desired)
	if err != nil {
		return err
	}
	return mutateDirectRoutes(ctx, add, remove)
}
func applySplitPrefixes(ctx context.Context, prefixes []netip.Prefix) error {
	id, err := bootID()
	if err != nil {
		return err
	}
	old, err := readSplitSession()
	if err != nil {
		return err
	}
	if old != nil && old.BootID != id {
		old = nil
	}
	var next *splitSession
	if len(prefixes) > 0 {
		text, err := command(ctx, "ip", "-4", "route", "show", "default")
		if err != nil {
			return err
		}
		name := fieldAfter(text, "dev")
		if name == "" || name == iface || name == "wg0" {
			return errors.New("physical default route is unavailable")
		}
		network, err := net.InterfaceByName(name)
		if err != nil {
			return err
		}
		var gateway netip.Addr
		if value := fieldAfter(text, "via"); value != "" {
			gateway, err = netip.ParseAddr(value)
			if err != nil || !gateway.Is4() {
				return errors.New("physical gateway is not IPv4")
			}
		}
		next = &splitSession{BootID: id, Interface: network.Index, Gateway: gateway, Prefixes: prefixes}
		if old != nil && (old.Interface != next.Interface || old.Gateway != next.Gateway) {
			return errors.New("physical network changed; reconnect VPN before updating routes")
		}
	}
	recovery := next
	if recovery == nil {
		recovery = old
	}
	if recovery == nil {
		return nil
	}
	union := *recovery
	seen := make(map[netip.Prefix]struct{})
	for _, state := range []*splitSession{old, next} {
		if state != nil {
			for _, prefix := range state.Prefixes {
				seen[prefix] = struct{}{}
			}
		}
	}
	union.Prefixes = make([]netip.Prefix, 0, len(seen))
	for prefix := range seen {
		union.Prefixes = append(union.Prefixes, prefix)
	}
	actual, err := kernelDirectRoutes(ctx)
	if err != nil {
		return err
	}
	// Check against the previous ownership BEFORE journaling new candidates.
	add, remove, err := planDirectRoutes(actual, old, next)
	if err != nil {
		return err
	}
	if err := saveSplitSession(&union); err != nil {
		return err
	}
	changeErr := mutateDirectRoutes(ctx, add, remove)
	if changeErr == nil {
		changeErr = saveSplitSession(next)
	}
	if err := changeErr; err != nil {
		rollback, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if recoveryErr := syncDirectRoutes(rollback, &union, old); recoveryErr != nil {
			return errors.Join(err, errors.New("split routing rollback incomplete; Stop can retry cleanup"), recoveryErr)
		}
		if recoveryErr := saveSplitSession(old); recoveryErr != nil {
			return errors.Join(err, recoveryErr)
		}
		return err
	}
	return nil
}
func clearSplitRoutes(ctx context.Context) error {
	state, err := readSplitSession()
	if err != nil || state == nil {
		return err
	}
	id, err := bootID()
	if err != nil {
		return err
	}
	if state.BootID == id {
		if err := syncDirectRoutes(ctx, state, nil); err != nil {
			return err
		}
	}
	return saveSplitSession(nil)
}
func applyConfiguredRouting(ctx context.Context) error {
	mode, err := readRoutingMode()
	if err != nil {
		return err
	}
	if mode == routingAll {
		return clearSplitRoutes(ctx)
	}
	dataset, err := loadRoutingDataset()
	if err != nil {
		return err
	}
	return applySplitPrefixes(ctx, dataset.Prefixes)
}
func saveRoutingMode(mode routingMode) error {
	raw, err := json.Marshal(struct {
		Mode routingMode `json:"mode"`
	}{mode})
	if err != nil {
		return err
	}
	return atomicWrite(routingPrefsPath, raw)
}
func prefixesForMode(mode routingMode) ([]netip.Prefix, error) {
	if mode == routingAll {
		return nil, nil
	}
	dataset, err := loadRoutingDataset()
	return dataset.Prefixes, err
}
func setRoutingMode(ctx context.Context, kit string, mode routingMode) error {
	if mode != routingAll && mode != routingRU {
		return errors.New("unknown routing mode")
	}
	previous, err := readRoutingMode()
	if err != nil {
		return err
	}
	current, err := loadSession()
	if err != nil {
		return err
	}
	active := current != nil && ownsProcess(current)
	if err := stopWorker(routingWorker); err != nil {
		return err
	}
	rollback := func(cause error) error {
		recovery, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		var restoreErr error
		if active {
			prefixes, err := prefixesForMode(previous)
			restoreErr = err
			if err == nil {
				restoreErr = applySplitPrefixes(recovery, prefixes)
			}
		}
		restoreErr = errors.Join(restoreErr, saveRoutingMode(previous))
		if active && previous == routingRU {
			restoreErr = errors.Join(restoreErr, startWorker(kit, routingWorker))
		}
		return errors.Join(cause, restoreErr)
	}
	if active {
		prefixes, err := prefixesForMode(mode)
		if err != nil {
			return rollback(err)
		}
		if err := applySplitPrefixes(ctx, prefixes); err != nil {
			return rollback(err)
		}
	} else if err := clearSplitRoutes(ctx); err != nil {
		return rollback(err)
	}
	if err := saveRoutingMode(mode); err != nil {
		return rollback(err)
	}
	if mode == routingRU && active {
		if err := startWorker(kit, routingWorker); err != nil {
			return rollback(err)
		}
	}
	recordEvent("Routing mode: " + string(mode) + ".")
	return printRoutingStatus()
}
func printRoutingStatus() error {
	mode, err := readRoutingMode()
	if err != nil {
		return err
	}
	dataset, err := loadRoutingDataset()
	if err != nil {
		return err
	}
	update, err := readRoutingUpdate()
	if err != nil {
		return err
	}
	state := update.State
	if state == "" {
		state = updateBundled
	}
	fmt.Printf("routing=%s\nrouting_prefixes=%d\nrouting_updated=%d\nrouting_update=%s\n", mode, len(dataset.Prefixes), dataset.Generated.Unix(), state)
	return nil
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
	raw, err := fetchRouteFile(ctx, client, origin+"/ru-standard.ipv4.txt", maxRoutingBytes)
	if err != nil {
		return routingDataset{}, err
	}
	return decodeRoutingFeed(manifest, raw)
}
func routingUpdateDue(previous routingUpdate, now time.Time, invocation updateInvocation) bool {
	if invocation == updateManual || previous.Attempted.IsZero() {
		return true
	}
	interval := 24 * time.Hour
	if previous.State == updateFailed {
		interval = time.Hour
	}
	if previous.State == updateRunning {
		interval = 2 * time.Minute
	}
	elapsed := now.Sub(previous.Attempted)
	return elapsed < 0 || elapsed >= interval
}
func refreshRouting(ctx context.Context, invocation updateInvocation) error {
	if err := prepareRuntime(); err != nil {
		return err
	}
	// A separate lock covers downloads, without blocking Start/Stop/status.
	lock, err := os.OpenFile(base+"/run/routing-update.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errOperationBusy
	}
	now := time.Now().UTC()
	previousUpdate, err := readRoutingUpdate()
	if err != nil {
		return err
	}
	if !routingUpdateDue(previousUpdate, now, invocation) {
		return nil
	}
	if err := saveRoutingUpdate(routingUpdate{Attempted: now, State: updateRunning}); err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var candidate routingDataset
	var fetchErr error
	for _, origin := range []string{"https://raw.githubusercontent.com/kyoresuas/ru-direct/release", "https://cdn.jsdelivr.net/gh/kyoresuas/ru-direct@release"} {
		candidate, fetchErr = downloadRoutingDataset(ctx, client, origin)
		if fetchErr == nil || ctx.Err() != nil {
			break
		}
	}
	result := fetchErr
	if result == nil {
		result = withLock(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			previous, err := loadRoutingDataset()
			if err != nil {
				return err
			}
			if candidate.Generated.Before(previous.Generated) || candidate.Generated.After(now.Add(24*time.Hour)) || len(candidate.Prefixes) < len(previous.Prefixes)/2 {
				return errors.New("routing update is older, future-dated or unexpectedly incomplete")
			}
			mode, err := readRoutingMode()
			if err != nil {
				return err
			}
			current, err := loadSession()
			if err != nil {
				return err
			}
			active := mode == routingRU && current != nil && ownsProcess(current)
			if active {
				if err := applySplitPrefixes(ctx, candidate.Prefixes); err != nil {
					return err
				}
			}
			candidate.Checked = now
			raw, err := json.Marshal(candidate)
			if err == nil {
				err = atomicWrite(routingCachePath, raw)
			}
			if err != nil && active {
				recovery, done := context.WithTimeout(context.Background(), 10*time.Second)
				defer done()
				return errors.Join(err, applySplitPrefixes(recovery, previous.Prefixes))
			}
			return err
		})
	}
	if errors.Is(result, errOperationBusy) || errors.Is(ctx.Err(), context.Canceled) {
		// Cancellation/another operation is not a failed feed; retry next minute.
		return errors.Join(result, saveRoutingUpdate(previousUpdate))
	}
	if result != nil {
		recordEvent("Routing list update failed; previous list retained.")
		return errors.Join(result, saveRoutingUpdate(routingUpdate{Attempted: now, State: updateFailed}))
	}
	if err := saveRoutingUpdate(routingUpdate{Attempted: now, State: updateCurrent}); err != nil {
		return err
	}
	recordEvent("Routing list updated.")
	return nil
}

func runRoutingUpdater() error {
	if err := prepareRuntime(); err != nil {
		return err
	}
	if err := waitForWorkerIdentity(routingWorker); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		mode, err := readRoutingMode()
		if err != nil {
			return err
		}
		current, err := loadSession()
		if err != nil {
			return err
		}
		if mode != routingRU || current == nil || !ownsProcess(current) {
			return nil
		}
		update, done := context.WithTimeout(ctx, 45*time.Second)
		err = refreshRouting(update, updateScheduled)
		done()
		if err != nil && ctx.Err() == nil {
			recordEvent("Automatic routing update did not complete; cached list remains active.")
		}
		timer.Reset(time.Minute)
	}
}
