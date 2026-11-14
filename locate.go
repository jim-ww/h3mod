package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// External tools and data files are looked up next to the h3mod binary first
// (tools/ and data/ of the portable bundle), then where the nix package puts
// them (PATH and H3MOD_* variables), then in an SMF install.

const bundleHint = "use the h3mod bundle (nix build .#bundle) or run h3mod through nix"

// exeDir is the folder the running h3mod binary is in.
func exeDir() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Dir(p)
}

func toolPath(name string) (string, error) {
	p := filepath.Join(exeDir(), "tools", name)
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	if _, err := os.Stat(p); err != nil {
		if p, err = exec.LookPath(name); err != nil {
			return "", fmt.Errorf("%s not found in %s or PATH; %s", name, filepath.Join(exeDir(), "tools"), bundleHint)
		}
	}
	// Resolved store paths identify the tool version, which keys the cache.
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

// dataFile finds a data file: env, the bundle's data/ (also gzipped), or
// SMF's Third-Party folder.
func dataFile(cfg *Config, env, name string) (string, error) {
	tried := []string{}
	if p := os.Getenv(env); p != "" {
		return p, nil
	}
	for _, p := range []string{
		filepath.Join(exeDir(), "data", name),
		filepath.Join(exeDir(), "data", name+".gz"),
		filepath.Join(cfg.abs(cfg.Game), "SMF", "Third-Party", name),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		tried = append(tried, p)
	}
	return "", fmt.Errorf("%s not found (%s unset; looked in %s); %s", name, env, strings.Join(tried, ", "), bundleHint)
}

// isGame reports whether dir is a HITMAN 3 installation.
func isGame(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "Runtime", "chunk0.rpkg"))
	return err == nil
}

// findGame looks for HITMAN 3 where it usually is: around the program (a
// release unpacked into the game folder) and in the usual Steam libraries.
func findGame() string {
	dirs := []string{filepath.Dir(exeDir()), exeDir()}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Dir(wd), wd)
	}
	for _, d := range append(dirs, steamGameDirs()...) {
		if isGame(d) {
			return d
		}
	}
	return ""
}

func steamGameDirs() []string {
	var roots []string
	if runtime.GOOS == "windows" {
		roots = []string{`C:\Program Files (x86)\Steam`, `C:\Program Files\Steam`, `D:\SteamLibrary`, `E:\SteamLibrary`}
	} else if home, err := os.UserHomeDir(); err == nil {
		roots = []string{filepath.Join(home, ".steam", "steam"), filepath.Join(home, ".local", "share", "Steam")}
	}
	var out []string
	for _, r := range roots {
		out = append(out, filepath.Join(r, "steamapps", "common", "HITMAN 3"))
	}
	return out
}

// configPath is the config to use without --config: the first of
// ./h3mod.json, h3mod.json next to the program and, on Linux,
// ~/.config/h3mod/config.json that exists. Without any, a new one goes to
// ~/.config/h3mod on Linux and next to the program on Windows (or to
// %APPDATA%\h3mod when the program's folder isn't writable).
func configPath() string {
	candidates := []string{"h3mod.json", filepath.Join(exeDir(), "h3mod.json")}
	xdg := xdgConfigPath()
	if xdg != "" {
		candidates = append(candidates, xdg)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	if xdg != "" {
		return xdg
	}
	if dirWritable(exeDir()) {
		return filepath.Join(exeDir(), "h3mod.json")
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "h3mod", "h3mod.json")
	}
	return "h3mod.json"
}

// xdgConfigPath is ~/.config/h3mod/config.json on Linux, "" elsewhere.
func xdgConfigPath() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "h3mod", "config.json")
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".h3mod-write-test-")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}
