package main

import (
	"bytes"
	"testing"
)

func TestMergeKeepsOrder(t *testing.T) {
	dst, _ := parseJSON([]byte(`{"a":1,"b":{"x":1,"y":[1,2]},"c":3}`))
	src, _ := parseJSON([]byte(`{"b":{"y":[9],"z":2},"d":4}`))
	mergeInto(dst, src)
	var buf bytes.Buffer
	dst.encode(&buf)
	if want := `{"a":1,"b":{"x":1,"y":[9],"z":2},"c":3,"d":4}`; buf.String() != want {
		t.Fatalf("got %s, want %s", buf.String(), want)
	}
}

func TestPatchGameRepository(t *testing.T) {
	idx, err := loadIndex("../Runtime")
	if err != nil {
		t.Skip("no game files: ", err)
	}
	base, err := idx.lookup(repoHash)
	if err != nil {
		t.Fatal(err)
	}
	orig, err := base.Data()
	if err != nil {
		t.Fatal(err)
	}
	// An empty patch must reproduce the original repository exactly.
	same, err := patchRepository(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(same.data, orig) {
		t.Fatalf("unpatched REPO differs from original (%d vs %d bytes)", len(same.data), len(orig))
	}
	p, _ := parseJSON([]byte(`{"2b1bd2af-554e-4ea7-a717-3f6d0eb0215f":{"Evergreen_Rarity":1.5}}`))
	out, err := patchRepository(base, []*node{p})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.data, []byte(`"Evergreen_Rarity":1.5`)) {
		t.Fatal("patch not applied")
	}
}
