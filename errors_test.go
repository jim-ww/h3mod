package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Deploy errors name the mod and the file in it.
func TestDeployErrorsNameMod(t *testing.T) {
	if _, err := os.Stat("../Runtime/chunk0.rpkg"); err != nil {
		t.Skip("no game files")
	}
	gameDir, _ := filepath.Abs("..")
	for _, tt := range []struct {
		name, file, body, want string
	}{
		// read while planning
		{"plan", "content/chunk0/bad.entity.patch.json", `{not json`, `mod "Broken": content/chunk0/bad.entity.patch.json`},
		// applied while deploying
		{"deploy", "content/chunk0/bad.JSON.patch.json", `{"file":"00204D1AFD76AB13","type":"REPO","patch":[{"op":"replace","path":"/no-such-item/x","value":1}]}`,
			`mod "Broken" (content/chunk0/bad.JSON.patch.json)`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mod := filepath.Join(dir, "mods", "Broken")
			os.MkdirAll(filepath.Join(mod, filepath.Dir(tt.file)), 0o755)
			os.WriteFile(filepath.Join(mod, "manifest.json"), []byte(`{"id":"t.broken","name":"Broken","description":"","authors":[],"version":"1.0.0","frameworkVersion":"2.33.0","contentFolders":["content"]}`), 0o644)
			os.WriteFile(filepath.Join(mod, tt.file), []byte(tt.body), 0o644)
			cfg := &Config{Game: gameDir, Mods: filepath.Join(dir, "mods"), Enabled: []string{"Broken"}, path: filepath.Join(dir, "h3mod.json")}
			d := newDeployer(cfg)
			d.log = t.Logf
			err := d.Deploy(t.Context(), false, filepath.Join(dir, "out"))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %v, want it to contain %s", err, tt.want)
			}
			t.Log(err)
		})
	}
}
