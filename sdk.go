package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ZHMModSDK lives in Retail: its loader (dinput8.dll), ZHMModSDK.dll, and
// mods in Retail/mods, enabled by sections in Retail/mods.ini. h3mod installs
// and updates it from its GitHub releases, recording every file it writes
// with a backup of anything it replaced, so uninstalling only takes out its
// own files. The loader can be turned off without uninstalling, which also
// works for an SDK installed by hand.

const sdkReleases = "https://api.github.com/repos/OrfeasZ/ZHMModSDK/releases/latest"

// sdkInstall is what h3mod installed, kept in state.json.
type sdkInstall struct {
	Version string            `json:"version"`
	Files   map[string]string `json:"files"`             // path in Retail -> sha256
	Backups []string          `json:"backups,omitempty"` // paths in Retail whose originals are in clean/sdk
}

func (cfg *Config) retailDir() string { return filepath.Join(cfg.abs(cfg.Game), "Retail") }

// sdkStatus describes the SDK in the game folder.
type sdkStatus struct {
	Installed bool   // the loader and ZHMModSDK.dll are there, on or off
	Enabled   bool   // the loader is on
	Version   string // when h3mod installed it
	Ours      bool   // installed by h3mod
	Mods      []sdkMod
}

type sdkMod struct {
	Name    string // file name without .dll
	Enabled bool
}

const loaderOff = "dinput8.dll.h3mod-disabled"

func readSDKStatus(cfg *Config) (*sdkStatus, error) {
	retail := cfg.retailDir()
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(retail, name)); return err == nil }
	st := &sdkStatus{Enabled: exists("dinput8.dll")}
	st.Installed = exists("ZHMModSDK.dll") && (st.Enabled || exists(loaderOff))
	state, err := loadState(cfg.abs("state.json"))
	if err != nil {
		return nil, err
	}
	if state.SDK != nil {
		st.Ours, st.Version = true, state.SDK.Version
	}
	enabled := map[string]bool{}
	ini, _ := readModsINI(filepath.Join(retail, "mods.ini"))
	for _, s := range ini.sectionNames() {
		enabled[strings.ToLower(s)] = true
	}
	ents, _ := os.ReadDir(filepath.Join(retail, "mods"))
	for _, e := range ents {
		if name, ok := strings.CutSuffix(e.Name(), ".dll"); ok && !e.IsDir() {
			st.Mods = append(st.Mods, sdkMod{name, enabled[strings.ToLower(name)]})
		}
	}
	return st, nil
}

// installSDK installs or updates ZHMModSDK from its latest release.
func installSDK(ctx context.Context, cfg *Config, logf func(string, ...any)) (string, error) {
	version, url, err := latestSDK(ctx)
	if err != nil {
		return "", err
	}
	work := filepath.Join(cfg.cacheDir(), "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", err
	}
	logf("downloading ZHMModSDK %s", version)
	archive, err := download(ctx, url, work)
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)
	return version, installSDKFrom(ctx, cfg, version, archive)
}

// installSDKFrom installs a ZHMModSDK release archive.
func installSDKFrom(ctx context.Context, cfg *Config, version, archive string) error {
	work := filepath.Join(cfg.cacheDir(), "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	unpacked, err := os.MkdirTemp(work, "sdk-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(unpacked)
	if err := extractArchive(ctx, archive, unpacked); err != nil {
		return err
	}

	statePath := cfg.abs("state.json")
	state, err := loadState(statePath)
	if err != nil {
		return err
	}
	inst := state.SDK
	if inst == nil {
		inst = &sdkInstall{Files: map[string]string{}}
	}
	retail, backups := cfg.retailDir(), cfg.abs(filepath.Join("clean", "sdk"))
	// With the loader turned off, an install keeps it off.
	keepOff := false
	if _, err := os.Stat(filepath.Join(retail, loaderOff)); err == nil {
		keepOff = true
	}
	var files []string
	err = filepath.WalkDir(unpacked, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(unpacked, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		return err
	}
	// The loader goes last, so the game never starts a half-updated SDK.
	slices.SortStableFunc(files, func(a, b string) int {
		return compareBool(a == "dinput8.dll", b == "dinput8.dll")
	})
	state.SDK = inst
	for _, rel := range files {
		dst := filepath.Join(retail, filepath.FromSlash(rel))
		if rel == "dinput8.dll" && keepOff {
			dst = filepath.Join(retail, loaderOff)
		}
		// a file that's there and isn't ours is kept, once
		if _, ours := inst.Files[rel]; !ours && !slices.Contains(inst.Backups, rel) {
			if b, err := os.ReadFile(dst); err == nil {
				bk := filepath.Join(backups, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(bk), 0o755); err != nil {
					return err
				}
				if err := writeFileAtomic(bk, b); err != nil {
					return err
				}
				inst.Backups = append(inst.Backups, rel)
			}
		}
		b, err := os.ReadFile(filepath.Join(unpacked, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(dst, b); err != nil {
			return err
		}
		inst.Files[rel] = fmt.Sprintf("%x", sha256.Sum256(b))
		// recorded after each file, so a crash leaves nothing unaccounted for
		if err := state.save(statePath); err != nil {
			return err
		}
	}
	inst.Version = version
	return state.save(statePath)
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// uninstallSDK removes the files h3mod installed and restores what they
// replaced. Files changed since are left, and reported.
func uninstallSDK(cfg *Config, logf func(string, ...any)) error {
	statePath := cfg.abs("state.json")
	state, err := loadState(statePath)
	if err != nil {
		return err
	}
	if state.SDK == nil {
		return errors.New("ZHMModSDK wasn't installed by h3mod; turn it off with sdk disable, or remove it by hand")
	}
	retail, backups := cfg.retailDir(), cfg.abs(filepath.Join("clean", "sdk"))
	for _, rel := range sortedKeys(state.SDK.Files) {
		dst := filepath.Join(retail, filepath.FromSlash(rel))
		if rel == "dinput8.dll" {
			if _, err := os.Stat(dst); err != nil {
				dst = filepath.Join(retail, loaderOff)
			}
		}
		h, err := fileHash(dst)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return err
		case h != state.SDK.Files[rel]:
			logf("left %s: it changed since h3mod installed it", rel)
			continue
		}
		if err := os.Remove(dst); err != nil {
			return err
		}
		if slices.Contains(state.SDK.Backups, rel) {
			b, err := os.ReadFile(filepath.Join(backups, filepath.FromSlash(rel)))
			if err != nil {
				return err
			}
			if err := writeFileAtomic(filepath.Join(retail, filepath.FromSlash(rel)), b); err != nil {
				return err
			}
		} else {
			removeEmptyDirs(retail, filepath.Dir(dst))
		}
	}
	os.RemoveAll(backups)
	state.SDK = nil
	return state.save(statePath)
}

// setSDKLoader turns the SDK's loader on or off.
func setSDKLoader(cfg *Config, on bool) error {
	retail := cfg.retailDir()
	from, to := filepath.Join(retail, loaderOff), filepath.Join(retail, "dinput8.dll")
	if !on {
		from, to = to, from
	}
	if _, err := os.Stat(to); err == nil {
		return nil
	}
	if _, err := os.Stat(from); err != nil {
		return errors.New("ZHMModSDK isn't installed")
	}
	return os.Rename(from, to)
}

// setSDKMod turns an SDK mod on or off in mods.ini.
func setSDKMod(cfg *Config, name string, on bool) error {
	st, err := readSDKStatus(cfg)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(st.Mods, func(m sdkMod) bool { return strings.EqualFold(m.Name, name) })
	if i < 0 {
		return fmt.Errorf("no ZHMModSDK mod %q in %s", name, filepath.Join(cfg.retailDir(), "mods"))
	}
	path := filepath.Join(cfg.retailDir(), "mods.ini")
	ini, err := readModsINI(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	ini.set(strings.ToLower(st.Mods[i].Name), on)
	return writeFileAtomic(path, ini.bytes())
}

func latestSDK(ctx context.Context) (version, url string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sdkReleases, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("ZHMModSDK releases: %s", resp.Status)
	}
	var r struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return "", "", err
	}
	for _, a := range r.Assets {
		if a.Name == "ZHMModSDK-Release.zip" {
			return r.Tag, a.URL, nil
		}
	}
	return "", "", fmt.Errorf("ZHMModSDK %s has no ZHMModSDK-Release.zip", r.Tag)
}

// modsINI is Retail/mods.ini: [sdk] holds settings, every other section
// name is an enabled mod. Lines are kept as they are.
type modsINI struct{ sections [][]string } // each starts with its [name] line, except a leading block

func readModsINI(path string) (*modsINI, error) {
	ini := &modsINI{}
	b, err := os.ReadFile(path)
	if err != nil {
		return ini, err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[") || len(ini.sections) == 0 {
			ini.sections = append(ini.sections, nil)
		}
		ini.sections[len(ini.sections)-1] = append(ini.sections[len(ini.sections)-1], line)
	}
	return ini, nil
}

func sectionName(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	l := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(l, "[") {
		return ""
	}
	return strings.TrimSpace(strings.Trim(l, "[]"))
}

func (ini *modsINI) sectionNames() []string {
	var out []string
	for _, s := range ini.sections {
		if n := sectionName(s); n != "" && !strings.EqualFold(n, "sdk") {
			out = append(out, n)
		}
	}
	return out
}

func (ini *modsINI) set(name string, on bool) {
	i := slices.IndexFunc(ini.sections, func(s []string) bool { return strings.EqualFold(sectionName(s), name) })
	switch {
	case on && i < 0:
		if n := len(ini.sections); n > 0 {
			last := ini.sections[n-1]
			if len(last) > 0 && strings.TrimSpace(last[len(last)-1]) != "" {
				ini.sections[n-1] = append(last, "")
			}
		}
		ini.sections = append(ini.sections, []string{"[" + name + "]", ""})
	case !on && i >= 0:
		ini.sections = slices.Delete(ini.sections, i, i+1)
	}
}

func (ini *modsINI) bytes() []byte {
	var lines []string
	for _, s := range ini.sections {
		lines = append(lines, s...)
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return []byte(out)
}
