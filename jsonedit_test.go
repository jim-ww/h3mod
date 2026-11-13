package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployJSONEdits(t *testing.T) {
	if _, err := os.Stat("../Runtime/chunk0.rpkg"); err != nil {
		t.Skip("no game files")
	}
	dir := t.TempDir()
	mod := filepath.Join(dir, "mods", "Test")
	write := func(rel, s string) {
		t.Helper()
		p := filepath.Join(mod, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", `{"id":"Test.Mod","name":"Test","version":"1","contentFolders":["content"],"blobsFolders":["blobs"],"peacockPlugins":["plugin.js"]}`)
	write("blobs/images/Test.png", "not really a png")
	write("content/chunk0/a.unlockables.json", `{"TEST_UNLOCKABLE":{"Type":"gear","Subtype":"test"}}`)
	write("content/chunk0/b.contract.json", `{"Metadata":{"Id":"11111111-2222-3333-4444-555555555555","Title":"Test"}}`)
	write("content/chunk0/c.JSON.patch.json", `{"file":"00204D1AFD76AB13","type":"REPO","patch":[
		{"op":"replace","path":"/2b1bd2af-554e-4ea7-a717-3f6d0eb0215f/Evergreen_Rarity","value":7}]}`)

	gameDir, _ := filepath.Abs("..")
	cfg := &Config{Game: gameDir, Mods: filepath.Join(dir, "mods"), Enabled: []string{"Test"}, path: filepath.Join(dir, "h3mod.json")}
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
	got := map[uint64]*Resource{}
	for _, r := range res {
		got[r.Hash] = r
	}
	data := func(h uint64) []byte {
		t.Helper()
		r := got[h]
		if r == nil {
			t.Fatalf("%016X not deployed", h)
		}
		b, err := r.Data()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	u, err := parseUnlockablesORES(data(unlockablesORES))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(u, []byte(`{"Id":"TEST_UNLOCKABLE","Type":"gear","Subtype":"test"}`)) {
		t.Error("unlockable not added")
	}
	entries, err := parseNameTable(data(contractsORES))
	if err != nil {
		t.Fatal(err)
	}
	h := contractHash("11111111-2222-3333-4444-555555555555")
	if last := entries[len(entries)-1]; last.hash != h {
		t.Errorf("contract not registered: %+v", last)
	}
	if !bytes.Contains(data(h), []byte(`"Title":"Test"`)) {
		t.Error("contract JSON missing")
	}
	if !bytes.Contains(data(repoHash), []byte(`"Evergreen_Rarity":7`)) {
		t.Error("repository JSON patch not applied")
	}
	bh := ioiHash("[assembly:/_pro/online/default/cloudstorage/resources/images/test.png].pc_gfx")
	if got[bh] == nil || got[bh].Type != "GFXI" || string(data(bh)) != "not really a png" {
		t.Error("blob not deployed")
	}
	blobs, err := parseNameTable(data(blobsORES))
	if err != nil {
		t.Fatal(err)
	}
	if last := blobs[len(blobs)-1]; last.hash != bh || last.id != "images/test.png" {
		t.Errorf("blob not registered: %+v", last)
	}

	// what Peacock reads
	sb, err := os.ReadFile(filepath.Join(out, "deploySummary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sum struct {
		LoadOrder            []string
		LastServerSideStates struct {
			Unlockables    []map[string]any
			Contracts      map[string]struct{ Metadata struct{ Title string } }
			PeacockPlugins []string
		}
	}
	if err := json.Unmarshal(sb, &sum); err != nil {
		t.Fatal(err)
	}
	st := sum.LastServerSideStates
	if len(sum.LoadOrder) != 1 || sum.LoadOrder[0] != "Test.Mod" {
		t.Errorf("load order %v", sum.LoadOrder)
	}
	if len(st.Unlockables) < 100 || st.Unlockables[len(st.Unlockables)-1]["Id"] != "TEST_UNLOCKABLE" {
		t.Errorf("summary unlockables: %d, last %v", len(st.Unlockables), st.Unlockables[len(st.Unlockables)-1])
	}
	if st.Contracts["11111111-2222-3333-4444-555555555555"].Metadata.Title != "Test" {
		t.Errorf("summary contracts %v", st.Contracts)
	}
	if want := "Z:" + strings.ReplaceAll(filepath.Join(mod, "plugin.js"), "/", `\`); len(st.PeacockPlugins) != 1 || st.PeacockPlugins[0] != want {
		t.Errorf("summary plugins %v, want %s", st.PeacockPlugins, want)
	}
}
