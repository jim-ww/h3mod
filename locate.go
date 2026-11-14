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
