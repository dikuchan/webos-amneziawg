package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

const (
	journalPath       = base + "/run/events.log"
	journalLimitBytes = 65536
	journalViewBytes  = 12288
)

func appendJournal(path string, at time.Time, message string) error {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer closeResource(lock)
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}
	old, err := readRegular(path, journalLimitBytes)
	if err != nil {
		return err
	}
	if len(message) > 2048 {
		message = message[:2048]
	}
	message = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(message, "?"))
	line := at.UTC().Format("2006-01-02 15:04:05Z") + "  " + message + "\n"
	data := append(old, line...)
	if len(data) > journalLimitBytes {
		data = data[len(data)-journalLimitBytes:]
		if first := strings.IndexByte(string(data), '\n'); first >= 0 {
			data = data[first+1:]
		}
	}
	return atomicWrite(path, data)
}

func readJournal(path string) (string, error) {
	data, err := readRegular(path, journalLimitBytes)
	if err != nil {
		return "", err
	}
	if len(data) > journalViewBytes {
		data = data[len(data)-journalViewBytes:]
		if first := strings.IndexByte(string(data), '\n'); first >= 0 {
			data = data[first+1:]
		}
	}
	return string(data), nil
}

func recordEvent(message string) {
	if err := appendJournal(journalPath, time.Now(), message); err != nil {
		fmt.Fprintln(os.Stderr, "WARNING: cannot persist the operation journal")
	}
}
