package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const tmpSuffix = ".h3mod-tmp"

var (
	patchLevel     = regexp.MustCompile(`patchlevel=[0-9]+`)
	moddedPD       = []byte("patchlevel=310")
	outputArchive  = regexp.MustCompile(fmt.Sprintf(`^chunk\d+patch%d\.rpkg$`, outputPatch))
	errUnsupported = errors.New("enabled mods use unsupported features (use -skip-unsupported to deploy without them)")
)

// buildPlan collects the enabled mods in load order. Missing mods are an
// error when strict, and skipped otherwise.
func buildPlan(cfg *Config, strict bool) (*Plan, error) {
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return nil, err
	}
	byDir := map[string]*Mod{}
	for _, m := range mods {
		byDir[m.Dir] = m
	}
	p := newPlan()
	for _, dir := range cfg.Enabled {
		m := byDir[dir]
		if m == nil {
			if !strict {
				continue
			}
			return nil, fmt.Errorf("enabled mod %q not found in %s", dir, cfg.abs(cfg.Mods))
		}
		if err := p.addMod(m, cfg); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func printUnsupported(p *Plan) {
	mods := make([]string, 0, len(p.Unsupported))
	for m := range p.Unsupported {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	for _, m := range mods {
		var parts []string
		for what, n := range p.Unsupported[m] {
			parts = append(parts, fmt.Sprintf("%s ×%d", what, n))
		}
		sort.Strings(parts)
		fmt.Printf("  %s: %s\n", m, strings.Join(parts, ", "))
	}
}

// deploy writes into the game, or into out instead when it is set.
func deploy(cfg *Config, skipUnsupported bool, out string) error {
	start := time.Now()
	if len(cfg.Enabled) == 0 && out == "" {
		return undeploy(cfg)
	}
	p, err := buildPlan(cfg, true)
	if err != nil {
		return err
	}
	if len(p.Unsupported) > 0 {
		fmt.Println("unsupported, not deployed:")
		printUnsupported(p)
		if !skipUnsupported {
			return errUnsupported
		}
	}

	runtime := cfg.runtimeDir()
	cleanPD, err := ensureCleanPD(cfg)
	if err != nil {
		return err
	}
	dst := runtime
	if out != "" {
		dst = out
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
	}
	if err := removeTemps(dst); err != nil {
		return err
	}

	if len(p.Repo) > 0 {
		base, err := baseResource(runtime, 0, repoHash)
		if err != nil {
			return err
		}
		repo, err := patchRepository(base, p.Repo)
		if err != nil {
			return err
		}
		p.add(0, repo)
	}

	// Stage every archive fully before touching any game file.
	chunks := make([]int, 0, len(p.Chunks))
	for c := range p.Chunks {
		chunks = append(chunks, c)
	}
	sort.Ints(chunks)
	keep := map[string]bool{}
	for _, c := range chunks {
		name := fmt.Sprintf("chunk%dpatch%d.rpkg", c, outputPatch)
		res := make([]*Resource, 0, len(p.Chunks[c]))
		for _, r := range p.Chunks[c] {
			res = append(res, r)
		}
		if err := WritePatchArchive(filepath.Join(dst, name+tmpSuffix), res); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		keep[name] = true
		fmt.Printf("%s: %d files\n", name, len(res))
	}

	// Each rename is atomic; packagedefinition goes last so the game only
	// starts loading patch archives once all of them are in place.
	for name := range keep {
		if err := os.Rename(filepath.Join(dst, name+tmpSuffix), filepath.Join(dst, name)); err != nil {
			return err
		}
	}
	if err := removeOutputs(dst, keep); err != nil {
		return err
	}
	plain, err := decryptPD(cleanPD)
	if err != nil {
		return fmt.Errorf("clean packagedefinition: %w", err)
	}
	plain = patchLevel.ReplaceAll(plain, moddedPD)
	if err := writeFileAtomic(filepath.Join(dst, "packagedefinition.txt"), encryptPD(plain)); err != nil {
		return err
	}
	fmt.Printf("deployed %d mods to %s in %s\n", len(cfg.Enabled), dst, time.Since(start).Round(time.Millisecond))
	return nil
}

func undeploy(cfg *Config) error {
	runtime := cfg.runtimeDir()
	cleanPD, err := ensureCleanPD(cfg)
	if err != nil {
		return err
	}
	if err := removeTemps(runtime); err != nil {
		return err
	}
	// Restore packagedefinition first: without it the game ignores patch300.
	if err := writeFileAtomic(filepath.Join(runtime, "packagedefinition.txt"), cleanPD); err != nil {
		return err
	}
	if err := removeOutputs(runtime, nil); err != nil {
		return err
	}
	fmt.Println("game files restored to vanilla")
	return nil
}

// ensureCleanPD returns the vanilla packagedefinition, keeping a copy in
// clean/. A vanilla file in Runtime always refreshes the copy, which picks up
// game updates; a modded one falls back to the copy, or SMF's.
func ensureCleanPD(cfg *Config) ([]byte, error) {
	cleanPath := cfg.abs(filepath.Join("clean", "packagedefinition.txt"))
	cur, err := os.ReadFile(filepath.Join(cfg.runtimeDir(), "packagedefinition.txt"))
	if err != nil {
		return nil, err
	}
	plain, err := decryptPD(cur)
	if err != nil {
		return nil, fmt.Errorf("Runtime/packagedefinition.txt: %w", err)
	}
	if !bytes.Contains(plain, moddedPD) {
		if old, err := os.ReadFile(cleanPath); err != nil || !bytes.Equal(old, cur) {
			if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
				return nil, err
			}
			if err := writeFileAtomic(cleanPath, cur); err != nil {
				return nil, err
			}
		}
		return cur, nil
	}
	if b, err := os.ReadFile(cleanPath); err == nil {
		return b, nil
	}
	smf := filepath.Join(cfg.abs(cfg.Game), "SMF", "cleanPackageDefinition.txt")
	b, err := os.ReadFile(smf)
	if err != nil {
		return nil, errors.New("packagedefinition.txt is modded and no clean copy exists; verify game files once")
	}
	if p, err := decryptPD(b); err != nil || bytes.Contains(p, moddedPD) {
		return nil, fmt.Errorf("%s is not a clean packagedefinition", smf)
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
		return nil, err
	}
	return b, writeFileAtomic(cleanPath, b)
}

// removeOutputs deletes patch300 archives except those in keep (ours from a
// previous deploy, or SMF's).
func removeOutputs(runtime string, keep map[string]bool) error {
	ents, err := os.ReadDir(runtime)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if outputArchive.MatchString(e.Name()) && !keep[e.Name()] {
			if err := os.Remove(filepath.Join(runtime, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// removeTemps cleans up after an interrupted deploy.
func removeTemps(runtime string) error {
	ents, err := os.ReadDir(runtime)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), tmpSuffix) {
			if err := os.Remove(filepath.Join(runtime, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
