package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		mode := int64(0o644)
		if strings.HasSuffix(name, "/h3mod") {
			mode = 0o755
		}
		tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// releaseServer serves a GitHub-like latest release of h3mod 9.0.0.
func releaseServer(t *testing.T, archive []byte, badSum bool) *httptest.Server {
	t.Helper()
	name := "h3mod-9.0.0-linux-x64.tar.gz"
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))
	if badSum {
		sum = strings.Repeat("0", 64)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.0.0", "body": "new things", "assets": []map[string]string{
				{"name": name, "browser_download_url": srv.URL + "/" + name},
				{"name": "checksums.txt", "browser_download_url": srv.URL + "/checksums.txt"},
			}})
		case "/" + name:
			w.Write(archive)
		case "/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", sum, name)
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func TestSelfUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release archive is the Linux one")
	}
	archive := tarGz(t, map[string]string{
		"h3mod-linux-x64/h3mod":         "new program",
		"h3mod-linux-x64/release.txt":   "9.0.0",
		"h3mod-linux-x64/tools/new.exe": "new tool",
		"h3mod-linux-x64/data/x":        "new data",
	})
	oldVersion := version
	version = "1.0.0"
	defer func() { version = oldVersion }()

	setup := func(t *testing.T) string {
		dir := t.TempDir()
		put := func(rel, s string) {
			os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)
			os.WriteFile(filepath.Join(dir, rel), []byte(s), 0o644)
		}
		put("h3mod", "old program")
		put("release.txt", "1.0.0")
		put("tools/old.exe", "old tool")
		put("data/x", "old data")
		put("mods/MyMod/manifest.json", "{}") // the user's
		put("h3mod.json", "{}")
		return dir
	}
	read := func(dir, rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}

	t.Run("update", func(t *testing.T) {
		srv := releaseServer(t, archive, false)
		defer srv.Close()
		selfReleases = srv.URL + "/latest"
		rel, newer, err := checkSelfUpdate(t.Context())
		if err != nil || !newer || rel.Version != "9.0.0" || rel.Notes != "new things" {
			t.Fatalf("check: %+v %v %v", rel, newer, err)
		}
		dir := setup(t)
		if err := applySelfUpdateIn(t.Context(), rel, dir, t.Logf); err != nil {
			t.Fatal(err)
		}
		for rel, want := range map[string]string{
			"h3mod": "new program", "release.txt": "9.0.0", "tools/new.exe": "new tool", "tools/old.exe": "<missing>",
			"data/x": "new data", "mods/MyMod/manifest.json": "{}", "h3mod.json": "{}",
		} {
			if got := read(dir, rel); got != want {
				t.Errorf("%s: %q, want %q", rel, got, want)
			}
		}
		if st, _ := os.Stat(filepath.Join(dir, "h3mod")); st.Mode()&0o111 == 0 {
			t.Error("program not executable")
		}
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".h3mod-old-") || e.Name() == updateStage || e.Name() == updateJournal {
				t.Errorf("left behind: %s", e.Name())
			}
		}
	})

	t.Run("bad checksum changes nothing", func(t *testing.T) {
		srv := releaseServer(t, archive, true)
		defer srv.Close()
		selfReleases = srv.URL + "/latest"
		rel, _, _ := checkSelfUpdate(t.Context())
		dir := setup(t)
		if err := applySelfUpdateIn(t.Context(), rel, dir, t.Logf); err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Fatalf("err %v", err)
		}
		if read(dir, "h3mod") != "old program" || read(dir, "tools/old.exe") != "old tool" {
			t.Error("files changed")
		}
	})

	t.Run("interrupted update finishes on next start", func(t *testing.T) {
		dir := setup(t)
		// staged release and a journal, with the swap stopped after tools
		root := filepath.Join(dir, updateStage, "unpacked", "h3mod-linux-x64")
		os.MkdirAll(filepath.Join(root, "data"), 0o755)
		os.WriteFile(filepath.Join(root, "data", "x"), []byte("new data"), 0o644)
		os.WriteFile(filepath.Join(root, "h3mod"), []byte("new program"), 0o755)
		os.WriteFile(filepath.Join(root, "release.txt"), []byte("9.0.0"), 0o644)
		os.Rename(filepath.Join(dir, "tools"), filepath.Join(dir, ".h3mod-old-tools-1"))
		os.MkdirAll(filepath.Join(dir, "tools"), 0o755)
		os.WriteFile(filepath.Join(dir, "tools", "new.exe"), []byte("new tool"), 0o644)
		j, _ := json.Marshal(updateJournalData{Version: "9.0.0", Root: root, Entries: []string{"tools", "data", "release.txt", "h3mod"}})
		os.WriteFile(filepath.Join(dir, updateJournal), j, 0o644)

		finishSelfUpdateIn(dir)
		for rel, want := range map[string]string{"h3mod": "new program", "data/x": "new data", "tools/new.exe": "new tool", "release.txt": "9.0.0"} {
			if got := read(dir, rel); got != want {
				t.Errorf("%s: %q, want %q", rel, got, want)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, updateJournal)); err == nil {
			t.Error("journal left")
		}
	})

	t.Run("not a release install", func(t *testing.T) {
		dir := t.TempDir()
		if err := selfUpdatableIn(dir); err == nil {
			t.Error("updatable without release.txt")
		}
		if err := selfUpdatableIn("/nix/store/abc-h3mod/bin"); err == nil || !strings.Contains(err.Error(), "nix") {
			t.Errorf("nix: %v", err)
		}
	})
}
