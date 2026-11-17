package main

import (
	"errors"
	"net/netip"
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
