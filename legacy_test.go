package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func legacySetup(t *testing.T) (*legacyUpgrader, *Index) {
	t.Helper()
	tools, err := findTools()
	if err != nil {
		t.Skip(err)
	}
	qn3, err := toolPath("quickentity-3")
	if err != nil {
		t.Skip(err)
	}
	js := os.Getenv("H3MOD_LEGACY_QN")
	if js == "" {
		t.Skip("H3MOD_LEGACY_QN not set")
	}
	idx, err := loadIndex("../Runtime")
	if err != nil {
		t.Skip("no game files: ", err)
	}
	dir := t.TempDir()
	return &legacyUpgrader{tools: tools, qn3: qn3, jsDir: js, cacheDir: filepath.Join(dir, "cache"), workDir: filepath.Join(dir, "work")}, idx
}

// gameEntity finds a TEMP's TBLU and writes their ResourceTool files.
func gameEntity(t *testing.T, u *legacyUpgrader, idx *Index, temp uint64, dir string) (*Resource, *Resource, rtFiles) {
	t.Helper()
	tr, err := idx.lookup(temp)
	if err != nil {
		t.Fatal(err)
	}
	var br *Resource
	for _, ref := range tr.Refs {
		if r, err := idx.lookup(ref.Hash); err == nil && r.Type == "TBLU" {
			br = r
			break
		}
	}
	if br == nil {
		t.Fatal("no TBLU")
	}
	os.MkdirAll(dir, 0o755)
	rt, err := u.gameFiles(t.Context(), dir, tr, br, nil)
	if err != nil {
		t.Fatal(err)
	}
	return tr, br, rt
}

// sameEntities compares two QuickEntity 3.1 files' entities, allowing float
// noise from the round trip.
func sameEntities(t *testing.T, a, b string) {
	t.Helper()
	var x, y struct{ Entities map[string]any }
	for p, v := range map[string]any{a: &x, b: &y} {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatal(err)
		}
	}
	if len(x.Entities) != len(y.Entities) {
		t.Fatalf("%d entities vs %d", len(x.Entities), len(y.Entities))
	}
	var close func(a, b any) bool
	unitScale := func(m map[string]any) map[string]any {
		// QuickEntity before 3.0 has no transform scale; a scale of 1 is lost
		s, ok := m["scale"].(map[string]any)
		if !ok {
			return m
		}
		for _, v := range s {
			if f, _ := v.(float64); math.Abs(f-1) > 1e-4 {
				return m
			}
		}
		c := map[string]any{}
		for k, v := range m {
			if k != "scale" {
				c[k] = v
			}
		}
		return c
	}
	// a resource with the default flag may be written as just its hash
	defaultFlag := func(v any) any {
		if m, ok := v.(map[string]any); ok && len(m) == 2 && m["flag"] == "5F" {
			if r, ok := m["resource"].(string); ok {
				return r
			}
		}
		return v
	}
	close = func(a, b any) bool {
		a, b = defaultFlag(a), defaultFlag(b)
		if am, ok := a.(map[string]any); ok {
			if bm, ok := b.(map[string]any); ok {
				a, b = unitScale(am), unitScale(bm)
			}
		}
		switch av := a.(type) {
		case float64:
			bv, ok := b.(float64)
			return ok && math.Abs(av-bv) <= 1e-3*math.Max(1, math.Abs(av))
		case map[string]any:
			bv, ok := b.(map[string]any)
			if !ok || len(av) != len(bv) {
				return false
			}
			for k := range av {
				if !close(av[k], bv[k]) {
					return false
				}
			}
			return true
		case []any:
			bv, ok := b.([]any)
			if !ok || len(av) != len(bv) {
				return false
			}
			for i := range av {
				if !close(av[i], bv[i]) {
					return false
				}
			}
			return true
		}
		return a == b
	}
	bad := 0
	for k, e := range x.Entities {
		if !close(e, y.Entities[k]) {
			if bad++; bad <= 3 {
				em, _ := e.(map[string]any)
				fm, _ := y.Entities[k].(map[string]any)
				for f := range em {
					if !close(em[f], fm[f]) {
						if f == "properties" {
							ep, _ := em[f].(map[string]any)
							fp, _ := fm[f].(map[string]any)
							for p := range ep {
								if !close(ep[p], fp[p]) {
									a, _ := json.Marshal(ep[p])
									b, _ := json.Marshal(fp[p])
									t.Errorf("entity %s property %s:\n%.300s\n%.300s", k, p, a, b)
								}
							}
							for p := range fp {
								if _, ok := ep[p]; !ok {
									t.Errorf("entity %s property %s only in second", k, p)
								}
							}
							continue
						}
						a, _ := json.Marshal(em[f])
						b, _ := json.Marshal(fm[f])
						t.Errorf("entity %s %s:\n%.300s\n%.300s", k, f, a, b)
					}
				}
			}
		}
	}
}

func TestLegacyRoundTrip(t *testing.T) {
	u, idx := legacySetup(t)
	const temp = 0x0017852FD17918B3 // an outfit template
	for _, tt := range []struct {
		name string
		q    legacyQN
	}{
		{"1.136", legacyForPatch(0)},
		{"2.0", legacyForPatch(3)},
		{"2.1", legacyForPatch(4)},
		{"3.0", legacyForPatch(5)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			_, _, in := gameEntity(t, u, idx, temp, dir)
			direct := filepath.Join(dir, "direct.json")
			if err := u.convert31(t.Context(), dir, in, direct); err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(dir, "old.json")
			if err := u.convert(t.Context(), tt.q, dir, in, old); err != nil {
				t.Fatal(err)
			}
			gen := rtIn(dir, "gen.")
			if err := u.generate(t.Context(), tt.q, dir, old, gen); err != nil {
				t.Fatal(err)
			}
			back := filepath.Join(dir, "back.json")
			if err := u.convert31(t.Context(), dir, gen, back); err != nil {
				t.Fatal(err)
			}
			sameEntities(t, direct, back)
		})
	}
}

func TestLegacyPatch(t *testing.T) {
	u, idx := legacySetup(t)
	const temp = 0x0017852FD17918B3
	for _, v := range []float64{0, 3, 4, 5} {
		t.Run(strconv.FormatFloat(v, 'f', -1, 64), func(t *testing.T) {
			dir := t.TempDir()
			tr, br, in := gameEntity(t, u, idx, temp, dir)
			q := legacyForPatch(v)
			orig := filepath.Join(dir, "orig.json")
			if err := u.convert(t.Context(), q, dir, in, orig); err != nil {
				t.Fatal(err)
			}
			// rename one entity in the old format, and make the old patch
			b, _ := os.ReadFile(orig)
			n, err := parseJSON(b)
			if err != nil {
				t.Fatal(err)
			}
			ents := n.get("entities")
			var e *node
			if ents.isArr {
				e = ents.arr[0]
			} else {
				e = ents.obj[ents.keys[0]]
			}
			e.set("name", &node{raw: []byte(`"RenamedByPatch"`)})
			var buf bytes.Buffer
			n.encode(&buf)
			modified := filepath.Join(dir, "modified.json")
			os.WriteFile(modified, buf.Bytes(), 0o644)
			patch := filepath.Join(dir, "old.entity.patch.json")
			if q.script == "" {
				err = u.tools.run(t.Context(), dir, u.qn3, "patch", "generate", "--input1", orig, "--input2", modified, "--output", patch)
			} else {
				err = u.js(dir).call(q.script, "createPatchJSON", orig, modified, patch)
			}
			if err != nil {
				t.Fatal(err)
			}
			h, ptemp, _, err := readEntityHeader(patch)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("old patch: patchVersion %s, tempHash %016X", h.PatchVersion, ptemp)

			up, err := u.patch(t.Context(), patch, v, tr, br, nil)
			if err != nil {
				t.Fatal(err)
			}
			uh, utemp, utblu, err := readEntityHeader(up)
			if err != nil {
				t.Fatal(err)
			}
			if uh.PatchVersion.String() != "6" || utemp != tr.Hash || utblu != br.Hash {
				t.Fatalf("upgraded patch: version %s, %016X/%016X", uh.PatchVersion, utemp, utblu)
			}
			direct := filepath.Join(dir, "direct.json")
			if err := u.convert31(t.Context(), dir, in, direct); err != nil {
				t.Fatal(err)
			}
			applied := filepath.Join(dir, "applied.json")
			if err := u.tools.run(t.Context(), dir, u.tools.QuickEntity, "patch", "apply", "--input", direct, "--patch", up,
				"--output", applied, "--permissive", "--format-fix"); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(applied)
			if !bytes.Contains(got, []byte(`"RenamedByPatch"`)) {
				pb, _ := os.ReadFile(up)
				t.Fatalf("rename not applied; upgraded patch: %.500s", pb)
			}
			if pb, _ := os.ReadFile(up); bytes.Count(pb, []byte(`"SubEntityOperation"`))+bytes.Count(pb, []byte(`SetName`)) == 0 {
				t.Logf("upgraded patch: %.300s", pb)
			}
		})
	}
}

func TestLegacyEntity(t *testing.T) {
	u, idx := legacySetup(t)
	for _, v := range []float64{1.136, 2.0, 2.1, 3.0} {
		t.Run(strconv.FormatFloat(v, 'f', -1, 64), func(t *testing.T) {
			dir := t.TempDir()
			_, _, in := gameEntity(t, u, idx, 0x0017852FD17918B3, dir)
			// an entity.json in the old format, made by that QuickEntity
			q := legacyForEntity(v)
			if q.script == "quickentity1136" {
				q.idsMode = true
			}
			old := filepath.Join(dir, "old.entity.json")
			if err := u.convert(t.Context(), q, dir, in, old); err != nil {
				t.Fatal(err)
			}
			up, err := u.entity(t.Context(), old, v)
			if err != nil {
				t.Fatal(err)
			}
			if h, _, _, err := readEntityHeader(up); err != nil || h.QuickEntityVersion.String() != "3.1" {
				t.Fatalf("upgraded: %v, version %v", err, h)
			}
			direct := filepath.Join(dir, "direct.json")
			if err := u.convert31(t.Context(), dir, in, direct); err != nil {
				t.Fatal(err)
			}
			sameEntities(t, direct, up)
			if _, again := u.cached([]byte("entity"), must(os.ReadFile(old))); !again {
				t.Error("upgrade not cached")
			}
		})
	}
}

func must(b []byte, err error) []byte { return b }

// A mod's old-format patch deploys like SMF would deploy it.
func TestDeployLegacyPatch(t *testing.T) {
	u, idx := legacySetup(t)
	if _, err := os.Stat("../Runtime/chunk0.rpkg"); err != nil {
		t.Skip("no game files")
	}
	const temp = 0x0017852FD17918B3
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	_, _, in := gameEntity(t, u, idx, temp, work)
	q := legacyForPatch(4)
	orig := filepath.Join(work, "orig.json")
	if err := u.convert(t.Context(), q, work, in, orig); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(orig)
	n, _ := parseJSON(b)
	ents := n.get("entities")
	ents.obj[ents.keys[0]].set("name", &node{raw: []byte(`"RenamedByOldPatch"`)})
	var buf bytes.Buffer
	n.encode(&buf)
	modified := filepath.Join(work, "modified.json")
	os.WriteFile(modified, buf.Bytes(), 0o644)
	mod := filepath.Join(dir, "mods", "Old")
	os.MkdirAll(filepath.Join(mod, "content", "chunk0"), 0o755)
	os.WriteFile(filepath.Join(mod, "manifest.json"), []byte(`{"id":"t.old","name":"Old","description":"","authors":[],"version":"1.0.0","frameworkVersion":"2.33.0","contentFolders":["content"]}`), 0o644)
	if err := u.js(work).call(q.script, "createPatchJSON", orig, modified, filepath.Join(mod, "content", "chunk0", "x.entity.patch.json")); err != nil {
		t.Fatal(err)
	}

	gameDir, _ := filepath.Abs("..")
	cfg := &Config{Game: gameDir, Mods: filepath.Join(dir, "mods"), Enabled: []string{"Old"}, path: filepath.Join(dir, "h3mod.json")}
	d := newDeployer(cfg)
	d.log = t.Logf
	out := filepath.Join(dir, "out")
	if err := d.Deploy(t.Context(), false, out); err != nil {
		t.Fatal(err)
	}
	res, err := ReadArchive(filepath.Join(out, "chunk0patch300.rpkg"))
	if err != nil {
		t.Fatal(err)
	}
	var dt, db *Resource
	for _, r := range res {
		switch r.Type {
		case "TEMP":
			dt = r
		case "TBLU":
			db = r
		}
	}
	if dt == nil || db == nil || dt.Hash != temp {
		t.Fatal("entity not deployed")
	}
	check := filepath.Join(dir, "check")
	os.MkdirAll(check, 0o755)
	rt, err := u.gameFiles(t.Context(), check, dt, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	qn := filepath.Join(check, "qn.json")
	if err := u.convert31(t.Context(), check, rt, qn); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(qn); !bytes.Contains(got, []byte(`"RenamedByOldPatch"`)) {
		t.Fatal("old patch not applied")
	}
}
