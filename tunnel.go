package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func addOwnedRoute(ctx context.Context, s *session, args []string) error {
	args = append(args, "proto", routeProtocol, "metric", routeMetric)
	s.Routes = append(s.Routes, args)
	if err := saveSession(s); err != nil {
		return err
	}
	_, err := command(ctx, "ip", append([]string{"-4", "route", "add"}, args...)...)
	return err
}

func dns(ctx context.Context, kit, action string) error {
	out, err := command(ctx, "/bin/sh", filepath.Join(kit, "scripts", "dns.sh"), action)
	if out != "" {
		fmt.Println(out)
	}
	return err
}

func stop(ctx context.Context, kit string) error {
	if err := closeUpload(); err != nil {
		return err
	}
	if err := stopWorker(routingWorker); err != nil {
		return err
	}
	if err := clearSplitRoutes(ctx); err != nil {
		return fmt.Errorf("split routing cleanup failed; retry Stop: %w", err)
	}
	// DNS recovery must succeed before removing its recovery directory or routes.
	if err := dns(ctx, kit, "restore"); err != nil {
		return fmt.Errorf("DNS recovery failed; retry Stop: %w", err)
	}
	s, err := loadSession()
	if err != nil {
		return err
	}
	if s == nil {
		fmt.Println("state=stopped\nNo owned session; foreign interfaces were left alone.")
		return nil
	}
	id, err := bootID()
	if err != nil {
		return err
	}
	if s.BootID != id {
		if err := os.Remove(statePath); err != nil {
			return err
		}
		fmt.Println("state=stopped\nRemoved stale recovery state from an earlier boot.")
		return nil
	}
	var failures []error
	for i := len(s.Routes) - 1; i >= 0; i-- {
		args := s.Routes[i]
		// Only exact routes bearing this client's protocol, metric and next hop.
		// A missing route is expected after its TUN interface disappears.
		if _, err := command(ctx, "ip", append([]string{"-4", "route", "del"}, args...)...); err != nil {
			out, checkErr := command(ctx, "ip", "-4", "route", "show", "proto", routeProtocol)
			if checkErr != nil || strings.Contains(out, args[0]+" ") {
				failures = append(failures, errors.New("cannot remove an owned route"))
			}
		}
	}
	if ownsProcess(s) {
		if err := syscall.Kill(s.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			failures = append(failures, err)
		}
		for n := 0; n < 30 && ownsProcess(s); n++ {
			time.Sleep(100 * time.Millisecond)
		}
		if ownsProcess(s) {
			if err := syscall.Kill(s.PID, syscall.SIGKILL); err != nil {
				failures = append(failures, err)
			}
		}
	}
	if s.IfIndex != "" {
		b, err := os.ReadFile("/sys/class/net/" + iface + "/ifindex")
		if err == nil && strings.TrimSpace(string(b)) == s.IfIndex {
			if _, err := command(ctx, "ip", "link", "del", iface); err != nil {
				if _, statErr := os.Stat("/sys/class/net/" + iface); !os.IsNotExist(statErr) {
					failures = append(failures, err)
				}
			}
		}
	}
	if s.IPv6 != nil {
		path := "/proc/sys/net/ipv6/conf/" + s.IPv6.Device + "/disable_ipv6"
		b, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(b)) == "1" {
			if s.IPv6.Previous != "1" {
				if err := os.WriteFile(path, []byte(s.IPv6.Previous+"\n"), 0o600); err != nil {
					failures = append(failures, err)
				}
			}
		} else if err != nil {
			failures = append(failures, err)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("cleanup incomplete; recovery state retained: %w", err)
	}
	if err := os.Remove(statePath); err != nil {
		return err
	}
	if err := os.Remove(base + "/run/setconf"); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Println("state=stopped\nOriginal DNS and IPv6 state restored; ordinary WG was not changed.")
	return nil
}

func handshake(ctx context.Context, awg string) (int64, error) {
	out, err := command(ctx, awg, "show", iface, "latest-handshakes")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, errors.New("unexpected handshake status")
	}
	n, err := strconv.ParseInt(fields[1], 10, 64)
	return n, err
}

func start(ctx context.Context, kit string) (result error) {
	if _, err := command(ctx, "ip", "link", "show", "awgtest0"); err == nil {
		return errLegacyActive
	}
	if err := closeUpload(); err != nil {
		return err
	}
	if _, err := command(ctx, "ip", "link", "show", "wg0"); err == nil {
		return errors.New("ordinary wg0 is active; stop WG manually before starting AWG")
	}
	saved, err := loadSession()
	if err != nil {
		return err
	}
	id, err := bootID()
	if err != nil {
		return err
	}
	if saved != nil {
		if saved.BootID == id {
			return errors.New("an AWG session exists; use Stop before Start")
		}
		if err := stop(ctx, kit); err != nil {
			return err
		}
	}
	if _, err := command(ctx, "ip", "link", "show", iface); err == nil {
		return errors.New("awg0 already exists without our session; leaving it alone")
	}
	_, c, err := readConfig(configPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		return errors.New("/dev/net/tun is unavailable")
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	ips, err := (&net.Resolver{PreferGo: true}).LookupNetIP(resolveCtx, "ip4", c.Host)
	cancel()
	if err != nil || len(ips) == 0 || !ips[0].IsGlobalUnicast() {
		return errors.New("cannot resolve the Endpoint to a unicast IPv4 address using the original DNS")
	}
	endpoint := ips[0].Unmap()
	path, err := command(ctx, "ip", "-4", "route", "get", endpoint.String())
	if err != nil {
		return err
	}
	dev, via := fieldAfter(path, "dev"), fieldAfter(path, "via")
	if dev == "" || dev == iface || dev == "wg0" {
		return errors.New("endpoint is not routed through a physical interface")
	}
	defaults, err := command(ctx, "ip", "-4", "route", "show", "default")
	if err != nil {
		return err
	}
	physical := fieldAfter(defaults, "dev")
	if physical == "" || physical == "wg0" || physical == iface {
		return errors.New("cannot identify the original default interface")
	}
	s := &session{BootID: id, Binary: filepath.Join(kit, "bin", "amneziawg-go")}
	if err := saveSession(s); err != nil {
		return err
	}
	defer func() {
		if result != nil {
			fmt.Println("Start failed; rolling back the AWG session.")
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cleanupCancel()
			if cleanupErr := stop(cleanupCtx, kit); cleanupErr != nil {
				result = errors.Join(result, cleanupErr)
			}
		}
	}()
	args := []string{endpoint.String() + "/32"}
	if via != "" {
		args = append(args, "via", via)
	}
	args = append(args, "dev", dev)
	if err := addOwnedRoute(ctx, s, args); err != nil {
		return err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	// Stop owns the detached daemon after its process identity is persisted.
	child := exec.CommandContext(context.Background(), s.Binary, "-f", iface)
	child.Stdin, child.Stdout, child.Stderr = null, null, null
	child.Env = append(os.Environ(), "LOG_LEVEL=error")
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = child.Start()
	if closeErr := null.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("cannot execute the ARM userspace daemon")
	}
	s.PID, s.ProcessStart = child.Process.Pid, processStart(child.Process.Pid)
	if err := saveSession(s); err != nil {
		return errors.Join(err, terminateChild(child))
	}
	if err := child.Process.Release(); err != nil {
		return err
	}
	ready := false
	for n := 0; n < 50; n++ {
		if !ownsProcess(s) {
			return errors.New("amneziawg-go exited before interface creation; check CPU/kernel/TUN compatibility")
		}
		if b, err := os.ReadFile("/sys/class/net/" + iface + "/ifindex"); err == nil {
			s.IfIndex = strings.TrimSpace(string(b))
			if _, err := os.Stat("/var/run/amneziawg/" + iface + ".sock"); err == nil {
				ready = true
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := saveSession(s); err != nil {
		return err
	}
	if !ready {
		return errors.New("userspace daemon did not create its interface/control socket in time")
	}
	wirePath := base + "/run/setconf"
	if err := atomicWrite(wirePath, c.setconf(net.JoinHostPort(endpoint.String(), strconv.Itoa(int(c.Port))))); err != nil {
		return err
	}
	awg := filepath.Join(kit, "bin", "awg")
	_, err = command(ctx, awg, "setconf", iface, wirePath)
	removeErr := os.Remove(wirePath)
	if err != nil {
		return fmt.Errorf("official awg rejected the protocol settings: %w", err)
	}
	if removeErr != nil {
		return removeErr
	}
	for _, address := range c.Addresses {
		if _, err := command(ctx, "ip", "-4", "addr", "add", address.String(), "dev", iface); err != nil {
			return err
		}
	}
	if _, err := command(ctx, "ip", "link", "set", iface, "mtu", strconv.Itoa(int(c.MTU)), "up"); err != nil {
		return err
	}
	path6 := "/proc/sys/net/ipv6/conf/" + physical + "/disable_ipv6"
	if b, err := os.ReadFile(path6); err == nil {
		previous := strings.TrimSpace(string(b))
		if previous != "0" && previous != "1" {
			return errors.New("unexpected physical-interface IPv6 state")
		}
		s.IPv6 = &ipv6Recovery{Device: physical, Previous: previous}
		if err := saveSession(s); err != nil {
			return err
		}
		if previous != "1" {
			if err := os.WriteFile(path6, []byte("1\n"), 0o600); err != nil {
				return errors.New("cannot suspend physical-interface IPv6; refusing an IPv4-only tunnel")
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// DNS must stay inside the VPN even when its public network is bypassed.
	pinnedDNS := make(map[netip.Addr]struct{})
	for _, address := range c.DNS {
		if address == endpoint {
			return errors.New("DNS and VPN Endpoint must use different addresses")
		}
		if _, exists := pinnedDNS[address]; exists {
			continue
		}
		if err := addOwnedRoute(ctx, s, []string{address.String() + "/32", "dev", iface}); err != nil {
			return err
		}
		pinnedDNS[address] = struct{}{}
	}
	if err := applyBootstrapRouting(ctx); err != nil {
		return err
	}
	fmt.Println("state=connecting\nWaiting up to 20 seconds for an AWG handshake...")
	probe := netip.MustParseAddr("1.1.1.1")
	if len(c.DNS) > 0 {
		probe = c.DNS[0]
	}
	probePath, err := command(ctx, "ip", "-4", "route", "get", probe.String())
	if err != nil || fieldAfter(probePath, "dev") != iface {
		return errors.New("probe/DNS address is not routed through the AWG tunnel")
	}
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "udp4", net.JoinHostPort(probe.String(), "53"))
	if err != nil {
		return errors.New("cannot trigger an AWG handshake")
	}
	err = conn.SetWriteDeadline(time.Now().Add(time.Second))
	if err == nil {
		_, err = conn.Write([]byte{0x41, 0x57, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1})
	}
	closeErr := conn.Close()
	if err != nil {
		return errors.New("cannot send the handshake trigger")
	}
	if closeErr != nil {
		return closeErr
	}
	connected := false
	for n := 0; n < 20; n++ {
		if !ownsProcess(s) {
			return errors.New("userspace daemon exited during handshake")
		}
		if last, err := handshake(ctx, awg); err == nil && last > 0 {
			connected = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if !connected {
		return errors.New("no handshake in 20 seconds; check server/config/connectivity")
	}
	if err := dns(ctx, kit, "apply"); err != nil {
		return err
	}
	if err := startupRoutingSources(ctx); err != nil {
		return err
	}
	if err := applyConfiguredRouting(ctx); err != nil {
		return err
	}
	document, err := readRoutingDocument()
	if err != nil {
		return err
	}
	if hasRemoteSources(document) {
		if err := startWorker(kit, routingWorker); err != nil {
			return err
		}
	}
	return status(ctx, kit)
}

func status(ctx context.Context, kit string) error {
	if err := printRoutingStatus(); err != nil {
		return err
	}
	if err := statusAutostart(kit); err != nil {
		return err
	}
	s, err := loadSession()
	if err != nil {
		return err
	}
	id, err := bootID()
	if err != nil {
		return err
	}
	if s == nil || s.BootID != id {
		fmt.Println("state=stopped")
		return nil
	}
	if !ownsProcess(s) || routingNeedsRecovery() {
		fmt.Println("state=cleanup-required\nNetwork recovery required; run Stop to restore network state.")
		return nil
	}
	last, err := handshake(ctx, filepath.Join(kit, "bin", "awg"))
	if err != nil {
		return err
	}
	state := "connecting"
	if last > 0 {
		state = "connected"
	}
	fmt.Printf("state=%s\ninterface=%s\npid=%d\n", state, iface, s.PID)
	if last > 0 {
		fmt.Printf("handshake_age_seconds=%d\n", time.Now().Unix()-last)
	}
	if b, err := os.ReadFile("/sys/class/net/" + iface + "/mtu"); err == nil {
		fmt.Printf("mtu=%s\n", strings.TrimSpace(string(b)))
	}
	transfer, err := command(ctx, filepath.Join(kit, "bin", "awg"), "show", iface, "transfer")
	if err != nil {
		return err
	}
	f := strings.Fields(transfer)
	if len(f) == 3 {
		fmt.Printf("received_bytes=%s\nsent_bytes=%s\n", f[1], f[2])
	}
	if s.IPv6 != nil {
		fmt.Printf("physical_ipv6=suspended-on-%s\n", s.IPv6.Device)
	}
	return nil
}
