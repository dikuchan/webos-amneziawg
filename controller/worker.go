package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type workerKind string

const (
	uploadWorker  workerKind = "upload-serve"
	routingWorker workerKind = "route-updater"
)

type workerIdentity struct {
	BootID  string     `json:"bootId"`
	PID     int        `json:"pid"`
	Started string     `json:"started"`
	Binary  string     `json:"binary"`
	Kind    workerKind `json:"kind"`
}

func workerPath(kind workerKind) string { return base + "/run/" + string(kind) + ".json" }
func readWorker(kind workerKind) (*workerIdentity, error) {
	raw, err := readRegular(workerPath(kind), 4096)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var current workerIdentity
	if err := json.Unmarshal(raw, &current); err != nil || current.Kind != kind {
		return nil, errors.New("invalid auxiliary worker state")
	}
	return &current, nil
}
func ownsWorker(current *workerIdentity) bool {
	if current == nil || current.PID <= 1 || current.Started == "" || processStart(current.PID) != current.Started {
		return false
	}
	boot, err := bootID()
	if err != nil || boot != current.BootID {
		return false
	}
	executable, err := os.Readlink("/proc/" + strconv.Itoa(current.PID) + "/exe")
	if err != nil || executable != current.Binary {
		return false
	}
	args, err := os.ReadFile("/proc/" + strconv.Itoa(current.PID) + "/cmdline")
	fields := strings.Split(string(args), "\x00")
	return err == nil && len(fields) >= 2 && fields[1] == string(current.Kind)
}
func startWorker(kit string, kind workerKind) error {
	old, err := readWorker(kind)
	if err != nil {
		return err
	}
	if ownsWorker(old) {
		return nil
	}
	id, err := bootID()
	if err != nil {
		return err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	binary := filepath.Join(kit, "bin", "awg-test")
	child := exec.Command(binary, string(kind))
	child.Stdin, child.Stdout, child.Stderr = null, null, null
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return errors.New("cannot start auxiliary worker")
	}
	state := workerIdentity{BootID: id, PID: child.Process.Pid, Started: processStart(child.Process.Pid), Binary: binary, Kind: kind}
	raw, err := json.Marshal(state)
	if err == nil {
		err = atomicWrite(workerPath(kind), raw)
	}
	if err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		return err
	}
	return child.Process.Release()
}
func waitForWorkerIdentity(kind workerKind) error {
	for attempt := 0; attempt < 50; attempt++ {
		current, err := readWorker(kind)
		if err != nil {
			return err
		}
		if current != nil && current.PID == os.Getpid() && ownsWorker(current) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("auxiliary worker identity was not recorded")
}
func stopWorker(kind workerKind) error {
	current, err := readWorker(kind)
	if err != nil {
		return err
	}
	if ownsWorker(current) {
		if err := syscall.Kill(current.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		for attempt := 0; attempt < 60 && ownsWorker(current); attempt++ {
			time.Sleep(50 * time.Millisecond)
		}
		if ownsWorker(current) {
			return errors.New("auxiliary worker did not stop; retry")
		}
	}
	if err := os.Remove(workerPath(kind)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
