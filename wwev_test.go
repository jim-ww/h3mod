package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Game WWEVs must survive parsing and encoding unchanged.
func TestWWEVRoundTrip(t *testing.T) {
	idx, err := loadIndex("../Runtime")
	if err != nil {
		t.Skip("no game files: ", err)
	}
	n, embedded := 0, 0
	for _, r := range idx.res {
		if r.Type != "WWEV" || n == 2000 {
			continue
		}
		n++
		b, err := r.Data()
		if err != nil {
			t.Fatal(err)
		}
		w, err := parseWWEV(b)
		if err != nil {
			t.Fatalf("%016X: %v", r.Hash, err)
		}
		if !bytes.Equal(w.encode(), b) {
			t.Fatalf("%016X does not round-trip", r.Hash)
		}
		if len(w.wems) > 0 {
			embedded++
		}
	}
	t.Logf("%d WWEVs, %d with embedded wems", n, embedded)
}

func TestWWEVReplaceWem(t *testing.T) {
	idx, err := loadIndex("../Runtime")
	if err != nil {
		t.Skip("no game files: ", err)
	}
	var base *wwev
	var hash uint64
	for _, r := range idx.res {
		if r.Type != "WWEV" {
			continue
		}
		b, _ := r.Data()
		if w, err := parseWWEV(b); err == nil && len(w.wems) >= 2 {
			base, hash = w, r.Hash
			break
		}
	}
	if base == nil {
		t.Skip("no WWEV with two wems")
	}
	path := filepath.Join(t.TempDir(), hashString(hash)+"~1.sfx.wem")
	os.WriteFile(path, []byte("RIFF new wem"), 0o644)
	h, i, err := parseWemName(path)
	if err != nil || h != hash || i != 1 {
		t.Fatalf("parseWemName = %x, %d, %v", h, i, err)
	}
	j := &wwevJob{wems: map[int]string{1: path}, out: &Resource{Hash: hash, Type: "WWEV"}}
	if err := j.build(idx); err != nil {
		t.Fatal(err)
	}
	got, err := parseWWEV(j.out.data)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.wems[1].data) != "RIFF new wem" || got.wems[1].id != base.wems[1].id ||
		!bytes.Equal(got.wems[0].data, base.wems[0].data) || !bytes.Equal(got.tail, base.tail) {
		t.Fatal("wem 1 not replaced in place")
	}
}
