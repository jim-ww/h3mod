package main

import (
	"errors"
	"os"

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
