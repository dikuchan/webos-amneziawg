package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"strconv"
	"time"
)

const (
	splitRouteMetric = 42432
	splitStatePath   = base + "/run/routes.json"
	routeLimit       = 2*prefixLimit + 256
)

type managedRoute struct {
	Prefix    netip.Prefix `json:"prefix"`
	Interface int          `json:"interface"`
	Gateway   netip.Addr   `json:"gateway"`
}
type physicalNetwork struct {
	Index   int
	Gateway netip.Addr
	LAN     []netip.Prefix
}
type routePhase string

const (
	routesStable   routePhase = "stable"
	routesApplying routePhase = "applying"
)

type routeOwner struct {
	PID     int    `json:"pid"`
	Started string `json:"started"`
}
type managedSession struct {
	BootID   string         `json:"bootId"`
	Phase    routePhase     `json:"phase"`
	Revision string         `json:"revision"`
	Routes   []managedRoute `json:"routes"`
	Data     []managedRoute `json:"data"`
	Control  []managedRoute `json:"control,omitempty"`
	Owner    *routeOwner    `json:"owner,omitempty"`
}

var errRoutingRecovery = errors.New("routing recovery required; disconnect and reconnect VPN")

func ownerAlive(owner *routeOwner) bool {
	return owner != nil && owner.PID > 1 && owner.Started != "" && processStart(owner.PID) == owner.Started
}
func sameOwner(a, b *routeOwner) bool { return a != nil && b != nil && *a == *b }
func routeSet(routes []managedRoute) map[managedRoute]struct{} {
	r := make(map[managedRoute]struct{}, len(routes))
	for _, v := range routes {
		r[v] = struct{}{}
	}
	return r
}
func planManagedRoutes(actual, owned, desired []managedRoute) ([]managedRoute, []managedRoute, error) {
	have, ours, wanted := routeSet(actual), routeSet(owned), routeSet(desired)
	var add, remove []managedRoute
	for r := range wanted {
		if _, ok := have[r]; !ok {
			add = append(add, r)
		} else if _, ok := ours[r]; !ok {
			return nil, nil, errors.New("an existing route is outside our recovery state")
		}
	}
	for r := range ours {
		if _, ok := have[r]; ok {
			if _, keep := wanted[r]; !keep {
				remove = append(remove, r)
			}
		}
	}
	return add, remove, nil
}
func readManagedSession() (*managedSession, error) {
	raw, err := readRegular(splitStatePath, 8*1024*1024)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var s managedSession
	if json.Unmarshal(raw, &s) != nil || s.BootID == "" || (s.Phase != routesStable && s.Phase != routesApplying) || len(s.Routes) > 2*routeLimit || len(s.Data) > routeLimit || len(s.Control) > 128 {
		return nil, errors.New("invalid routing recovery state")
	}
	for _, r := range append(append(append([]managedRoute(nil), s.Routes...), s.Data...), s.Control...) {
		if !r.Prefix.IsValid() || !r.Prefix.Addr().Is4() || r.Prefix != r.Prefix.Masked() || r.Interface < 1 || r.Interface > 1<<31-1 || r.Gateway.IsValid() && !r.Gateway.Is4() {
			return nil, errors.New("invalid routing recovery tuple")
		}
	}
	return &s, nil
}
func saveManagedSession(s *managedSession) error {
	if s == nil {
		err := os.Remove(splitStatePath)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicWrite(splitStatePath, raw)
}
func mergeRoutes(data, control []managedRoute) []managedRoute {
	byPrefix := make(map[netip.Prefix]managedRoute, len(data)+len(control))
	for _, r := range data {
		byPrefix[r.Prefix] = r
	}
	for _, r := range control {
		byPrefix[r.Prefix] = r
	}
	out := make([]managedRoute, 0, len(byPrefix))
	for _, r := range byPrefix {
		out = append(out, r)
	}
	return out
}
func applyManagedSession(ctx context.Context, next *managedSession) error {
	old, err := readManagedSession()
	if err != nil {
		return err
	}
	id, err := bootID()
	if err != nil {
		return err
	}
	if old != nil && old.BootID != id {
		old = nil
	}
	var owned, desired []managedRoute
	if old != nil {
		owned = old.Routes
	}
	if next != nil {
		next.BootID = id
		next.Phase = routesStable
		next.Routes = mergeRoutes(next.Data, next.Control)
		desired = next.Routes
	}
	actual, err := kernelManagedRoutes(ctx)
	if err != nil {
		return err
	}
	add, remove, err := planManagedRoutes(actual, owned, desired)
	if err != nil {
		return err
	}
	union := routeSet(append(append([]managedRoute(nil), owned...), desired...))
	journal := &managedSession{BootID: id, Phase: routesApplying}
	for r := range union {
		journal.Routes = append(journal.Routes, r)
	}
	if err := saveManagedSession(journal); err != nil {
		return err
	}
	result := mutateManagedRoutes(ctx, add, remove)
	if result == nil {
		result = saveManagedSession(next)
	}
	if result == nil {
		return nil
	}
	rollback, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	actual, recoveryErr := kernelManagedRoutes(rollback)
	if recoveryErr == nil {
		var a, d []managedRoute
		a, d, recoveryErr = planManagedRoutes(actual, journal.Routes, owned)
		if recoveryErr == nil {
			recoveryErr = mutateManagedRoutes(rollback, a, d)
		}
	}
	if recoveryErr == nil {
		recoveryErr = saveManagedSession(old)
	}
	return errors.Join(result, recoveryErr)
}
func clearSplitRoutes(ctx context.Context) error { return applyManagedSession(ctx, nil) }
func inspectPhysicalNetwork(ctx context.Context) (physicalNetwork, error) {
	text, err := command(ctx, "ip", "-4", "route", "show", "default")
	if err != nil {
		return physicalNetwork{}, err
	}
	name := fieldAfter(text, "dev")
	if name == "" || name == iface || name == "wg0" {
		return physicalNetwork{}, errors.New("physical default route is unavailable")
	}
	device, err := net.InterfaceByName(name)
	if err != nil {
		return physicalNetwork{}, err
	}
	network := physicalNetwork{Index: device.Index}
	if value := fieldAfter(text, "via"); value != "" {
		network.Gateway, err = netip.ParseAddr(value)
		if err != nil || !network.Gateway.Is4() {
			return network, errors.New("physical gateway is not IPv4")
		}
	}
	addrs, err := device.Addrs()
	if err != nil {
		return network, err
	}
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err == nil && p.Addr().Is4() {
			network.LAN = append(network.LAN, p.Masked())
		}
		if len(network.LAN) > 16 {
			return network, errors.New("too many physical LAN addresses")
		}
	}
	return network, nil
}
func splitPrefix(p netip.Prefix) (netip.Prefix, netip.Prefix) {
	bits := p.Bits() + 1
	left := netip.PrefixFrom(p.Addr(), bits)
	v := p.Addr().As4()
	pos := bits - 1
	v[pos/8] |= byte(1 << uint(7-pos%8))
	return left, netip.PrefixFrom(netip.AddrFrom4(v), bits)
}
func withoutLAN(p netip.Prefix, lan []netip.Prefix, out *[]netip.Prefix) error {
	if len(*out) > routeLimit {
		return errors.New("routing plan is too large")
	}
	for _, local := range lan {
		if !p.Overlaps(local) {
			continue
		}
		if local.Bits() <= p.Bits() {
			return nil
		}
		left, right := splitPrefix(p)
		if err := withoutLAN(left, lan, out); err != nil {
			return err
		}
		return withoutLAN(right, lan, out)
	}
	if p.Bits() == 0 {
		left, right := splitPrefix(p)
		*out = append(*out, left, right)
	} else {
		*out = append(*out, p)
	}
	return nil
}
func compileDataRoutes(mode routingMode, prefixes []netip.Prefix, network physicalNetwork, tunnelIndex int) ([]managedRoute, error) {
	if !validMode(mode) || network.Index < 1 || tunnelIndex < 1 {
		return nil, errors.New("invalid routing plan")
	}
	byPrefix := make(map[netip.Prefix]managedRoute)
	if mode != modeInclude {
		for _, text := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
			p := netip.MustParsePrefix(text)
			byPrefix[p] = managedRoute{Prefix: p, Interface: tunnelIndex}
		}
	}
	if mode != modeAll {
		var filtered []netip.Prefix
		for _, p := range prefixes {
			if err := withoutLAN(p, network.LAN, &filtered); err != nil {
				return nil, err
			}
		}
		for _, p := range filtered {
			r := managedRoute{Prefix: p, Interface: tunnelIndex}
			if mode == modeExclude {
				r.Interface = network.Index
				r.Gateway = network.Gateway
			}
			byPrefix[p] = r
		}
	}
	if len(byPrefix) > routeLimit {
		return nil, errors.New("routing plan is too large")
	}
	out := make([]managedRoute, 0, len(byPrefix))
	for _, r := range byPrefix {
		out = append(out, r)
	}
	return out, nil
}
func activeRoutingSession() (*session, error) {
	s, err := loadSession()
	if err != nil {
		return nil, err
	}
	id, err := bootID()
	if err != nil {
		return nil, err
	}
	if s == nil || s.BootID != id || !ownsProcess(s) {
		return nil, nil
	}
	return s, nil
}
func applyDocumentRoutes(ctx context.Context, d routingDocument) error {
	current, err := activeRoutingSession()
	if err != nil || current == nil {
		return err
	}
	network, err := inspectPhysicalNetwork(ctx)
	if err != nil {
		return err
	}
	index, err := strconv.Atoi(current.IfIndex)
	if err != nil {
		return err
	}
	var prefixes []netip.Prefix
	if d.Mode != modeAll {
		prefixes, err = documentPrefixes(d)
		if err != nil {
			return err
		}
	}
	data, err := compileDataRoutes(d.Mode, prefixes, network, index)
	if err != nil {
		return err
	}
	old, err := readManagedSession()
	if err != nil {
		return err
	}
	next := &managedSession{Revision: dataRevision(d), Data: data}
	if old != nil && ownerAlive(old.Owner) {
		next.Control = old.Control
		next.Owner = old.Owner
	}
	return applyManagedSession(ctx, next)
}
func commitRoutingDocument(ctx context.Context, next routingDocument) error {
	if _, err := documentPrefixes(next); err != nil && next.Mode != modeAll {
		return err
	}
	old, err := readRoutingDocument()
	if err != nil {
		return err
	}
	if err := applyDocumentRoutes(ctx, next); err != nil {
		return err
	}
	if err := saveRoutingDocument(next); err != nil {
		recovery, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		return errors.Join(err, applyDocumentRoutes(recovery, old))
	}
	current, err := activeRoutingSession()
	if err != nil {
		return err
	}
	if current != nil && hasRemoteSources(next) {
		kit, err := kitDir()
		if err != nil {
			return err
		}
		if err := startWorker(kit, routingWorker); err != nil {
			return err
		}
	}
	return nil
}
func setRoutingMode(ctx context.Context, kit string, mode routingMode) error {
	if !validMode(mode) {
		return errors.New("unknown routing mode")
	}
	d, err := readRoutingDocument()
	if err != nil {
		return err
	}
	d.Mode = mode
	if err := commitRoutingDocument(ctx, d); err != nil {
		return err
	}
	recordEvent("Routing mode: " + string(mode) + ".")
	return printRoutingStatus()
}
func applyConfiguredRouting(ctx context.Context) error {
	d, err := readRoutingDocument()
	if err != nil {
		return err
	}
	return applyDocumentRoutes(ctx, d)
}
func applyBootstrapRouting(ctx context.Context) error {
	network, err := inspectPhysicalNetwork(ctx)
	if err != nil {
		return err
	}
	s, err := activeRoutingSession()
	if err != nil {
		return err
	}
	if s == nil {
		return errors.New("VPN daemon is unavailable")
	}
	index, err := strconv.Atoi(s.IfIndex)
	if err != nil {
		return err
	}
	data, err := compileDataRoutes(modeAll, nil, network, index)
	if err != nil {
		return err
	}
	return applyManagedSession(ctx, &managedSession{Revision: "bootstrap", Data: data})
}
func routingNeedsRecovery() bool {
	s, err := readManagedSession()
	if err != nil {
		return true
	}
	if s == nil {
		return false
	}
	d, err := readRoutingDocument()
	return err != nil || s.Phase != routesStable || s.Revision != dataRevision(d)
}
func prepareDownloadRoutes(ctx context.Context, ips []netip.Addr) (*routeOwner, error) {
	var owner *routeOwner
	err := withLock(func() error {
		s, err := activeRoutingSession()
		if err != nil || s == nil {
			return err
		}
		state, err := readManagedSession()
		if err != nil {
			return err
		}
		if state == nil || routingNeedsRecovery() {
			return errRoutingRecovery
		}
		index, err := strconv.Atoi(s.IfIndex)
		if err != nil {
			return err
		}
		owner = &routeOwner{PID: os.Getpid(), Started: processStart(os.Getpid())}
		next := *state
		next.Owner = owner
		next.Control = nil
		for _, ip := range ips {
			if ip.IsPrivate() || ip.IsLoopback() || !ip.IsGlobalUnicast() {
				continue
			}
			next.Control = append(next.Control, managedRoute{Prefix: netip.PrefixFrom(ip, 32), Interface: index})
		}
		return applyManagedSession(ctx, &next)
	})
	return owner, err
}
func releaseDownloadRoutes(ctx context.Context, owner *routeOwner) error {
	if owner == nil {
		return nil
	}
	for {
		err := withLock(func() error { return clearDownloadOwner(ctx, owner) })
		if !errors.Is(err, errOperationBusy) {
			return err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func clearDownloadOwner(ctx context.Context, owner *routeOwner) error {
	state, err := readManagedSession()
	if err != nil || state == nil {
		return err
	}
	if !sameOwner(state.Owner, owner) {
		return nil
	}
	if state.Phase != routesStable {
		return errRoutingRecovery
	}
	next := *state
	next.Control = nil
	next.Owner = nil
	return applyManagedSession(ctx, &next)
}
func clearOrphanDownloadRoutes(ctx context.Context) error {
	state, err := readManagedSession()
	if err != nil || state == nil || state.Owner == nil || ownerAlive(state.Owner) {
		return err
	}
	return clearDownloadOwner(ctx, state.Owner)
}
