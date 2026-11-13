package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ModJPFiles mods also copy loose files into Retail/mods, which other tools
// (ZHMModSDK) use too. h3mod records every file it places with its hash, so
// redeploys and undeploy remove only its own files, and it never overwrites
// a file it didn't write.

// jpContentDir is the folder the ModJPFiles script copies for a selected
// "... (ModJPFilesContent)" option.
func jpContentDir(option string) string {
	s := strings.TrimSpace(strings.Replace(option, "(ModJPFilesContent)", "", 1))
	s = strings.NewReplacer(" ", "_", "\t", "_", "\n", "_", ":", "_", "(", "", ")", "").Replace(s)
	return "content_" + strings.ToLower(s)
}

// addRetailDir adds the files under dir, by path relative to it.
func (p *Plan) addRetailDir(dir string) error {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if p.Retail == nil {
			p.Retail = map[string]string{}
		}
		p.Retail[filepath.ToSlash(rel)] = path
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func fileHash(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// syncRetail makes root hold the wanted files (rel path -> source), removes
// files placed before (rel path -> hash) that are no longer wanted, and
// returns what is placed now. Files changed by someone else are left alone.
func syncRetail(root string, wanted, placed map[string]string, log func(string, ...any)) (map[string]string, error) {
	now := map[string]string{}
	ours := func(rel string) (bool, error) {
		h, err := fileHash(filepath.Join(root, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			return true, nil
		}
		return err == nil && placed[rel] != "" && h == placed[rel], err
	}
	for _, rel := range sortedKeys(wanted) {
		src := wanted[rel]
		want, err := fileHash(src)
		if err != nil {
			return now, err
		}
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if h, err := fileHash(dst); err == nil && h == want {
			now[rel] = want // already there, maybe from an older deploy
			continue
		}
		own, err := ours(rel)
		if err != nil {
			return now, err
		}
		if !own {
			log("note: not replacing %s, h3mod didn't write it", dst)
			continue
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return now, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return now, err
		}
		if err := writeFileAtomic(dst, b); err != nil {
			return now, err
		}
		now[rel] = want
	}
	for _, rel := range sortedKeys(placed) {
		if _, ok := now[rel]; ok {
			continue
		}
		own, err := ours(rel)
		if err != nil {
			return now, err
		}
		if !own {
			log("note: leaving %s, it changed since h3mod wrote it", filepath.Join(root, rel))
			continue
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return now, fmt.Errorf("removing %s: %w", rel, err)
		}
		removeEmptyDirs(root, filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))))
	}
	return now, nil
}

// removeEmptyDirs removes dir and its parents up to root while they are empty.
func removeEmptyDirs(root, dir string) {
	for dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)) {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
