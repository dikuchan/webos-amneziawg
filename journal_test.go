package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
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
	if err := os.WriteFile(target, []byte("secret configuration"), 0o600); err != nil {
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
