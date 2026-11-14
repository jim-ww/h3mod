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

	// A clean copy that skips the intro is replaced by SMF's.
	skipped := encryptPD([]byte("SCENE_FILE=assembly:/_PRO/Scenes/Frontend/MainMenu.entity\n[Hitman5]\n"))
	os.WriteFile(cfg.thumbsBase(), skipped, 0o644)
	os.WriteFile(thumbs, skipped, 0o644)
	os.MkdirAll(filepath.Join(game, "SMF"), 0o755)
	os.WriteFile(filepath.Join(game, "SMF", "cleanThumbs.dat"), orig, 0o644)
	if err := restoreThumbs(cfg); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(thumbs); !bytes.Equal(b, orig) {
		t.Fatal("restored a thumbs.dat that skips the intro")
	}
	if b, _ := os.ReadFile(cfg.thumbsBase()); !bytes.Equal(b, orig) {
		t.Fatal("bad clean copy kept")
	}
}

func TestSkipIntroDefault(t *testing.T) {
	dir := t.TempDir()
	for body, want := range map[string]bool{`{}`: true, `{"skipIntro": true}`: true, `{"skipIntro": false}`: false} {
		path := filepath.Join(dir, "h3mod.json")
		os.WriteFile(path, []byte(body), 0o644)
		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.skipIntro() != want {
			t.Errorf("%s: skipIntro %v, want %v", body, cfg.skipIntro(), want)
		}
	}
}

func TestPlatform(t *testing.T) {
	game := t.TempDir()
	os.Mkdir(filepath.Join(game, "Retail"), 0o755)
	cfg := &Config{Game: game, path: filepath.Join(t.TempDir(), "h3mod.json")}
	if p := cfg.platform(); p != "steam" {
		t.Errorf("default %s", p)
	}
	os.WriteFile(filepath.Join(game, "Retail", "EOSSDK-Win64-Shipping.dll"), nil, 0o644)
	if p := cfg.platform(); p != "epic" {
		t.Errorf("with EOS SDK %s", p)
	}
}
