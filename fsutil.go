package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// lockGame takes an exclusive lock for changing game files, so two h3mod
// processes never deploy at once. The lock lives as long as the process.
func lockGame(cfg *Config) (unlock func(), err error) {
	path := cfg.abs("h3mod.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, errLocked) {
			return nil, fmt.Errorf("another h3mod is deploying (%s is locked)", path)
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}

// writeFileAtomic replaces path so readers see either the old or the new
// content, never a partial file, also across a crash.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + tmpSuffix
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return renameDurable(tmp, path)
}

// renameDurable renames and syncs the directory, so the rename survives a
// crash or power loss.
func renameDurable(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	return syncDir(filepath.Dir(to))
}
