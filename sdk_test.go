package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSDK(t *testing.T) {
	dir := t.TempDir()
	game := filepath.Join(dir, "game")
	retail := filepath.Join(game, "Retail")
	os.MkdirAll(retail, 0o755)
	put := func(path, s string) { os.MkdirAll(filepath.Dir(path), 0o755); os.WriteFile(path, []byte(s), 0o644) }
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(retail, rel))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	put(filepath.Join(retail, "crashpad_handler.exe"), "the game's")
	put(filepath.Join(retail, "mods.ini"), "[sdk]\ncrash_reporting = false\n")
	release := filepath.Join(dir, "release.zip")
	writeZip(t, release, map[string]string{
		"dinput8.dll": "loader", "ZHMModSDK.dll": "sdk", "crashpad_handler.exe": "the sdk's",
		"mods/FreeCam.dll": "freecam", "mods/NoPause.dll": "nopause", "licenses/x.txt": "license",
	}, nil)
	cfg := &Config{Game: game, path: filepath.Join(dir, "h3mod.json")}
	logf := func(f string, a ...any) { t.Logf(f, a...) }

	st, _ := readSDKStatus(cfg)
	if st.Installed {
		t.Fatal("installed before install")
	}
	if err := installSDKFrom(t.Context(), cfg, "v1", release); err != nil {
		t.Fatal(err)
	}
	if read("dinput8.dll") != "loader" || read("mods/FreeCam.dll") != "freecam" || read("crashpad_handler.exe") != "the sdk's" {
		t.Fatal("not installed")
	}
	st, _ = readSDKStatus(cfg)
	if !st.Installed || !st.Enabled || !st.Ours || st.Version != "v1" || len(st.Mods) != 2 || st.Mods[0].Enabled {
		t.Fatalf("status after install: %+v", st)
	}

	// mods.ini: sections are enabled mods; [sdk] stays
	if err := setSDKMod(cfg, "freecam", true); err != nil {
		t.Fatal(err)
	}
	if ini := read("mods.ini"); !strings.Contains(ini, "[sdk]\ncrash_reporting = false") || !strings.Contains(ini, "[freecam]") {
		t.Fatalf("mods.ini: %q", ini)
	}
	if st, _ = readSDKStatus(cfg); !st.Mods[0].Enabled || st.Mods[1].Enabled {
		t.Fatalf("mods: %+v", st.Mods)
	}
	setSDKMod(cfg, "FreeCam", false)
	if strings.Contains(read("mods.ini"), "freecam") {
		t.Fatal("mod not turned off")
	}
	if err := setSDKMod(cfg, "nope", true); err == nil {
		t.Error("unknown mod accepted")
	}

	// the loader switch
	setSDKLoader(cfg, false)
	if st, _ = readSDKStatus(cfg); !st.Installed || st.Enabled || read("dinput8.dll") != "<missing>" {
		t.Fatalf("after disable: %+v", st)
	}
	// updating keeps it off
	if err := installSDKFrom(t.Context(), cfg, "v2", release); err != nil {
		t.Fatal(err)
	}
	if st, _ = readSDKStatus(cfg); st.Enabled || st.Version != "v2" {
		t.Fatalf("update turned the loader on: %+v", st)
	}
	setSDKLoader(cfg, true)

	// uninstall: our files go, the game's file comes back, changed files stay
	put(filepath.Join(retail, "mods", "NoPause.dll"), "edited by someone")
	if err := uninstallSDK(cfg, logf); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{
		"dinput8.dll": "<missing>", "ZHMModSDK.dll": "<missing>", "mods/FreeCam.dll": "<missing>",
		"licenses/x.txt": "<missing>", "crashpad_handler.exe": "the game's", "mods/NoPause.dll": "edited by someone",
	} {
		if got := read(rel); got != want {
			t.Errorf("%s: %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(retail, "licenses")); err == nil {
		t.Error("empty licenses folder left")
	}
	if err := uninstallSDK(cfg, logf); err == nil {
		t.Error("uninstalled an SDK h3mod didn't install")
	}
}

func TestModsINI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.ini")
	os.WriteFile(path, []byte("[sdk]\r\nx = 1\r\n\r\n[firstperson]\r\n"), 0o644)
	ini, err := readModsINI(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := ini.sectionNames(); len(n) != 1 || n[0] != "firstperson" {
		t.Fatalf("sections %v", n)
	}
	ini.set("freecam", true)
	ini.set("firstperson", false)
	if got := string(ini.bytes()); got != "[sdk]\nx = 1\n\n[freecam]\n" {
		t.Fatalf("got %q", got)
	}
}
