//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

var errLocked = syscall.EWOULDBLOCK

func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLocked
	}
	return err
}

// syncDir makes a rename in dir durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func hideWindow(*exec.Cmd) {}

// attachConsole reports that there is output to write to: Unix programs keep
// their terminal (or wherever output was sent).
func attachConsole() bool { return true }
