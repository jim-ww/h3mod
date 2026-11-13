package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyPD(t *testing.T) {
	base := "@partition name=super parent=none type=standard patchlevel=310\r\nboot\r\n"
	tests := []struct {
		name    string
		changes []PDChange
		want    string
		wantErr bool
	}{
		{"partition", []PDChange{{Type: "partition", Name: "mymod", Parent: "super", PartitionType: "standard"}},
			base + "\r\n@partition name=mymod parent=super type=standard patchlevel=310\r\n", false},
		{"entity", []PDChange{{Type: "entity", Partition: "super", Path: "[assembly:/x.brick].pc_entitytype"}},
			"@partition name=super parent=none type=standard patchlevel=310\r\n[assembly:/x.brick].pc_entitytype\r\nboot\r\n", false},
		{"entity already there", []PDChange{{Type: "entity", Partition: "super", Path: "boot"}}, base, false},
		{"missing partition", []PDChange{{Type: "entity", Partition: "nope", Path: "x"}}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyPD([]byte(base), tt.changes)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && string(got) != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestThumbsRoundTrip(t *testing.T) {
	game := t.TempDir()
	if err := os.Mkdir(filepath.Join(game, "Retail"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := encryptPD([]byte("SCENE_FILE=assembly:/_PRO/Scenes/Frontend/Boot.entity\n[Hitman5]\nusegamecontroller=1\n"))
	thumbs := filepath.Join(game, "Retail", "thumbs.dat")
	if err := os.WriteFile(thumbs, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Game: game, path: filepath.Join(t.TempDir(), "h3mod.json")}
	if err := writeThumbs(cfg, true, []string{"ConsoleCmd Foo 1"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(thumbs)
	plain, err := decryptPD(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(plain, []byte("MainMenu.entity")) || !bytes.Contains(plain, []byte("[Hitman5]\nConsoleCmd Foo 1\n")) {
		t.Fatalf("not patched: %q", plain)
	}
	if err := restoreThumbs(cfg); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(thumbs); !bytes.Equal(b, orig) {
		t.Fatal("restore did not bring back the original")
	}
}
