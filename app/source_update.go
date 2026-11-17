package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"
)

type updateInvocation uint8

const (
	updateScheduled updateInvocation = iota
	updateManual
	updateMissing
)

type downloadAccess uint8

const (
	downloadNormal downloadAccess = iota
	downloadBootstrap
)

func withUpdateLock(fn func() error) error {
	f, err := os.OpenFile(base+"/run/source-update.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer closeResource(f)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errOperationBusy
	}
	return fn()
}

func sourceURLs(s sourceSpec) []string {
	switch s.Kind {
	case sourceRemote:
		r := []string{
			s.URL,
		}
		if s.ChecksumURL != "" {
			r = append(r, s.ChecksumURL)
		}
		return r
	case sourceRU:
		return []string{
			"https://raw.githubusercontent.com/kyoresuas/ru-direct/release/manifest.json",
			"https://cdn.jsdelivr.net/gh/kyoresuas/ru-direct@release/manifest.json",
		}
	default:
		return nil
	}
}

func resolveSourceHosts(ctx context.Context, sources []listSource) (map[string][]netip.Addr, []netip.Addr) {
	hosts := make(map[string][]netip.Addr)
	for _, source := range sources {
		for _, raw := range sourceURLs(source.Spec) {
			u, err := url.Parse(raw)
			if err == nil {
				hosts[u.Hostname()] = nil
			}
		}
	}
	var all []netip.Addr
	for host := range hosts {
		lookup, cancel := context.WithTimeout(ctx, 4*time.Second)
		ips, err := (&net.Resolver{
			PreferGo: true,
		}).LookupNetIP(lookup, "ip4", host)
		cancel()
		if err != nil {
			continue
		}
		for _, ip := range ips {
			ip = ip.Unmap()
			if ip.Is4() {
				hosts[host] = append(hosts[host], ip)
				all = append(all, ip)
			}
			if len(hosts[host]) == 4 {
				break
			}
		}
	}
	return hosts, all
}

func sourceHTTPClient(hosts map[string][]netip.Addr) (*http.Client, *http.Transport) {
	dialer := &net.Dialer{
		Timeout: 4 * time.Second,
	}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("invalid source address")
			}
			for _, ip := range hosts[host] {
				conn, err := dialer.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				if ctx.Err() != nil {
					break
				}
			}
			return nil, errors.New("source is unavailable")
		},
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, transport
}

func fetchText(ctx context.Context, client *http.Client, address string, cache sourceCache) ([]byte, string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, "", "", errors.New("invalid source request")
	}
	if validCache(cache) {
		if cache.ETag != "" {
			request.Header.Set("If-None-Match", cache.ETag)
		}
		if cache.Modified != "" {
			request.Header.Set("If-Modified-Since", cache.Modified)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", "", errors.New("source download failed")
	}
	defer closeResource(response.Body)
	if response.StatusCode == http.StatusNotModified && validCache(cache) {
		return cache.Raw, cache.ETag, cache.Modified, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, "", "", errors.New("source returned an HTTP error")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, sourceByteLimit+1))
	if err != nil || len(raw) > sourceByteLimit {
		return nil, "", "", errors.New("source download exceeds 256 KiB or is incomplete")
	}
	etag, modified := response.Header.Get("ETag"), response.Header.Get("Last-Modified")
	if len(etag) > 1024 || len(modified) > 1024 {
		return nil, "", "", errors.New("source HTTP metadata exceeds limit")
	}
	return raw, etag, modified, nil
}

func checksumFor(raw []byte, filename string) (string, error) {
	if len(raw) > 8192 {
		return "", errors.New("checksum file is too large")
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		hash := strings.ToLower(fields[0])
		if len(hash) != 64 {
			continue
		}
		valid := true
		for _, c := range hash {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		if len(fields) == 1 {
			return hash, nil
		}
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == filename {
			return hash, nil
		}
	}
	return "", errors.New("checksum file has no matching SHA-256")
}

func downloadSource(ctx context.Context, client *http.Client, source listSource, now time.Time) (sourceCache, error) {
	if source.Spec.Kind == sourceLocal {
		return source.Cache, nil
	}
	var raw []byte
	var hash, etag, modified string
	if source.Spec.Kind == sourceRU {
		var dataset routingDataset
		var err error
		for _, origin := range []string{
			"https://raw.githubusercontent.com/kyoresuas/ru-direct/release",
			"https://cdn.jsdelivr.net/gh/kyoresuas/ru-direct@release",
		} {
			dataset, err = downloadRoutingDataset(ctx, client, origin)
			if err == nil || ctx.Err() != nil {
				break
			}
		}
		if err != nil {
			return sourceCache{}, err
		}
		raw = dataset.Source
		hash = dataset.Hash
	} else {
		var err error
		raw, etag, modified, err = fetchText(ctx, client, source.Spec.URL, source.Cache)
		if err != nil {
			return sourceCache{}, err
		}
		if _, err := parseIPv4Text(raw); err != nil {
			return sourceCache{}, err
		}
		hash = contentHash(raw)
		if source.Spec.ChecksumURL != "" {
			sum, err := fetchRouteFile(ctx, client, source.Spec.ChecksumURL, 8192)
			if err != nil {
				return sourceCache{}, err
			}
			u, err := url.Parse(source.Spec.URL)
			if err != nil {
				return sourceCache{}, errors.New("invalid source URL")
			}
			expected, err := checksumFor(sum, path.Base(u.Path))
			if err != nil {
				return sourceCache{}, err
			}
			if expected != hash {
				return sourceCache{}, errors.New("source SHA-256 does not match")
			}
		}
	}
	return sourceCache{
		Raw:       raw,
		Hash:      hash,
		ETag:      etag,
		Modified:  modified,
		Attempted: now,
		Checked:   now,
		State:     sourceReady,
	}, nil
}

func shouldRefreshSource(s listSource, invocation updateInvocation, now time.Time) bool {
	if s.Spec.Kind == sourceLocal {
		return false
	}
	return invocation == updateManual || invocation == updateMissing && !validCache(s.Cache) || invocation == updateScheduled && sourceIsDue(s.Spec.EveryHours, s.Cache.Attempted, s.Cache.State, now)
}

func hasRemoteSources(d routingDocument) bool {
	for _, s := range d.Sources {
		if s.Spec.Kind != sourceLocal {
			return true
		}
	}
	return false
}

func refreshDocument(ctx context.Context, d routingDocument, invocation updateInvocation, access downloadAccess) (routingDocument, error) {
	now := time.Now().UTC()
	selected := make([]int, 0, len(d.Sources))
	sources := make([]listSource, 0, len(d.Sources))
	for i, s := range d.Sources {
		if s.Spec.Kind == sourceLocal {
			continue
		}
		needed := shouldRefreshSource(s, invocation, now)
		if needed {
			selected = append(selected, i)
			sources = append(sources, s)
		}
	}
	if len(selected) == 0 {
		return d, nil
	}
	batch, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	hosts, ips := resolveSourceHosts(batch, sources)
	var owner *routeOwner
	if access == downloadNormal {
		var err error
		owner, err = prepareDownloadRoutes(batch, ips)
		if err != nil {
			return d, err
		}
		defer func() {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			var err error
			if ctx.Err() != nil {
				// Stop holds the operation lock while waiting for this worker.
				// It takes over cleanup after the worker exits.
				err = withLock(func() error { return clearDownloadOwner(cleanup, owner) })
			} else {
				err = releaseDownloadRoutes(cleanup, owner)
			}
			if err != nil && !errors.Is(err, errOperationBusy) {
				recordEvent("Temporary download route cleanup needs retry.")
			}
		}()
	}
	client, transport := sourceHTTPClient(hosts)
	defer transport.CloseIdleConnections()
	type result struct {
		index int
		cache sourceCache
		err   error
	}
	jobs := make(chan int)
	results := make(chan result, len(sources))
	var workers sync.WaitGroup
	for worker := 0; worker < 2; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				cache, err := downloadSource(batch, client, sources[index], now)
				results <- result{
					index,
					cache,
					err,
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range sources {
			select {
			case jobs <- i:
			case <-batch.Done():
				return
			}
		}
	}()
	go func() { workers.Wait(); close(results) }()
	next := d
	next.Sources = append([]listSource(nil), d.Sources...)
	var failures []error
	for result := range results {
		index := selected[result.index]
		if result.err != nil {
			next.Sources[index].Cache.Attempted = now
			next.Sources[index].Cache.State = sourceFailed
			failures = append(failures, result.err)
		} else {
			next.Sources[index].Cache = result.cache
		}
	}
	if errors.Is(batch.Err(), context.Canceled) {
		return d, batch.Err()
	}
	if errors.Is(batch.Err(), context.DeadlineExceeded) {
		for _, index := range selected {
			if next.Sources[index].Cache.Attempted != now {
				next.Sources[index].Cache.Attempted = now
				next.Sources[index].Cache.State = sourceFailed
			}
		}
		failures = append(failures, batch.Err())
	}
	return next, errors.Join(failures...)
}

func refreshRouting(ctx context.Context, invocation updateInvocation) error {
	if err := prepareRuntime(); err != nil {
		return err
	}
	return withUpdateLock(func() error {
		old, err := readRoutingDocument()
		if err != nil {
			return err
		}
		due := false
		for _, s := range old.Sources {
			if shouldRefreshSource(s, invocation, time.Now().UTC()) {
				due = true
				break
			}
		}
		if !due {
			return nil
		}
		next, fetchErr := refreshDocument(ctx, old, invocation, downloadNormal)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = withLock(func() error {
			current, err := readRoutingDocument()
			if err != nil {
				return err
			}
			for i := range current.Sources {
				for _, candidate := range next.Sources {
					if current.Sources[i].Spec == candidate.Spec {
						current.Sources[i].Cache = candidate.Cache
					}
				}
			}
			return commitRoutingDocument(ctx, current)
		})
		if err != nil {
			return err
		}
		if fetchErr != nil {
			recordEvent("Some routing sources could not be updated; saved lists retained.")
			return fetchErr
		}
		return nil
	})
}

func startupRoutingSources(ctx context.Context) error {
	d, err := readRoutingDocument()
	if err != nil || d.Mode == modeAll {
		return err
	}
	if _, err := documentPrefixes(d); err == nil {
		return nil
	}
	return withUpdateLock(func() error {
		next, err := refreshDocument(ctx, d, updateMissing, downloadBootstrap)
		if err != nil {
			return err
		}
		if _, err := documentPrefixes(next); err != nil {
			return err
		}
		return saveRoutingDocument(next)
	})
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
		current, err := activeRoutingSession()
		if err != nil || current == nil {
			return err
		}
		if routingNeedsRecovery() {
			return errRoutingRecovery
		}
		document, err := readRoutingDocument()
		if err != nil {
			return err
		}
		if !hasRemoteSources(document) {
			return nil
		}
		update, done := context.WithTimeout(ctx, 50*time.Second)
		err = refreshRouting(update, updateScheduled)
		done()
		if err != nil && ctx.Err() == nil && !errors.Is(err, errOperationBusy) {
			recordEvent("Routing update failed; saved lists remain active.")
		}
		timer.Reset(time.Minute)
	}
}

func saveEditorRouting(ctx context.Context, expected string, next routingDocument) error {
	return withUpdateLock(func() error {
		current, err := readRoutingDocument()
		if err != nil {
			return err
		}
		if policyRevision(current) != expected {
			return errRoutingConflict
		}
		for i := range next.Sources {
			for _, old := range current.Sources {
				if next.Sources[i].Spec.ID == old.Spec.ID && next.Sources[i].Spec.Kind == old.Spec.Kind && next.Sources[i].Spec.URL == old.Spec.URL && next.Sources[i].Spec.ChecksumURL == old.Spec.ChecksumURL && len(next.Sources[i].Cache.Raw) == 0 {
					next.Sources[i].Cache = old.Cache
				}
			}
		}
		candidate, err := refreshDocument(ctx, next, updateMissing, downloadNormal)
		if err != nil {
			return err
		}
		if _, err := documentPrefixes(candidate); err != nil && len(candidate.Sources) > 0 {
			return err
		}
		return withLock(func() error {
			latest, err := readRoutingDocument()
			if err != nil {
				return err
			}
			if policyRevision(latest) != expected {
				return errRoutingConflict
			}
			return commitRoutingDocument(ctx, candidate)
		})
	})
}
