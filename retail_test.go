package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncRetail(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "Retail", "mods")
	src := filepath.Join(dir, "src")
	put := func(path, s string) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(s), 0o644)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	logf := func(f string, a ...any) { t.Logf(f, a...) }
	put(filepath.Join(src, "a/x.txt"), "x1")
	put(filepath.Join(src, "y.txt"), "y1")
	put(filepath.Join(root, "Other.dll"), "someone else's")
	put(filepath.Join(root, "y.txt"), "user's own y")

	placed, err := syncRetail(root, map[string]string{"a/x.txt": filepath.Join(src, "a/x.txt"), "y.txt": filepath.Join(src, "y.txt")}, nil, logf)
	if err != nil {
		t.Fatal(err)
	}
	if read("a/x.txt") != "x1" || read("y.txt") != "user's own y" || len(placed) != 1 {
		t.Fatalf("first sync: x=%q y=%q placed=%v", read("a/x.txt"), read("y.txt"), placed)
	}

	// An updated source replaces our file.
	put(filepath.Join(src, "a/x.txt"), "x2")
	if placed, err = syncRetail(root, map[string]string{"a/x.txt": filepath.Join(src, "a/x.txt")}, placed, logf); err != nil || read("a/x.txt") != "x2" {
		t.Fatalf("update: %q, %v", read("a/x.txt"), err)
	}

	// Removing it deletes our file and its empty folder, nothing else.
	if placed, err = syncRetail(root, nil, placed, logf); err != nil || len(placed) != 0 {
		t.Fatalf("remove: %v, %v", placed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a")); err == nil {
		t.Error("empty folder left")
	}
	if read("Other.dll") != "someone else's" || read("y.txt") != "user's own y" {
		t.Error("touched files h3mod didn't write")
	}

	// A file of ours edited by someone else is left alone.
	placed, _ = syncRetail(root, map[string]string{"a/x.txt": filepath.Join(src, "a/x.txt")}, nil, logf)
	put(filepath.Join(root, "a/x.txt"), "edited")
	if _, err := syncRetail(root, nil, placed, logf); err != nil || read("a/x.txt") != "edited" {
		t.Fatalf("edited file: %q, %v", read("a/x.txt"), err)
	}
}

func TestJPContentDir(t *testing.T) {
	if got := jpContentDir("Extra: Voice (Lines) (ModJPFilesContent)"); got != "content_extra__voice_lines" {
		t.Errorf("got %q", got)
	}
}
