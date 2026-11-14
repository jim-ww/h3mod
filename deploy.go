package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const tmpSuffix = ".h3mod-tmp"

var (
	patchLevel     = regexp.MustCompile(`patchlevel=[0-9]+`)
	moddedPD       = []byte("patchlevel=310")
	errUnsupported = errors.New("enabled mods use unsupported features (use -skip-unsupported to deploy without them)")
)

// Deployer writes mods into the game. Base game archives are never written:
// output is chunkNpatch300.rpkg files plus packagedefinition.txt.
type Deployer struct {
	cfg       *Config
	game      *gameData
	log       func(format string, args ...any)
	skipIntro bool // from the config unless the deploy flag says otherwise
}

func newDeployer(cfg *Config) *Deployer {
	return &Deployer{
		cfg:       cfg,
		game:      newGameData(cfg),
		log:       func(format string, args ...any) { fmt.Printf(format+"\n", args...) },
		skipIntro: cfg.skipIntro(),
	}
}

func (d *Deployer) statePath() string { return d.cfg.abs("state.json") }

func printSkipped(p *Plan) {
	for _, m := range slices.Sorted(maps.Keys(p.Skipped)) {
		var parts []string
		for _, what := range slices.Sorted(maps.Keys(p.Skipped[m])) {
			parts = append(parts, fmt.Sprintf("%s ×%d", what, p.Skipped[m][what]))
		}
		fmt.Printf("  %s: %s\n", m, strings.Join(parts, ", "))
	}
}

// Deploy builds and installs the enabled mods. With out set, output goes
// there instead of the game, which is left untouched.
func (d *Deployer) Deploy(ctx context.Context, skipUnsupported bool, out string) error {
	start := time.Now()
	if len(d.cfg.Enabled) == 0 && out == "" {
		d.log("no mods enabled (see status), so removing deployed mods")
		return d.Undeploy()
	}
	unlock, err := lockGame(d.cfg)
	if err != nil {
		return err
	}
	defer unlock()
	p, err := buildPlan(d.cfg, d.game, true)
	if err != nil {
		return err
	}
	if p.Reordered {
		if err := d.cfg.save(); err != nil {
			return err
		}
	}
	for _, n := range p.Notes {
		d.log("note: %s", n)
	}
	if len(p.Skipped) > 0 {
		fmt.Println("unsupported, not deployed:")
		printSkipped(p)
		if !skipUnsupported {
			return errUnsupported
		}
	}
	if err := d.buildEntities(ctx, p); err != nil {
		return err
	}
	if err := d.buildTextures(p); err != nil {
		return err
	}
	if err := d.buildDeltas(ctx, p); err != nil {
		return err
	}
	if err := d.buildRTLVs(ctx, p); err != nil {
		return err
	}
	if err := d.buildWWEVs(p); err != nil {
		return err
	}
	if err := d.patchRepository(p); err != nil {
		return err
	}
	if err := d.patchLocalisation(p); err != nil {
		return err
	}
	if err := d.applyJSONEdits(p); err != nil {
		return err
	}
	if err := d.registerBlobs(p); err != nil {
		return err
	}

	cleanPD, err := ensureCleanPD(d.cfg)
	if err != nil {
		return err
	}
	dst := d.cfg.runtimeDir()
	state := &State{}
	if out != "" {
		dst = out
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
	} else if state, err = loadState(d.statePath()); err != nil {
		return err
	}
	if err := removeTemps(dst); err != nil {
		return err
	}

	// Stage every changed archive fully before touching any game file.
	next := map[string]Output{}
	var staged []string
	for _, c := range slices.Sorted(maps.Keys(p.Chunks)) {
		name := fmt.Sprintf("chunk%dpatch%d.rpkg", c, outputPatch)
		res := slices.Collect(maps.Values(p.Chunks[c]))
		fp, err := fingerprint(res)
		if err != nil {
			return err
		}
		if old, ok := state.Archives[name]; ok && old.Inputs == fp && fileSize(filepath.Join(dst, name)) == old.Size {
			next[name] = old
			d.log("%s: %d files, unchanged", name, len(res))
			continue
		}
		tmp := filepath.Join(dst, name+tmpSuffix)
		if err := WritePatchArchive(tmp, res); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
		next[name] = Output{Inputs: fp, Size: fileSize(tmp)}
		staged = append(staged, name)
		d.log("%s: %d files", name, len(res))
	}

	// Each rename is atomic; packagedefinition goes last so the game only
	// loads patch archives once all of them are in place.
	for _, name := range staged {
		if err := renameDurable(filepath.Join(dst, name+tmpSuffix), filepath.Join(dst, name)); err != nil {
			return err
		}
	}
	if err := removeOutputs(dst, next); err != nil {
		return err
	}
	plain, err := decryptPD(cleanPD)
	if err != nil {
		return fmt.Errorf("clean packagedefinition: %w", err)
	}
	plain = patchLevel.ReplaceAll(plain, moddedPD)
	if plain, err = applyPD(plain, p.PD); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dst, "packagedefinition.txt"), encryptPD(plain)); err != nil {
		return err
	}
	if out == "" {
		if d.skipIntro || len(p.Thumbs) > 0 {
			err = writeThumbs(d.cfg, d.skipIntro, p.Thumbs)
		} else {
			err = restoreThumbs(d.cfg)
		}
		if err != nil {
			return err
		}
	}
	summary := summaryPath(d.cfg)
	if out != "" {
		summary = filepath.Join(out, "deploySummary.json")
	}
	if err := writeDeploySummary(d.cfg, p, summary); err != nil {
		return fmt.Errorf("deploy summary for Peacock: %w", err)
	}
	retail, placed := filepath.Join(d.cfg.abs(d.cfg.Game), "Retail", "mods"), state.Retail
	if out != "" {
		retail, placed = filepath.Join(out, "Retail", "mods"), nil
	}
	nowRetail, err := syncRetail(retail, p.Retail, placed, d.log)
	if out == "" {
		state.Retail = nowRetail
		if err != nil {
			return errors.Join(err, state.save(d.statePath()))
		}
	} else if err != nil {
		return err
	}
	if out == "" {
		state.Deployed, state.Mods, state.Archives = time.Now(), slices.Clone(d.cfg.Enabled), next
		if err := state.save(d.statePath()); err != nil {
			return err
		}
	}
	d.log("deployed %d mods to %s in %s", len(d.cfg.Enabled), dst, time.Since(start).Round(time.Millisecond))
	return nil
}

func (d *Deployer) buildEntities(ctx context.Context, p *Plan) error {
	if len(p.Entities) == 0 {
		return nil
	}
	tools, err := findTools()
	if err != nil {
		return err
	}
	pathList, err := d.game.pathList()
	if err != nil {
		return err
	}
	idx, err := d.game.index()
	if err != nil {
		return err
	}
	er := &entityRunner{
		tools:    tools,
		idx:      idx,
		pathList: pathList,
		cacheDir: d.cfg.abs(filepath.Join("cache", "entities")),
		workDir:  d.cfg.abs(filepath.Join("cache", "work")),
	}
	start := time.Now()
	results, err := er.runAll(ctx, p.Entities)
	if err != nil {
		return err
	}
	for i, j := range p.Entities {
		p.put(j.chunk, results[i][0])
		p.put(j.chunk, results[i][1])
	}
	d.log("entities: %d in %s", len(p.Entities), time.Since(start).Round(time.Millisecond))
	return nil
}

func (d *Deployer) buildTextures(p *Plan) error {
	if len(p.Textures) == 0 {
		return nil
	}
	idx, err := d.game.index()
	if err != nil {
		return err
	}
	start := time.Now()
	cache := d.cfg.abs(filepath.Join("cache", "textures"))
	vanilla := func(h uint64) *Resource { return idx.res[h] }
	for _, j := range p.Textures {
		if err := j.build(cache, vanilla); err != nil {
			return fmt.Errorf("%s: %w", j.path, err)
		}
	}
	d.log("textures: %d in %s", len(p.Textures), time.Since(start).Round(time.Millisecond))
	return nil
}

// patchLocalisation applies manifest strings to their LOCRs, starting from
// a version a mod ships or the game's.
func (d *Deployer) patchLocalisation(p *Plan) error {
	if len(p.Loc) == 0 {
		return nil
	}
	byLOCR := map[uint64][]locEdit{}
	var order []uint64
	for _, e := range p.Loc {
		if _, ok := byLOCR[e.locr]; !ok {
			order = append(order, e.locr)
		}
		byLOCR[e.locr] = append(byLOCR[e.locr], e)
	}
	idx, err := d.game.index()
	if err != nil {
		return err
	}
	for _, h := range order {
		chunk, base := p.staged(h)
		if base == nil {
			if base, err = idx.lookup(h); err != nil {
				return err
			}
			chunk = idx.chunkOf(base)
		}
		data, err := base.Data()
		if err != nil {
			return err
		}
		l, err := parseLOCR(data)
		if err != nil {
			return fmt.Errorf("LOCR %016X: %w", h, err)
		}
		for _, e := range byLOCR[h] {
			l.set(e.lang, e.key, e.text)
		}
		r := locrResource(h, l)
		r.Refs = base.Refs
		p.put(chunk, r)
	}
	return nil
}

func (d *Deployer) buildDeltas(ctx context.Context, p *Plan) error {
	if len(p.Deltas) == 0 {
		return nil
	}
	idx, err := d.game.index()
	if err != nil {
		return err
	}
	for _, j := range p.Deltas {
		if err := j.build(ctx, idx, d.cfg.abs(filepath.Join("cache", "deltas")), d.cfg.abs(filepath.Join("cache", "work"))); err != nil {
			return fmt.Errorf("%s: %w", j.path, err)
		}
	}
	return nil
}

// buildWWEVs places rebuilt WWEVs over anything mods shipped, like SMF does.
func (d *Deployer) buildWWEVs(p *Plan) error {
	if len(p.WWEVs) == 0 {
		return nil
	}
	idx, err := d.game.index()
	if err != nil {
		return err
	}
	for _, j := range p.WWEVs {
		if err := j.build(idx); err != nil {
			return fmt.Errorf("WWEV %s: %w", hashString(j.out.Hash), err)
		}
		for _, res := range p.Chunks {
			delete(res, j.out.Hash)
		}
		p.put(j.chunk, j.out)
	}
	return nil
}

func (d *Deployer) buildRTLVs(ctx context.Context, p *Plan) error {
	if len(p.RTLVs) == 0 {
		return nil
	}
	tools, err := findTools()
	if err != nil {
		return err
	}
	for _, j := range p.RTLVs {
		if err := j.build(ctx, tools, d.cfg.abs(filepath.Join("cache", "rtlv")), d.cfg.abs(filepath.Join("cache", "work"))); err != nil {
			return fmt.Errorf("%s: %w", j.path, err)
		}
	}
	return nil
}

func (d *Deployer) patchRepository(p *Plan) error {
	if len(p.Repo) == 0 {
		return nil
	}
	base := p.Chunks[0][repoHash] // a mod may ship a whole repository
	if base == nil {
		idx, err := d.game.index()
		if err != nil {
			return err
		}
		if base, err = idx.lookup(repoHash); err != nil {
			return err
		}
	}
	repo, err := patchRepository(base, p.Repo)
	if err != nil {
		return err
	}
	p.put(0, repo)
	return nil
}

// Undeploy restores vanilla game files.
func (d *Deployer) Undeploy() error {
	unlock, err := lockGame(d.cfg)
	if err != nil {
		return err
	}
	defer unlock()
	runtime := d.cfg.runtimeDir()
	cleanPD, err := ensureCleanPD(d.cfg)
	if err != nil {
		return err
	}
	if err := removeTemps(runtime); err != nil {
		return err
	}
	// packagedefinition first: without it the game ignores mod archives.
	if err := writeFileAtomic(filepath.Join(runtime, "packagedefinition.txt"), cleanPD); err != nil {
		return err
	}
	if err := removeOutputs(runtime, nil); err != nil {
		return err
	}
	if err := restoreThumbs(d.cfg); err != nil {
		return err
	}
	if err := removeDeploySummary(summaryPath(d.cfg)); err != nil {
		return err
	}
	state, err := loadState(d.statePath())
	if err != nil {
		return err
	}
	left, err := syncRetail(filepath.Join(d.cfg.abs(d.cfg.Game), "Retail", "mods"), nil, state.Retail, d.log)
	if err != nil {
		return errors.Join(err, (&State{Retail: left}).save(d.statePath()))
	}
	if err := (&State{}).save(d.statePath()); err != nil {
		return err
	}
	d.log("game files restored to vanilla")
	return nil
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return st.Size()
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

// removeOutputs deletes mod archives (patch numbers 200 to 300, which SMF
// manages too and the game never ships) except those in keep.
func removeOutputs(dir string, keep map[string]Output) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		m := archiveName.FindStringSubmatch(e.Name())
		if m == nil || m[2] == "" {
			continue
		}
		if n, _ := strconv.Atoi(m[2]); n < managedPatchMin || n > outputPatch {
			continue
		}
		if _, ok := keep[e.Name()]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// removeTemps cleans up after an interrupted deploy.
func removeTemps(dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), tmpSuffix) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
