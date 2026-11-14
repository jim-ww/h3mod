package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Entities and patches made for QuickEntity before 3.1 (entity.json
// quickEntityVersion < 3.1, entity.patch.json patchVersion < 6) are upgraded
// like SMF does, into the cache rather than over the mod's file: the old
// QuickEntity generates game files, QuickEntity 3.1 converts them back, and a
// patch becomes the 3.1 difference between the patched and unpatched entity.
// SMF's QuickEntity 1.136, 2.0 and 2.1 are JavaScript (run with goja); 3.0
// is a binary.

// legacyQN is one old QuickEntity: a script in the legacy folder, or "" for
// the QuickEntity 3.0 binary.
type legacyQN struct {
	script  string
	idsMode bool // 1.136's convert takes an extra "ids" mode argument
}

// legacyForEntity picks the QuickEntity SMF uses for an entity.json version.
func legacyForEntity(v float64) legacyQN {
	switch {
	case v < 2.0:
		return legacyQN{script: "quickentity1136"}
	case v < 2.1:
		return legacyQN{script: "quickentity20"}
	case v < 3.0:
		return legacyQN{script: "quickentity"}
	}
	return legacyQN{}
}

// legacyForPatch picks the QuickEntity SMF uses for a patch version.
func legacyForPatch(v float64) legacyQN {
	switch {
	case v < 3:
		return legacyQN{script: "quickentity1136", idsMode: true}
	case v < 4:
		return legacyQN{script: "quickentity20"}
	case v < 5:
		return legacyQN{script: "quickentity"}
	}
	return legacyQN{}
}

// rtFiles are ResourceTool JSON files of an entity with their meta JSONs.
type rtFiles struct{ temp, tempMeta, tblu, tbluMeta string }

func rtIn(dir, prefix string) rtFiles {
	p := func(s string) string { return filepath.Join(dir, prefix+s) }
	return rtFiles{p("TEMP.json"), p("TEMP.meta.json"), p("TBLU.json"), p("TBLU.meta.json")}
}

type legacyUpgrader struct {
	tools    Tools
	qn3      string // QuickEntity 3.0 binary, found when needed
	jsDir    string // legacy QuickEntity scripts
	cacheDir string
	workDir  string
}

func (u *legacyUpgrader) js(work string) *jsModules { return newJSModules(u.jsDir, work) }

func (u *legacyUpgrader) generate(ctx context.Context, q legacyQN, work, in string, out rtFiles) error {
	if q.script == "" {
		if err := u.tools.run(ctx, work, u.qn3, "entity", "generate", "--input", in,
			"--output-factory", out.temp, "--output-factory-meta", out.tempMeta,
			"--output-blueprint", out.tblu, "--output-blueprint-meta", out.tbluMeta); err != nil {
			return err
		}
		return fixBlueprintHash(in, out.tbluMeta)
	}
	return u.js(work).call(q.script, "generate", "HM3", in, out.temp, out.tempMeta, out.tblu, out.tbluMeta)
}

// fixBlueprintHash corrects QuickEntity 3.0's blueprint meta, which it
// writes with the factory's hash.
func fixBlueprintHash(entity, meta string) error {
	_, _, tblu, err := readEntityHeader(entity)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(meta)
	if err != nil {
		return err
	}
	m, err := parseJSON(b)
	if err != nil {
		return err
	}
	raw, _ := marshalScalar(hashString(tblu))
	m.set("hash_value", &node{raw: raw})
	var buf bytes.Buffer
	m.encode(&buf)
	return os.WriteFile(meta, buf.Bytes(), 0o644)
}

func (u *legacyUpgrader) convert(ctx context.Context, q legacyQN, work string, in rtFiles, out string) error {
	if q.script == "" {
		return u.tools.run(ctx, work, u.qn3, "entity", "convert",
			"--input-factory", in.temp, "--input-factory-meta", in.tempMeta,
			"--input-blueprint", in.tblu, "--input-blueprint-meta", in.tbluMeta, "--output", out, "--lossless")
	}
	args := []any{"HM3", in.temp, in.tempMeta, in.tblu, in.tbluMeta, out}
	if q.idsMode {
		args = append([]any{"HM3", "ids"}, args[1:]...)
	}
	return u.js(work).call(q.script, "convert", args...)
}

func (u *legacyUpgrader) applyPatch(ctx context.Context, q legacyQN, work, in, patch, out string) error {
	if q.script == "" {
		return u.tools.run(ctx, work, u.qn3, "patch", "apply", "--input", in, "--patch", patch, "--output", out)
	}
	return u.js(work).call(q.script, "applyPatchJSON", in, patch, out)
}

// convert31 turns ResourceTool files into a QuickEntity 3.1 entity.
func (u *legacyUpgrader) convert31(ctx context.Context, work string, in rtFiles, out string) error {
	return u.tools.run(ctx, work, u.tools.QuickEntity, "entity", "convert",
		"--input-factory", in.temp, "--input-factory-meta", in.tempMeta,
		"--input-blueprint", in.tblu, "--input-blueprint-meta", in.tbluMeta, "--output", out, "--lossless")
}

// cached returns the cache file for the inputs, and whether it exists.
func (u *legacyUpgrader) cached(parts ...[]byte) (string, bool) {
	h := sha256.New()
	fmt.Fprintf(h, "h3mod legacy v1\n%s\n%s\n%s\n%s\n", u.tools.ResourceTool, u.tools.QuickEntity, u.qn3, u.jsDir)
	for _, p := range parts {
		fmt.Fprintf(h, "%d\n", len(p))
		h.Write(p)
	}
	path := filepath.Join(u.cacheDir, hex.EncodeToString(h.Sum(nil))+".json")
	_, err := os.Stat(path)
	return path, err == nil
}

func (u *legacyUpgrader) workspace() (string, func(), error) {
	if err := os.MkdirAll(u.workDir, 0o755); err != nil {
		return "", nil, err
	}
	work, err := os.MkdirTemp(u.workDir, "legacy-")
	if err != nil {
		return "", nil, err
	}
	return work, func() { os.RemoveAll(work) }, nil
}

// store moves a finished file into the cache.
func (u *legacyUpgrader) store(src, dst string) error {
	if err := os.MkdirAll(u.cacheDir, 0o755); err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, b)
}

// entity upgrades an entity.json of the given version to QuickEntity 3.1,
// keeping its comments, and returns the upgraded file.
func (u *legacyUpgrader) entity(ctx context.Context, path string, version float64) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	dst, ok := u.cached([]byte("entity"), src)
	if ok {
		return dst, nil
	}
	work, done, err := u.workspace()
	if err != nil {
		return "", err
	}
	defer done()
	rt := rtIn(work, "gen.")
	if err := u.generate(ctx, legacyForEntity(version), work, path, rt); err != nil {
		return "", err
	}
	out := filepath.Join(work, "entity.json")
	if err := u.convert31(ctx, work, rt, out); err != nil {
		return "", err
	}
	if err := keepComments(src, out, version); err != nil {
		return "", err
	}
	return dst, u.store(out, dst)
}

// keepComments copies comments into the upgraded entity: before 3.0 they
// were entities of type "comment", since then a "comments" list.
func keepComments(orig []byte, upgraded string, version float64) error {
	o, err := parseJSON(stripJSONC(orig))
	if err != nil {
		return err
	}
	var comments *node
	if version < 3 {
		comments = &node{isArr: true, arr: []*node{}}
		if ents := o.get("entities"); ents != nil {
			for _, k := range ents.keys {
				e := ents.obj[k]
				if t, _ := e.get("type").text(); t != "comment" {
					continue
				}
				c := &node{obj: map[string]*node{}}
				for _, f := range []string{"parent", "name", "text"} {
					if v := e.get(f); v != nil {
						c.set(f, v)
					}
				}
				comments.arr = append(comments.arr, c)
			}
		}
	} else if comments = o.get("comments"); comments == nil {
		return nil
	}
	b, err := os.ReadFile(upgraded)
	if err != nil {
		return err
	}
	u, err := parseJSON(b)
	if err != nil {
		return err
	}
	u.set("comments", comments)
	var buf bytes.Buffer
	u.encode(&buf)
	return os.WriteFile(upgraded, buf.Bytes(), 0o644)
}

// gameFiles writes an entity's ResourceTool files named by hash, as old
// QuickEntity takes the entity's hashes from the file names.
func (u *legacyUpgrader) gameFiles(ctx context.Context, work string, temp, tblu *Resource, names map[uint64]string) (rtFiles, error) {
	for _, r := range []*Resource{temp, tblu} {
		if err := writeEntityInput(work, r, names); err != nil {
			return rtFiles{}, err
		}
		base := filepath.Join(work, hashString(r.Hash)+"."+r.Type)
		if err := u.tools.run(ctx, work, u.tools.ResourceTool, "HM3", "convert", r.Type, "in."+r.Type, base+".json", "--simple"); err != nil {
			return rtFiles{}, err
		}
		if err := os.Rename(filepath.Join(work, "in."+r.Type+".meta.json"), base+".meta.json"); err != nil {
			return rtFiles{}, err
		}
	}
	t, b := filepath.Join(work, hashString(temp.Hash)+".TEMP"), filepath.Join(work, hashString(tblu.Hash)+".TBLU")
	return rtFiles{t + ".json", t + ".meta.json", b + ".json", b + ".meta.json"}, nil
}

// patchCached is patch, resolving reference names only when the upgrade
// isn't cached.
func (u *legacyUpgrader) patchCached(ctx context.Context, path string, version float64, temp, tblu *Resource, names func() (map[uint64]string, error)) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	td, err := temp.Data()
	if err != nil {
		return "", err
	}
	bd, err := tblu.Data()
	if err != nil {
		return "", err
	}
	if dst, ok := u.cached([]byte("patch"), src, td, bd); ok {
		return dst, nil
	}
	n, err := names()
	if err != nil {
		return "", err
	}
	return u.patch(ctx, path, version, temp, tblu, n)
}

// patch upgrades an entity.patch.json of the given version against the
// game's entity (temp, tblu, written with reference names) to patch
// version 6 and returns the upgraded file.
func (u *legacyUpgrader) patch(ctx context.Context, path string, version float64, temp, tblu *Resource, names map[uint64]string) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	td, err := temp.Data()
	if err != nil {
		return "", err
	}
	bd, err := tblu.Data()
	if err != nil {
		return "", err
	}
	dst, ok := u.cached([]byte("patch"), src, td, bd)
	if ok {
		return dst, nil
	}
	work, done, err := u.workspace()
	if err != nil {
		return "", err
	}
	defer done()
	game, err := u.gameFiles(ctx, work, temp, tblu, names)
	if err != nil {
		return "", err
	}
	q := legacyForPatch(version)
	in := filepath.Join(work, "orig.json")
	patched := filepath.Join(work, "patched.json")
	if err := u.convert(ctx, q, work, game, in); err != nil {
		return "", err
	}
	if err := u.applyPatch(ctx, q, work, in, path, patched); err != nil {
		return "", err
	}
	var qn31 [2]string
	for i, e := range []string{in, patched} {
		rt := rtIn(work, "gen"+strconv.Itoa(i)+".")
		if err := u.generate(ctx, q, work, e, rt); err != nil {
			return "", err
		}
		qn31[i] = filepath.Join(work, "qn31-"+strconv.Itoa(i)+".json")
		if err := u.convert31(ctx, work, rt, qn31[i]); err != nil {
			return "", err
		}
	}
	out := filepath.Join(work, "patch.json")
	if err := u.tools.run(ctx, work, u.tools.QuickEntity, "patch", "generate",
		"--input1", qn31[0], "--input2", qn31[1], "--output", out, "--format-fix"); err != nil {
		return "", err
	}
	return dst, u.store(out, dst)
}
