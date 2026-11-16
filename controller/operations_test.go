package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutostartCanBeEnabledAndDisabledWithoutStartingVPN(t *testing.T) {
	// Arrange: a packaged boot hook, including spaces in its source path.
	root := t.TempDir()
	target := filepath.Join(root, "application kit", "boot.sh")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, "75-amneziawg")

	// Act: opt in. This only installs a symlink; it never executes the hook.
	if err := setBootHook(hook, target, bootEnabled); err != nil {
		t.Fatal(err)
	}

	// Assert: repeated enable is safe and disable removes only our hook.
	if got, err := inspectBootHook(hook, target); err != nil || got != bootEnabled {
		t.Fatal(got, err)
	}
	if err := setBootHook(hook, target, bootEnabled); err != nil {
		t.Fatal(err)
	}
	if err := setBootHook(hook, target, bootDisabled); err != nil {
		t.Fatal(err)
	}
	if err := setBootHook(hook, target, bootDisabled); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(hook); !os.IsNotExist(err) {
		t.Fatal("boot hook was not removed", err)
	}
}

func TestAutostartLeavesForeignHooksUntouched(t *testing.T) {
	for _, kind := range []string{"file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			// Arrange: the destination belongs to another component.
			root := t.TempDir()
			hook := filepath.Join(root, "75-amneziawg")
			foreign := filepath.Join(root, "foreign-hook")
			if kind == "file" {
				if err := os.WriteFile(hook, []byte("foreign contents"), 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(foreign, hook); err != nil {
				t.Fatal(err)
			}

			// Act / Assert: neither opt-in nor opt-out may replace foreign state.
			for _, preference := range []bootSetting{bootEnabled, bootDisabled} {
				if err := setBootHook(hook, filepath.Join(root, "our-hook"), preference); err == nil {
					t.Fatal("foreign hook accepted")
				}
				if kind == "file" {
					data, err := os.ReadFile(hook)
					if err != nil || string(data) != "foreign contents" {
						t.Fatal("foreign file changed", err)
					}
				} else if target, err := os.Readlink(hook); err != nil || target != foreign {
					t.Fatal("foreign link changed", err)
				}
			}
		})
	}
}

func TestJournalRetainsRecentEventsWithinItsStorageLimit(t *testing.T) {
	// Arrange: enough events to exceed the journal cap.
	path := filepath.Join(t.TempDir(), "events.log")
	now := time.Date(2026, 11, 16, 18, 0, 0, 0, time.UTC)
	first := "first-event " + strings.Repeat("a", 900)
	if err := appendJournal(path, now, first); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 90; i++ {
		if err := appendJournal(path, now, "middle-event "+strings.Repeat("b", 900)); err != nil {
			t.Fatal(err)
		}
	}

	// Act: the most recent event must survive rotation.
	if err := appendJournal(path, now, "last-event"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Assert: bounded, complete lines, private permissions and readable tail.
	if len(data) > journalLimitBytes || strings.Contains(string(data), "first-event") || !strings.HasSuffix(string(data), "last-event\n") {
		t.Fatal("journal rotation lost its contract")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("journal is not private", err)
	}
	tail, err := readJournal(path)
	if err != nil || len(tail) > journalViewBytes || !strings.HasSuffix(tail, "last-event\n") {
		t.Fatal("journal tail is invalid", err)
	}
}

func TestJournalRejectsSymlinksWithoutChangingTheirTarget(t *testing.T) {
	// Arrange: a substituted log path.
	root := t.TempDir()
	target := filepath.Join(root, "private.conf")
	if err := os.WriteFile(target, []byte("secret configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "events.log")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	// Act / Assert: log write and viewer both refuse it.
	if err := appendJournal(path, time.Now(), "event"); err == nil {
		t.Fatal("log symlink accepted")
	}
	if _, err := readJournal(path); err == nil {
		t.Fatal("viewer followed a log symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "secret configuration" {
		t.Fatal("target changed", err)
	}
}
