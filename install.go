package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Installing works like SMF's Mod Manager: an archive holds framework mod
// folders (each with a manifest.json) or loose .rpkg files, which become an
// rpkg mod with chunk folders. Mods are unpacked into a hidden folder inside
// the mods directory and renamed into place, so a failed install leaves the
// mods directory as it was.

var rpkgChunk = regexp.MustCompile(`(?i)chunk\d+`)

// stagedMod is a mod unpacked into the staging folder.
type stagedMod struct {
	dir  string // folder name it installs as
	path string
	id   string
}

// stageMods unpacks src (an archive or a .rpkg) into stage. rpkgName names
// the mod made from loose .rpkg files.
func stageMods(ctx context.Context, src, stage, rpkgName string) ([]stagedMod, error) {
	unpacked := filepath.Join(stage, "unpacked")
	if err := os.Mkdir(unpacked, 0o755); err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(src), ".rpkg") {
		if err := copyFile(src, filepath.Join(unpacked, filepath.Base(src))); err != nil {
			return nil, err
		}
	} else if err := extractArchive(ctx, src, unpacked); err != nil {
		return nil, err
	}
	if err := checkUnpacked(unpacked); err != nil {
		return nil, err
	}

	ents, err := os.ReadDir(unpacked)
	if err != nil {
		return nil, err
	}
	framework := len(ents) > 0
	for _, e := range ents {
		if _, err := os.Stat(filepath.Join(unpacked, e.Name(), "manifest.json")); !e.IsDir() || err != nil {
			framework = false
		}
	}
	if framework {
		var out []stagedMod
		for _, e := range ents {
			path := filepath.Join(unpacked, e.Name())
			id, err := validateModFolder(path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", e.Name(), err)
			}
			out = append(out, stagedMod{dir: e.Name(), path: path, id: id})
		}
		return out, nil
	}

	var rpkgs []string
	filepath.WalkDir(unpacked, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".rpkg") {
			rpkgs = append(rpkgs, p)
		}
		return nil
	})
	if len(rpkgs) == 0 {
		return nil, errors.New("not a mod: no folders with a manifest.json and no .rpkg files")
	}
	if rpkgName == "" || rpkgName != filepath.Base(rpkgName) || strings.HasPrefix(rpkgName, ".") {
		return nil, fmt.Errorf("invalid mod name %q for an rpkg mod", rpkgName)
	}
	path := filepath.Join(stage, "rpkg", rpkgName)
	for _, r := range rpkgs {
		rel, _ := filepath.Rel(unpacked, r)
		chunk := strings.ToLower(rpkgChunk.FindString(filepath.ToSlash(rel)))
		if chunk == "" {
			chunk = "chunk0"
		}
		if err := os.MkdirAll(filepath.Join(path, chunk), 0o755); err != nil {
			return nil, err
		}
		if err := os.Rename(r, filepath.Join(path, chunk, filepath.Base(r))); err != nil {
			return nil, err
		}
	}
	return []stagedMod{{dir: rpkgName, path: path, id: rpkgName}}, nil
}

func extractArchive(ctx context.Context, src, dst string) error {
	sz, err := toolPath("7zz")
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, sz, "x", "-y", "-bd", "-o"+dst, abs)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extracting %s: %w: %s", src, err, strings.TrimSpace(out.String()))
	}
	return nil
}

// checkUnpacked rejects anything but plain files and folders, so an archive
// can't link outside the mods directory.
func checkUnpacked(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("archive contains a link or special file: %s", strings.TrimPrefix(p, dir+string(filepath.Separator)))
		}
		return nil
	})
}

// validateModFolder checks a framework mod the way the Mod Manager does and
// returns its ID.
func validateModFolder(path string) (string, error) {
	b, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		return "", err
	}
	man, err := parseManifest(b)
	if err != nil {
		return "", fmt.Errorf("manifest.json: %w", err)
	}
	if man.ID == "" {
		return "", errors.New("manifest.json has no id")
	}
	folders := man.ContentFolders
	for _, o := range man.Options {
		folders = append(folders, o.ContentFolders...)
	}
	for _, f := range folders {
		ents, err := os.ReadDir(filepath.Join(path, filepath.FromSlash(f)))
		if err != nil {
			return "", fmt.Errorf("content folder %q: %w", f, err)
		}
		for _, e := range ents {
			if !chunkInName.MatchString(e.Name()) {
				return "", fmt.Errorf("content folder %q: %q is not a chunk folder", f, e.Name())
			}
		}
	}
	return man.ID, nil
}

// placeMod moves a staged mod to modsDir/dir. An existing folder is replaced
// only when replace is set; the old one is moved into stage first, so it is
// put back if the move fails and deleted with stage otherwise.
func placeMod(m stagedMod, modsDir, dir, stage string, replace bool) error {
	dst := filepath.Join(modsDir, dir)
	old := ""
	if _, err := os.Lstat(dst); err == nil {
		if !replace {
			return fmt.Errorf("%s is already installed; use update, or install -replace", dir)
		}
		old = filepath.Join(stage, "old-"+dir)
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := renameDurable(m.path, dst); err != nil {
		if old != "" {
			os.Rename(old, dst)
		}
		return err
	}
	return nil
}

// installMods installs the mods in src and returns their folder names.
// asDir, if set, installs the single mod with ID asID into that folder
// (updates keep the folder the config refers to).
func installMods(ctx context.Context, cfg *Config, src, rpkgName string, replace bool, asDir, asID string) ([]string, error) {
	modsDir := cfg.abs(cfg.Mods)
	stage, err := os.MkdirTemp(modsDir, ".h3mod-install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	staged, err := stageMods(ctx, src, stage, rpkgName)
	if err != nil {
		return nil, err
	}
	if asDir != "" {
		var match []stagedMod
		for _, m := range staged {
			if strings.EqualFold(m.id, asID) {
				m.dir = asDir
				match = append(match, m)
			}
		}
		if len(match) != 1 {
			return nil, fmt.Errorf("%s does not contain mod %s", src, asID)
		}
		staged = match
	}
	var dirs []string
	for _, m := range staged {
		if err := placeMod(m, modsDir, m.dir, stage, replace); err != nil {
			return dirs, err
		}
		dirs = append(dirs, m.dir)
	}
	return dirs, nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
