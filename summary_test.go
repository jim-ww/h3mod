package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeploySummaryOwnership(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "mods"), 0o755)
	cfg := &Config{Game: dir, Mods: filepath.Join(dir, "mods"), path: filepath.Join(dir, "h3mod.json")}
	path := filepath.Join(dir, "deploySummary.json")

	if err := writeDeploySummary(cfg, newPlan(nil), path); err != nil {
		t.Fatal(err)
	}
	if err := removeDeploySummary(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("own summary not removed")
	}

	smf := []byte(`{"loadOrder":["x"],"lastServerSideStates":{}}`)
	os.WriteFile(path, smf, 0o644)
	if err := writeDeploySummary(cfg, newPlan(nil), path); err == nil {
		t.Error("overwrote a summary h3mod didn't write")
	}
	if err := removeDeploySummary(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(smf) {
		t.Error("removed a summary h3mod didn't write")
	}

	t.Setenv("XDG_DATA_HOME", dir)
	if got := summaryPath(cfg); got != filepath.Join(dir, "app.simple-mod-framework", "deploySummary.json") {
		t.Errorf("default path %s", got)
	}
	cfg.DeploySummary = "off"
	if summaryPath(cfg) != "" {
		t.Error("off still writes")
	}
}
