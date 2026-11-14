package main

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func writeZip(t *testing.T, path string, files map[string]string, links map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, body := range files {
		fw, _ := w.Create(name)
		fw.Write([]byte(body))
	}
	for name, target := range links {
		h := &zip.FileHeader{Name: name}
		h.SetMode(os.ModeSymlink | 0o777)
		fw, _ := w.CreateHeader(h)
		fw.Write([]byte(target))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestInstallMods(t *testing.T) {
	root := t.TempDir()
	mods := filepath.Join(root, "Mods")
	os.Mkdir(mods, 0o755)
	cfg := &Config{Mods: mods, path: filepath.Join(root, "h3mod.json")}
	manifest := func(version string) string {
		return `{"id": "a.mod", "name": "A", "version": "` + version + `", "contentFolders": ["content"]}`
	}

	framework := filepath.Join(root, "a.zip")
	writeZip(t, framework, map[string]string{
		"Mod A/manifest.json":                manifest("1.0.0"),
		"Mod A/content/chunk0/x.entity.json": "{}",
	}, nil)
	dirs, err := installMods(t.Context(), cfg, framework, "", false, "", "")
	if err != nil || !slices.Equal(dirs, []string{"Mod A"}) {
		t.Fatalf("install = %v, %v", dirs, err)
	}
	if _, err := installMods(t.Context(), cfg, framework, "", false, "", ""); err == nil {
		t.Fatal("installing over an existing mod without -replace succeeded")
	}

	// An update lands in the folder the config uses, whatever the archive calls it.
	update := filepath.Join(root, "update.zip")
	writeZip(t, update, map[string]string{
		"A v2/manifest.json":                manifest("2.0.0"),
		"A v2/content/chunk0/y.entity.json": "{}",
	}, nil)
	if _, err := installMods(t.Context(), cfg, update, "", true, "Mod A", "a.mod"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mods, "Mod A", "content", "chunk0", "y.entity.json")); err != nil {
		t.Fatal("update not installed: ", err)
	}
	if _, err := os.Stat(filepath.Join(mods, "Mod A", "content", "chunk0", "x.entity.json")); err == nil {
		t.Fatal("old files kept after update")
	}

	rpkg := filepath.Join(root, "r.zip")
	writeZip(t, rpkg, map[string]string{"stuff/chunk1patch5.rpkg": "GKPR", "b.rpkg": "GKPR"}, nil)
	if dirs, err := installMods(t.Context(), cfg, rpkg, "R", false, "", ""); err != nil || !slices.Equal(dirs, []string{"R"}) {
		t.Fatalf("rpkg install = %v, %v", dirs, err)
	}
	for _, p := range []string{"chunk1/chunk1patch5.rpkg", "chunk0/b.rpkg"} {
		if _, err := os.Stat(filepath.Join(mods, "R", p)); err != nil {
			t.Fatal(err)
		}
	}

	bad := filepath.Join(root, "bad.zip")
	writeZip(t, bad, map[string]string{"M/manifest.json": manifest("1.0.0"), "M/content/chunk0/a": ""},
		map[string]string{"M/content/chunk0/evil": "/etc/passwd"})
	if _, err := installMods(t.Context(), cfg, bad, "", false, "", ""); err == nil {
		t.Fatal("archive with a symlink was installed")
	}
	got, _ := discoverMods(mods)
	if len(got) != 2 {
		t.Fatalf("mods dir has %d mods, want 2", len(got))
	}
	if ents, _ := os.ReadDir(mods); len(ents) != 2 {
		t.Fatalf("leftovers in mods dir: %v", ents)
	}
}

// 7z archives unpack too; making one needs 7-Zip, so the test runs only
// where it's installed.
func TestInstall7z(t *testing.T) {
	sz, err := exec.LookPath("7zz")
	if err != nil {
		t.Skip("7zz not installed")
	}
	root := t.TempDir()
	src := filepath.Join(root, "src")
	os.MkdirAll(filepath.Join(src, "Mod B", "content", "chunk0"), 0o755)
	os.WriteFile(filepath.Join(src, "Mod B", "manifest.json"), []byte(`{"id":"b.mod","name":"B","version":"1.0.0","contentFolders":["content"]}`), 0o644)
	os.WriteFile(filepath.Join(src, "Mod B", "content", "chunk0", "x.entity.json"), []byte("{}"), 0o644)
	archive := filepath.Join(root, "b.7z")
	cmd := exec.Command(sz, "a", archive, "Mod B")
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("7zz: %v\n%s", err, out)
	}
	mods := filepath.Join(root, "Mods")
	os.Mkdir(mods, 0o755)
	cfg := &Config{Mods: mods, path: filepath.Join(root, "h3mod.json")}
	if dirs, err := installMods(t.Context(), cfg, archive, "", false, "", ""); err != nil || !slices.Equal(dirs, []string{"Mod B"}) {
		t.Fatalf("install = %v, %v", dirs, err)
	}
	if _, err := os.Stat(filepath.Join(mods, "Mod B", "content", "chunk0", "x.entity.json")); err != nil {
		t.Fatal(err)
	}
}
