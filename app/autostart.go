package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	bootHook       = "/var/lib/webosbrew/init.d/75-amneziawg"
	ordinaryWGHook = "/var/lib/webosbrew/init.d/90-wireguard"

	bootCancelledPath = base + "/run/boot-cancelled"
	bootAttemptPath   = base + "/run/boot-attempt"
)

type bootSetting uint8

const (
	bootDisabled bootSetting = iota
	bootEnabled
	bootConflict
)

func inspectBootHook(path, target string) (bootSetting, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return bootDisabled, nil
	}
	if err != nil {
		return bootConflict, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return bootConflict, nil
	}
	current, err := os.Readlink(path)
	if err != nil {
		return bootConflict, err
	}
	if current != target {
		return bootConflict, nil
	}
	return bootEnabled, nil
}

func setBootHook(path, target string, preference bootSetting) error {
	current, err := inspectBootHook(path, target)
	if err != nil {
		return err
	}
	if current == bootConflict {
		return errors.New("autostart path belongs to another component; left unchanged")
	}
	switch preference {
	case bootEnabled:
		if current == bootEnabled {
			return nil
		}
		info, err := os.Stat(target)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return errors.New("packaged boot script is unavailable")
		}
		return os.Symlink(target, path)
	case bootDisabled:
		if current == bootDisabled {
			return nil
		}
		return os.Remove(path)
	default:
		return errors.New("invalid autostart preference")
	}
}

func suppressBoot() error {
	id, err := bootID()
	if err != nil {
		return err
	}
	return atomicWrite(bootCancelledPath, []byte(id+"\n"))
}

func configureAutostart(kit string, preference bootSetting) error {
	if preference == bootEnabled {
		if _, _, err := readConfig(configPath); err != nil {
			return err
		}
		if _, err := os.Lstat(ordinaryWGHook); err == nil {
			return errors.New("ordinary WireGuard autostart is enabled; disable it in the WG app first")
		} else if !os.IsNotExist(err) {
			return err
		}
		dir := filepath.Dir(bootHook)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || owner.Uid != 0 {
			return errors.New("boot hook directory must be a real directory owned by root")
		}
	}
	if err := setBootHook(bootHook, filepath.Join(kit, "scripts", "boot.sh"), preference); err != nil {
		return err
	}
	if preference == bootEnabled {
		recordEvent("Boot autoconnect enabled.")
	} else {
		recordEvent("Boot autoconnect disabled.")
	}
	return statusAutostart(kit)
}

func statusAutostart(kit string) error {
	setting, err := inspectBootHook(bootHook, filepath.Join(kit, "scripts", "boot.sh"))
	if err != nil {
		return err
	}
	switch setting {
	case bootEnabled:
		fmt.Println("autostart=on")
	case bootDisabled:
		fmt.Println("autostart=off")
	case bootConflict:
		fmt.Println("autostart=conflict")
	}
	return nil
}

func bootAllowed(kit, id string) (bootSetting, error) {
	setting, err := inspectBootHook(bootHook, filepath.Join(kit, "scripts", "boot.sh"))
	if err != nil || setting != bootEnabled {
		return bootDisabled, err
	}
	cancelled, err := readRegular(bootCancelledPath, 128)
	if err != nil {
		return bootDisabled, err
	}
	if strings.TrimSpace(string(cancelled)) == id {
		return bootDisabled, nil
	}
	if _, err := os.Lstat(ordinaryWGHook); err == nil {
		return bootDisabled, errors.New("ordinary WireGuard autostart is enabled; AWG will not compete with it")
	} else if !os.IsNotExist(err) {
		return bootDisabled, err
	}
	return bootEnabled, nil
}

// boot runs in a detached worker, never inside the synchronous HBC run-parts.
// The operation lock is acquired only when starting the tunnel, so the app
// can cancel a pending boot connection with an ordinary manual Stop.
func runBoot(ctx context.Context, kit string) (result error) {
	if err := prepareRuntime(); err != nil {
		return err
	}
	lock, err := os.OpenFile(base+"/run/boot-worker.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer closeResource(lock)
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("boot worker is already running")
	}
	defer func() {
		if result != nil {
			recordEvent("Boot autoconnect failed: " + result.Error())
		}
	}()
	id, err := bootID()
	if err != nil {
		return err
	}
	setting, err := bootAllowed(kit, id)
	if err != nil || setting != bootEnabled {
		return err
	}
	attempted, err := readRegular(bootAttemptPath, 128)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(attempted)) == id {
		return nil
	}
	if err := atomicWrite(bootAttemptPath, []byte(id+"\n")); err != nil {
		return err
	}
	_, cfg, err := readConfig(configPath)
	if err != nil {
		return err
	}
	recordEvent("Boot autoconnect: waiting for network and clock.")
	waiting, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	for {
		setting, err := bootAllowed(kit, id)
		if err != nil {
			return err
		}
		if setting != bootEnabled {
			recordEvent("Boot autoconnect cancelled.")
			return nil
		}
		if time.Now().UTC().Year() >= 2024 {
			route, routeErr := command(waiting, "ip", "-4", "route", "show", "default")
			dev := fieldAfter(route, "dev")
			if routeErr == nil && dev != "" && dev != iface && dev != "wg0" {
				lookup, done := context.WithTimeout(waiting, 5*time.Second)
				ips, lookupErr := (&net.Resolver{
					PreferGo: true,
				}).LookupNetIP(lookup, "ip4", cfg.Host)
				done()
				if lookupErr == nil && len(ips) > 0 {
					break
				}
			}
		}
		select {
		case <-waiting.Done():
			return errors.New("network or clock was not ready within 120 seconds")
		case <-time.After(3 * time.Second):
		}
	}
	return withLock(func() error {
		setting, err := bootAllowed(kit, id)
		if err != nil {
			return err
		}
		if setting != bootEnabled {
			recordEvent("Boot autoconnect cancelled.")
			return nil
		}
		current, err := loadSession()
		if err != nil {
			return err
		}
		if current != nil && current.BootID == id && ownsProcess(current) {
			recordEvent("Boot autoconnect: tunnel is already running.")
			return nil
		}
		recordEvent("Boot autoconnect: connecting.")
		starting, done := context.WithTimeout(ctx, 90*time.Second)
		defer done()
		if err := start(starting, kit); err != nil {
			return err
		}
		recordEvent("Boot autoconnect: connected.")
		return nil
	})
}
