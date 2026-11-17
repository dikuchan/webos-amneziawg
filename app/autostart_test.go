package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutostartCanBeEnabledAndDisabledWithoutStartingVPN(t *testing.T) {
	// Arrange: a packaged boot hook, including spaces in its source path.
	root := t.TempDir()
	target := filepath.Join(root, "application kit", "boot.sh")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
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
	for _, kind := range []string{
		"file",
		"symlink",
	} {
		t.Run(kind, func(t *testing.T) {
			// Arrange: the destination belongs to another component.
			root := t.TempDir()
			hook := filepath.Join(root, "75-amneziawg")
			foreign := filepath.Join(root, "foreign-hook")
			if kind == "file" {
				if err := os.WriteFile(hook, []byte("foreign contents"), 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(foreign, hook); err != nil {
				t.Fatal(err)
			}

			// Act / Assert: neither opt-in nor opt-out may replace foreign state.
			for _, preference := range []bootSetting{
				bootEnabled,
				bootDisabled,
			} {
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
