package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The app and SSH-only kit share this private runtime.
const (
	base          = "/var/lib/webosbrew/amneziawg"
	iface         = "awg0"
	configPath    = base + "/conf/awg0.conf"
	statePath     = base + "/run/session.json"
	routeProtocol = "242"
	routeMetric   = "42431"
)

type outputBuffer struct{ bytes.Buffer }

func (b *outputBuffer) Write(p []byte) (int, error) {
	if len(p) > 16384-b.Len() {
		return 0, errors.New("command output exceeded 16 KiB")
	}
	return b.Buffer.Write(p)
}

// Never includes command arguments or tool stderr in errors: awg's parser may
// echo a malformed config line containing a key. Tools own that boundary.
func command(ctx context.Context, name string, args ...string) (string, error) {
	limit := 5 * time.Second
	if name == "/bin/sh" {
		limit = 30 * time.Second
	}
	step, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(step, name, args...)
	var stdout, stderr outputBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s failed (%w); command contents omitted", filepath.Base(name), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func kitDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	return filepath.Dir(filepath.Dir(exe)), err
}

func secureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || stat.Uid != 0 {
		return errors.New("runtime directory must be a real directory owned by root")
	}
	return os.Chmod(path, 0o700)
}

func prepareRuntime() error {
	if os.Geteuid() != 0 {
		return errors.New("run as root")
	}
	syscall.Umask(0o077)
	if err := os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin"); err != nil {
		return err
	}
	if err := os.Unsetenv("LD_PRELOAD"); err != nil {
		return err
	}
	for _, path := range []string{
		base,
		base + "/conf",
		base + "/run",
	} {
		if err := secureDir(path); err != nil {
			return err
		}
	}
	return migrateInstallation()
}

var errOperationBusy = errors.New("another AmneziaWG operation is running")

var errImportActive = errors.New("stop AmneziaWG before importing a config")

func withLock(fn func() error) error {
	if err := prepareRuntime(); err != nil {
		return err
	}
	f, err := os.OpenFile(base+"/run/operation.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer closeResource(f)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errOperationBusy
	}
	// Closing the file releases its flock.
	return fn()
}

func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(f.Name()); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "WARNING: cannot remove temporary runtime file")
		}
	}()
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func fieldAfter(text, key string) string {
	fields := strings.Fields(text)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == key {
			return fields[i+1]
		}
	}
	return ""
}

// Only small regular files in the private runtime are accepted. O_NONBLOCK
// prevents a substituted FIFO from hanging before its type can be checked.
func readRegular(path string, limit int) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot open private runtime file")
	}
	defer closeResource(f)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, errors.New("private runtime file has an invalid type or size")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("cannot read bounded runtime file")
	}
	return data, nil
}

// Cleanup reports failures without exposing paths or treating an already-closed
// handle as an operation failure. Writes still check Close before committing.
func closeResource(resource io.Closer) {
	if err := resource.Close(); err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, net.ErrClosed) {
		fmt.Fprintln(os.Stderr, "WARNING: resource cleanup failed")
	}
}

// Used only before ownership of a newly started child has been released.
func terminateChild(child *exec.Cmd) error {
	killErr := child.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	waitErr := child.Wait()
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) {
		waitErr = nil
	} // A killed child normally exits unsuccessfully.
	return errors.Join(killErr, waitErr)
}
