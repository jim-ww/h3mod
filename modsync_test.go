package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSyncMods(t *testing.T) {
	root := t.TempDir()
	mods := filepath.Join(root, "mods")
	addMod := func(dir, id string) {
		os.MkdirAll(filepath.Join(mods, dir), 0o755)
		os.WriteFile(filepath.Join(mods, dir, "manifest.json"), []byte(`{"id": "`+id+`", "name": "`+dir+` Mod"}`), 0o644)
	}
	addMod("Alpha", "x.alpha")
	addMod("Beta", "x.beta")
	cfgPath := filepath.Join(root, "h3mod.json")

	// A first run enables everything.
	cfg, _ := loadConfig(cfgPath)
	cfg.Mods = mods
	if _, _, err := cfg.syncMods(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Enabled, []string{"Alpha", "Beta"}) {
		t.Fatalf("enabled %v", cfg.Enabled)
	}

	// Disabled mods stay disabled; new ones get enabled; gone ones drop out.
	cfg, _ = loadConfig(cfgPath)
	cfg.Enabled = []string{"Alpha"}
	cfg.save()
	addMod("Gamma", "x.gamma")
	os.RemoveAll(filepath.Join(mods, "Alpha"))
	cfg, _ = loadConfig(cfgPath)
	_, notes, err := cfg.syncMods()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Enabled, []string{"Gamma"}) || len(notes) != 2 {
		t.Fatalf("enabled %v, notes %v", cfg.Enabled, notes)
	}
	if again, _ := loadConfig(cfgPath); !slices.Equal(again.Enabled, cfg.Enabled) {
		t.Fatal("sync not saved")
	}

	// A config from before mods were tracked keeps its choices.
	os.WriteFile(cfgPath, []byte(`{"mods": "mods", "enabled": ["Beta"]}`), 0o644)
	cfg, _ = loadConfig(cfgPath)
	if _, _, err := cfg.syncMods(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Enabled, []string{"Beta"}) {
		t.Fatalf("old config enabled %v", cfg.Enabled)
	}

	// One with nothing enabled is a first run.
	os.WriteFile(cfgPath, []byte(`{"mods": "mods", "enabled": []}`), 0o644)
	cfg, _ = loadConfig(cfgPath)
	if _, _, err := cfg.syncMods(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Enabled) != 2 {
		t.Fatalf("empty old config enabled %v", cfg.Enabled)
	}
}

func TestResolveMod(t *testing.T) {
	mk := func(dir, id string) *Mod { return &Mod{Dir: dir, Manifest: &Manifest{ID: id, Name: dir}} }
	mods := []*Mod{mk("Blood More", "a.blood"), mk("Ninja AI", "b.ninja"), mk("Ninja Suit", "c.suit")}
	cfg := &Config{Enabled: []string{"Ninja AI"}}
	tests := []struct {
		arg, want string
	}{
		{"Ninja AI", "Ninja AI"},
		{"c.suit", "Ninja Suit"},
		{"1", "Ninja AI"}, // enabled first
		{"2", "Blood More"},
		{"blood", "Blood More"},
		{"suit", "Ninja Suit"},
		{"ninja", ""}, // ambiguous
		{"9", ""},
		{"nope", ""},
	}
	for _, tt := range tests {
		m, err := resolveMod(cfg, mods, tt.arg)
		got := ""
		if err == nil {
			got = m.Dir
		}
		if got != tt.want {
			t.Errorf("%q: got %q (%v), want %q", tt.arg, got, err, tt.want)
		}
	}
}
