package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

var errLocked = windows.ERROR_LOCK_VIOLATION

func lockFile(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLocked
	}
	return err
}

// syncDir is a no-op: NTFS journals renames, and directories can't be
// flushed like files.
func syncDir(string) error { return nil }

// hideWindow keeps console tools from opening a window each time the GUI,
// which has no console, runs them.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// attachConsole connects a GUI-subsystem program, which starts without a
// console, to the terminal it was started from, so h3mod-gui can run CLI
// commands there. It reports whether there is output to write to.
func attachConsole() bool {
	// Handles the caller redirected (pipes, files, shell completion) work
	// already; only missing ones go to the parent's console.
	missing := func(f *os.File) bool {
		h := windows.Handle(f.Fd())
		return h == 0 || h == windows.InvalidHandle
	}
	if !missing(os.Stdout) && !missing(os.Stderr) && !missing(os.Stdin) {
		return true
	}
	const attachParentProcess = ^uint32(0)
	attach := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := attach.Call(uintptr(attachParentProcess)); r == 0 {
		return !missing(os.Stderr) // started without a terminal
	}
	reopened := false
	for _, f := range []struct {
		name string
		std  **os.File
	}{{"CONOUT$", &os.Stdout}, {"CONOUT$", &os.Stderr}, {"CONIN$", &os.Stdin}} {
		if !missing(*f.std) {
			continue
		}
		h, err := windows.CreateFile(windows.StringToUTF16Ptr(f.name), windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			*f.std = os.NewFile(uintptr(h), f.name)
			reopened = reopened || f.std == &os.Stdout
		}
	}
	if reopened {
		// The shell doesn't wait for GUI programs and has printed its prompt.
		os.Stdout.WriteString("\n")
	}
	return !missing(os.Stderr)
}
