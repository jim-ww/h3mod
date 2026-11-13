package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

// entityJob produces one entity (a TEMP/TBLU pair) by applying patches, in
// load order, to the game's version, a mod's raw files or a QuickEntity JSON.
type entityJob struct {
	chunk      int
	temp, tblu uint64
	baseTemp   *Resource // raw files from an earlier mod; nil means the game's
	baseTblu   *Resource
	qn         string   // entity.json to start from instead of binary files
	patches    []string // entity.patch.json files
}

// entityHeader holds the fields h3mod needs from entity.json and
// entity.patch.json files.
type entityHeader struct {
	TempHash           string          `json:"tempHash"`
	TbluHash           string          `json:"tbluHash"`
	PatchVersion       json.Number     `json:"patchVersion"`
	QuickEntityVersion json.Number     `json:"quickEntityVersion"`
	Patch              json.RawMessage `json:"patch"`
}

func readEntityHeader(path string) (*entityHeader, uint64, uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	var h entityHeader
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, 0, 0, err
	}
	temp, err := parseHash(h.TempHash)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("tempHash: %w", err)
	}
	tblu, err := parseHash(h.TbluHash)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("tbluHash: %w", err)
	}
	return &h, temp, tblu, nil
}

// Tools are the external converters for entity files, found in PATH (the
// nix package and dev shell provide them).
type Tools struct {
	ResourceTool string
	QuickEntity  string
}

func findTools() (Tools, error) {
	var t Tools
	var err error
	if t.ResourceTool, err = toolPath("ResourceTool"); err != nil {
		return t, err
	}
	t.QuickEntity, err = toolPath("quickentity-rs")
	return t, err
}

func toolPath(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH; run h3mod via `nix run` or inside `nix develop`", name)
	}
	// Resolved store paths identify the tool version, which keys the cache.
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

func (t Tools) run(ctx context.Context, dir string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", filepath.Base(name), strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return nil
}

// entityRunner runs entity jobs with a result cache.
type entityRunner struct {
	tools    Tools
	idx      *Index
	pathList string // hash_list.txt naming references, as SMF's rpkg-tool does
	cacheDir string // finished entities, by input hash
	workDir  string // scratch space for running jobs

	names func() (map[uint64]string, error) // paths for all jobs' references
}

func (er *entityRunner) runAll(ctx context.Context, jobs []*entityJob) ([][2]*Resource, error) {
	// Resolving paths scans the whole hash list, so it happens once, and only
	// when some entity isn't cached.
	er.names = sync.OnceValues(func() (map[uint64]string, error) {
		want := map[uint64]bool{}
		for _, j := range jobs {
			if j.qn != "" {
				continue
			}
			temp, tblu, err := er.base(j)
			if err != nil {
				return nil, err
			}
			for _, r := range append(slices.Clone(temp.Refs), tblu.Refs...) {
				want[r.Hash] = true
			}
		}
		return resolvePaths(er.pathList, want)
	})
	out := make([][2]*Resource, len(jobs))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(runtime.NumCPU())
	for i, j := range jobs {
		g.Go(func() error {
			temp, tblu, err := er.run(ctx, j)
			if err != nil {
				return fmt.Errorf("entity %016X: %w", j.temp, err)
			}
			out[i] = [2]*Resource{temp, tblu}
			return nil
		})
	}
	return out, g.Wait()
}

func (er *entityRunner) base(j *entityJob) (temp, tblu *Resource, err error) {
	temp, tblu = j.baseTemp, j.baseTblu
	if temp == nil {
		if temp, err = er.idx.lookup(j.temp); err != nil {
			return nil, nil, err
		}
	}
	if tblu == nil {
		if tblu, err = er.idx.lookup(j.tblu); err != nil {
			return nil, nil, err
		}
	}
	return temp, tblu, nil
}

func (er *entityRunner) run(ctx context.Context, j *entityJob) (*Resource, *Resource, error) {
	var temp, tblu *Resource
	var err error
	if j.qn == "" {
		if temp, tblu, err = er.base(j); err != nil {
			return nil, nil, err
		}
	}
	key, err := er.cacheKey(j, temp, tblu)
	if err != nil {
		return nil, nil, err
	}
	cached := filepath.Join(er.cacheDir, key)
	if res, err := loadEntity(cached, j); err == nil {
		return res[0], res[1], nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}

	if err := os.MkdirAll(er.workDir, 0o755); err != nil {
		return nil, nil, err
	}
	work, err := os.MkdirTemp(er.workDir, hashString(j.temp)+"-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(work)
	if err := er.build(ctx, work, j, temp, tblu); err != nil {
		return nil, nil, err
	}
	res, err := loadEntity(work, j)
	if err != nil {
		return nil, nil, err
	}
	if err := storeEntity(work, cached); err != nil {
		return nil, nil, err
	}
	return res[0], res[1], nil
}

// build runs the same pipeline as SMF: binary -> ResourceTool JSON ->
// QuickEntity JSON -> patches -> back.
func (er *entityRunner) build(ctx context.Context, work string, j *entityJob, temp, tblu *Resource) error {
	t := er.tools
	qn := filepath.Join(work, "entity.json")
	if j.qn != "" {
		b, err := os.ReadFile(j.qn)
		if err != nil {
			return err
		}
		if err := os.WriteFile(qn, b, 0o644); err != nil {
			return err
		}
	} else {
		names, err := er.names()
		if err != nil {
			return err
		}
		for _, r := range []*Resource{temp, tblu} {
			if err := writeEntityInput(work, r, names); err != nil {
				return err
			}
		}
		for _, typ := range []string{"TEMP", "TBLU"} {
			if err := t.run(ctx, work, t.ResourceTool, "HM3", "convert", typ, "in."+typ, "in."+typ+".json", "--simple"); err != nil {
				return err
			}
		}
		if err := t.run(ctx, work, t.QuickEntity, "entity", "convert",
			"--input-factory", "in.TEMP.json", "--input-factory-meta", "in.TEMP.meta.json",
			"--input-blueprint", "in.TBLU.json", "--input-blueprint-meta", "in.TBLU.meta.json",
			"--output", qn, "--lossless"); err != nil {
			return err
		}
	}
	patched := filepath.Join(work, "patched.json")
	for _, p := range j.patches {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		if err := t.run(ctx, work, t.QuickEntity, "patch", "apply",
			"--input", qn, "--patch", abs, "--output", patched, "--permissive", "--format-fix"); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if err := os.Rename(patched, qn); err != nil {
			return err
		}
	}
	if err := t.run(ctx, work, t.QuickEntity, "entity", "generate", "--input", qn,
		"--output-factory", "out.TEMP.json", "--output-factory-meta", "TEMP.meta.json",
		"--output-blueprint", "out.TBLU.json", "--output-blueprint-meta", "TBLU.meta.json"); err != nil {
		return err
	}
	for _, typ := range []string{"TEMP", "TBLU"} {
		if err := t.run(ctx, work, t.ResourceTool, "HM3", "generate", typ, "out."+typ+".json", typ, "--simple"); err != nil {
			return err
		}
	}
	return nil
}

func writeEntityInput(dir string, r *Resource, names map[uint64]string) error {
	data, err := r.Data()
	if err != nil {
		return err
	}
	meta, err := encodeMetaJSON(r, names)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "in."+r.Type), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "in."+r.Type+".meta.json"), meta, 0o644)
}

// loadEntity reads a built entity: TEMP, TBLU and their meta JSON files.
func loadEntity(dir string, j *entityJob) ([2]*Resource, error) {
	var out [2]*Resource
	for i, typ := range []string{"TEMP", "TBLU"} {
		data, err := os.ReadFile(filepath.Join(dir, typ))
		if err != nil {
			return out, err
		}
		meta, err := os.ReadFile(filepath.Join(dir, typ+".meta.json"))
		if err != nil {
			return out, err
		}
		refs, err := decodeMetaRefs(meta)
		if err != nil {
			return out, fmt.Errorf("%s meta: %w", typ, err)
		}
		hash := j.temp
		if typ == "TBLU" {
			hash = j.tblu
		}
		if out[i], err = bin1Resource(hash, typ, data, refs); err != nil {
			return out, err
		}
	}
	return out, nil
}

// storeEntity copies a built entity into the cache via a temp dir and rename,
// so an interrupted run never leaves a partial cache entry.
func storeEntity(work, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + tmpSuffix
	os.RemoveAll(tmp)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"TEMP", "TBLU", "TEMP.meta.json", "TBLU.meta.json"} {
		b, err := os.ReadFile(filepath.Join(work, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, name), b, 0o644); err != nil {
			return err
		}
	}
	os.RemoveAll(dst)
	return os.Rename(tmp, dst)
}

// cacheKey hashes everything an entity build depends on.
func (er *entityRunner) cacheKey(j *entityJob, temp, tblu *Resource) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "h3mod entity v2\n%s\n%s\n%016X %016X\n", er.tools.ResourceTool, er.tools.QuickEntity, j.temp, j.tblu)
	if st, err := os.Stat(er.pathList); err == nil {
		fmt.Fprintf(h, "%s %d %d\n", er.pathList, st.Size(), st.ModTime().UnixNano())
	}
	if j.qn != "" {
		b, err := os.ReadFile(j.qn)
		if err != nil {
			return "", err
		}
		h.Write(b)
	} else {
		for _, r := range []*Resource{temp, tblu} {
			data, err := r.Data()
			if err != nil {
				return "", err
			}
			meta, err := encodeMetaJSON(r, nil)
			if err != nil {
				return "", err
			}
			h.Write(data)
			h.Write(meta)
		}
	}
	for _, p := range j.patches {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "\npatch %d\n", len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
