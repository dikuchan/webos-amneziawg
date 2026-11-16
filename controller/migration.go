package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const legacyBase = "/var/lib/webosbrew/amneziawg-test"
const legacyAppID = "com.github.dikuchan.amneziawgtest"

var errLegacyActive = errors.New("stop the previous AmneziaWG app before migration")

type migrationLayout struct{ Legacy, Current, OldHook, NewHook, OldScript, NewScript, BootID string }

func migrateLegacyFiles(m migrationLayout) error {
	marker := m.Current + "/conf/migration"
	done, err := readRegular(marker, 64)
	if err != nil {
		return err
	}
	if len(done) > 0 {
		if string(done) != "fresh\n" && string(done) != "legacy\n" {
			return errors.New("invalid migration marker")
		}
		return nil
	}
	if _, err := os.Stat(m.Legacy); os.IsNotExist(err) {
		return atomicWrite(marker, []byte("fresh\n"))
	} else if err != nil {
		return err
	}
	raw, err := readRegular(m.Legacy+"/run/session.json", 16384)
	if err != nil {
		return err
	}
	if len(raw) > 0 {
		var s struct {
			BootID string `json:"bootId"`
		}
		if json.Unmarshal(raw, &s) != nil {
			return errors.New("invalid legacy recovery state; use its Stop command")
		}
		if s.BootID == m.BootID {
			return errLegacyActive
		}
	}
	dnsBoot, err := readRegular(m.Legacy+"/run/dns/boot-id", 128)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(dnsBoot)) == m.BootID {
		return errLegacyActive
	}
	for _, name := range []string{"upload-serve", "route-updater"} {
		raw, err := readRegular(m.Legacy+"/run/"+name+".json", 4096)
		if err != nil {
			return err
		}
		if len(raw) > 0 {
			var worker workerIdentity
			if json.Unmarshal(raw, &worker) != nil {
				return errors.New("invalid legacy worker state")
			}
			if ownsWorker(&worker) {
				return errLegacyActive
			}
		}
	}
	hook, err := inspectBootHook(m.OldHook, m.OldScript)
	if err != nil {
		return err
	}
	if hook == bootConflict {
		return errors.New("legacy autostart hook belongs to another component")
	}
	target := m.Current + "/conf/awg0.conf"
	existing, err := readRegular(target, maxConfigBytes)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		raw, err := readRegular(m.Legacy+"/conf/awgtest0.conf", maxConfigBytes)
		if err != nil {
			return err
		}
		if len(raw) > 0 {
			if _, err := parseConfig(raw); err != nil {
				return errors.New("legacy profile is invalid")
			}
			if err := atomicWrite(target, raw); err != nil {
				return err
			}
		}
	}
	policy, err := readRegular(m.Current+"/conf/routing.json", 2*sourceTotalByteLimit)
	if err != nil {
		return err
	}
	if len(policy) == 0 {
		document, err := defaultRoutingDocument()
		if err != nil {
			return err
		}
		old, err := readRegular(m.Legacy+"/conf/routing.json", 4096)
		if err != nil {
			return err
		}
		if len(old) > 0 {
			var value struct {
				Mode string `json:"mode"`
			}
			if json.Unmarshal(old, &value) != nil || value.Mode != "all" && value.Mode != "ru-direct" {
				return errors.New("legacy routing settings are invalid")
			}
			if value.Mode == "ru-direct" {
				document.Mode = modeExclude
			}
		}
		cache, err := readRegular(m.Legacy+"/conf/ru-direct.json", 256*1024)
		if err != nil {
			return err
		}
		if len(cache) > 0 {
			// A damaged optional list falls back to the bundled snapshot.
			if d, err := decodeRoutingCache(cache); err == nil {
				document.Sources[0].Cache = sourceCache{Raw: d.Source, Hash: d.Hash, Checked: d.Checked, State: sourceReady}
			}
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			return err
		}
		if err := atomicWrite(m.Current+"/conf/routing.json", encoded); err != nil {
			return err
		}
	}
	oldRemoved := false
	newCreated := false
	rollback := func(cause error) error {
		if oldRemoved {
			cause = errors.Join(cause, os.Symlink(m.OldScript, m.OldHook))
		}
		if newCreated {
			cause = errors.Join(cause, os.Remove(m.NewHook))
		}
		return cause
	}
	if hook == bootEnabled {
		if _, err := os.Stat(target); err != nil {
			return errors.New("cannot transfer autostart without a profile")
		}
		previous, err := inspectBootHook(m.NewHook, m.NewScript)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(m.NewHook), 0755); err != nil {
			return err
		}
		if err := setBootHook(m.NewHook, m.NewScript, bootEnabled); err != nil {
			return err
		}
		newCreated = previous == bootDisabled
		if err := os.Remove(m.OldHook); err != nil {
			return rollback(err)
		}
		oldRemoved = true
	}
	if err := atomicWrite(marker, []byte("legacy\n")); err != nil {
		return rollback(err)
	}
	return nil
}
func migrateInstallation() error {
	marker, err := readRegular(base+"/conf/migration", 64)
	if err != nil {
		return err
	}
	if len(marker) > 0 {
		if string(marker) != "fresh\n" && string(marker) != "legacy\n" {
			return errors.New("invalid migration marker")
		}
		return nil
	}
	lock, err := os.OpenFile(base+"/run/migration.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errOperationBusy
	}
	kit, err := kitDir()
	if err != nil {
		return err
	}
	id, err := bootID()
	if err != nil {
		return err
	}
	if info, err := os.Lstat(legacyBase); err == nil {
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || owner.Uid != 0 {
			return errors.New("legacy state directory must be owned by root")
		}
		if err := secureDir(legacyBase + "/run"); err != nil {
			return err
		}
		oldLock, err := os.OpenFile(legacyBase+"/run/operation.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return err
		}
		defer oldLock.Close()
		if err := syscall.Flock(int(oldLock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return errLegacyActive
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := command(ctx, "ip", "link", "show", "awgtest0"); err == nil {
			return errLegacyActive
		}
	}
	oldScript := "/media/developer/apps/usr/palm/applications/" + legacyAppID + "/payload/scripts/boot.sh"
	if target, err := os.Readlink("/var/lib/webosbrew/init.d/75-amneziawg-test"); err == nil && target == legacyBase+"/scripts/boot.sh" {
		oldScript = target
	}
	if _, err := os.Lstat(ordinaryWGHook); err == nil {
		if _, err := os.Lstat("/var/lib/webosbrew/init.d/75-amneziawg-test"); err == nil {
			return errors.New("ordinary WireGuard autostart is enabled; disable it before migration")
		}
	}
	return migrateLegacyFiles(migrationLayout{Legacy: legacyBase, Current: base, OldHook: "/var/lib/webosbrew/init.d/75-amneziawg-test", NewHook: bootHook, OldScript: oldScript, NewScript: filepath.Join(kit, "scripts", "boot.sh"), BootID: id})
}
