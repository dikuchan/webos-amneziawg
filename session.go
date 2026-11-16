package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type ipv6Recovery struct{ Device, Previous string }

type session struct {
	BootID       string        `json:"bootId"`
	PID          int           `json:"pid"`
	ProcessStart string        `json:"processStart"`
	Binary       string        `json:"binary"`
	IfIndex      string        `json:"ifIndex"`
	Routes       [][]string    `json:"routes"`
	IPv6         *ipv6Recovery `json:"ipv6,omitempty"`
}

func bootID() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(b)), err
}

func saveSession(s *session) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(statePath, b)
}

func loadSession() (*session, error) {
	f, err := os.Open(statePath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer closeResource(f)
	var s session
	d := json.NewDecoder(io.LimitReader(f, 16384))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return nil, errors.New("session recovery file is invalid; keep it for diagnosis")
	}
	if s.BootID == "" {
		return nil, errors.New("session recovery file has no boot ID")
	}
	return &s, nil
}

func processStart(pid int) string {
	if pid <= 1 {
		return ""
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return ""
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 || f[0] == "Z" {
		return ""
	}
	return f[19]
}

func ownsProcess(s *session) bool {
	if s.PID <= 1 || s.ProcessStart == "" || processStart(s.PID) != s.ProcessStart {
		return false
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", s.PID))
	return err == nil && exe == s.Binary
}
