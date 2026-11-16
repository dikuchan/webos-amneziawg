package main

import (
	"os"
	"path/filepath"
	"testing"
)

func migrationFixture(t *testing.T) migrationLayout {
	t.Helper()
	root := t.TempDir()
	m := migrationLayout{Legacy: filepath.Join(root, "legacy"), Current: filepath.Join(root, "current"), OldHook: filepath.Join(root, "75-old"), NewHook: filepath.Join(root, "75-new"), OldScript: filepath.Join(root, "old-boot.sh"), NewScript: filepath.Join(root, "new-boot.sh"), BootID: "test-boot"}
	for _, dir := range []string{m.Legacy + "/conf", m.Legacy + "/run", m.Current + "/conf", m.Current + "/run"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, script := range []string{m.OldScript, m.NewScript} {
		if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(m.Legacy+"/conf/awgtest0.conf", []byte(exampleConfig()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Legacy+"/conf/routing.json", []byte(`{"mode":"ru-direct"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(m.OldScript, m.OldHook); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestMigrationCopiesProfileAndTransfersOnlyItsBootHook(t *testing.T) {
	m := migrationFixture(t)

	if err := migrateLegacyFiles(m); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(m.Current + "/conf/awg0.conf")
	if err != nil || string(raw) != exampleConfig() {
		t.Fatal("profile was not preserved")
	}
	info, err := os.Stat(m.Current + "/conf/awg0.conf")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("profile permissions")
	}
	if target, err := os.Readlink(m.NewHook); err != nil || target != m.NewScript {
		t.Fatal("autostart was not transferred")
	}
	if _, err := os.Lstat(m.OldHook); !os.IsNotExist(err) {
		t.Fatal("old hook remains")
	}
	original, err := os.ReadFile(m.Legacy + "/conf/awgtest0.conf")
	if err != nil || string(original) != exampleConfig() {
		t.Fatal("legacy profile changed")
	}
	policy, err := os.ReadFile(m.Current + "/conf/routing.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := decodeRoutingDocument(policy)
	if err != nil || doc.Mode != modeExclude {
		t.Fatal("old routing preference lost", err)
	}
	if err := migrateLegacyFiles(m); err != nil {
		t.Fatal("migration is not idempotent", err)
	}
}
func TestMigrationRequiresLegacyNetworkRecoveryBeforeCopying(t *testing.T) {
	m := migrationFixture(t)
	if err := os.WriteFile(m.Legacy+"/run/session.json", []byte(`{"bootId":"test-boot"}`), 0600); err != nil {
		t.Fatal(err)
	}

	err := migrateLegacyFiles(m)

	if err == nil {
		t.Fatal("active legacy installation migrated")
	}
	if _, err := os.Stat(m.Current + "/conf/awg0.conf"); !os.IsNotExist(err) {
		t.Fatal("profile copied before legacy recovery")
	}
	if _, err := os.Readlink(m.OldHook); err != nil {
		t.Fatal("old hook changed")
	}
}
