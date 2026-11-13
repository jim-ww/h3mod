package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDeployDelta(t *testing.T) {
	xdelta, err := exec.LookPath("xdelta3")
	if err != nil {
		t.Skip("no xdelta3")
	}
	if _, err := os.Stat("../Runtime/chunk0.rpkg"); err != nil {
		t.Skip("no game files")
	}
	idx, err := loadIndex("../Runtime")
	if err != nil {
		t.Fatal(err)
	}
	const line = 0x00424FD3A53A6A5B // a small LINE resource
	base, err := idx.lookup(line)
	if err != nil {
		t.Fatal(err)
	}
	orig, err := base.Data()
	if err != nil {
		t.Fatal(err)
	}
	want := append(bytes.Clone(orig), "patched"...)

	dir := t.TempDir()
	mod := filepath.Join(dir, "mods", "Delta", "content", "chunk0")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	src, target := filepath.Join(dir, "src"), filepath.Join(dir, "target")
	os.WriteFile(src, orig, 0o644)
	os.WriteFile(target, want, 0o644)
	delta := filepath.Join(mod, "00424FD3A53A6A5B~LINE.delta")
	if out, err := exec.Command(xdelta, "-e", "-s", src, target, delta).CombinedOutput(); err != nil {
		t.Fatalf("xdelta3: %v: %s", err, out)
	}
	os.WriteFile(filepath.Join(dir, "mods", "Delta", "manifest.json"),
		[]byte(`{"id":"Delta","name":"Delta","version":"1","contentFolders":["content"]}`), 0o644)

	gameDir, _ := filepath.Abs("..")
	cfg := &Config{Game: gameDir, Mods: filepath.Join(dir, "mods"), Enabled: []string{"Delta"}, path: filepath.Join(dir, "h3mod.json")}
	d := newDeployer(cfg)
	d.log = t.Logf
	if err := d.Deploy(t.Context(), false, filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	res, err := ReadArchive(filepath.Join(dir, "out", "chunk0patch300.rpkg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Hash != line {
		t.Fatalf("unexpected archive contents: %d resources", len(res))
	}
	got, err := res[0].Data()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) || len(res[0].Refs) != len(base.Refs) {
		t.Fatalf("delta not applied: %q", got)
	}
}
