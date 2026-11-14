package main

import (
	"strings"
	"testing"
)

func TestDeployStatus(t *testing.T) {
	mod := func(dir, version string, opts ...string) *Mod {
		m := &Mod{Dir: dir, Manifest: &Manifest{ID: dir, Name: dir, Version: version}}
		for _, o := range opts {
			m.Manifest.Options = append(m.Manifest.Options, Option{Name: o, Type: "checkbox"})
		}
		return m
	}
	a, b, c, d := mod("A", "1.0"), mod("B", "1.0"), mod("C", "2.0", "Blood"), mod("D", "1.0")
	mods := []*Mod{a, b, c, d}
	cfg := &Config{Enabled: []string{"A", "B", "C"}}
	s := &State{}
	if line, pending := deployStatus(cfg, s, mods); line != "nothing deployed; not deployed yet: 3 to add" || !pending {
		t.Errorf("before any deploy: %q %v", line, pending)
	}
	s.recordDeploy(cfg, mods, cfg.skipIntro())
	if line, pending := deployStatus(cfg, s, mods); pending || strings.Contains(line, "not deployed") {
		t.Errorf("right after deploy: %q %v", line, pending)
	}
	for _, m := range []*Mod{a, b, c} {
		if st := modDeployStatus(cfg, s, m); st != "deployed" {
			t.Errorf("%s: %q", m.Dir, st)
		}
	}

	cfg.Enabled = []string{"C", "A", "D"} // B disabled, D added, order changed
	cfg.saveSelection(c, []string{"Blood"})
	c.Manifest.Version = "2.1"
	off := false
	cfg.SkipIntro = &off
	want := map[*Mod]string{
		a: "deployed",
		b: "deployed, removed on next deploy",
		c: "updated, not deployed yet",
		d: "not deployed yet",
	}
	for m, w := range want {
		if st := modDeployStatus(cfg, s, m); st != w {
			t.Errorf("%s: %q, want %q", m.Dir, st, w)
		}
	}
	line, pending := deployStatus(cfg, s, mods)
	if !pending || !strings.HasSuffix(line, "not deployed yet: 1 to add, 1 to remove, 1 changed, load order, skip intro") {
		t.Errorf("after changes: %q", line)
	}

	// a deployed mod whose folder was deleted still has to come out
	if line, _ := deployStatus(cfg, s, []*Mod{a, c, d}); !strings.Contains(line, "1 to remove") {
		t.Errorf("after deleting B: %q", line)
	}
}
