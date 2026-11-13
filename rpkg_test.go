package main

import (
	"bytes"
	"os"
	"testing"
)

func TestArchiveRoundTrip(t *testing.T) {
	src := "../SMF/Mods/fat absolution/chunk0/chunk0patch38.rpkg"
	in, err := ReadArchive(src)
	if err != nil {
		t.Skip("no sample rpkg: ", err)
	}
	// written next to the package, so tests don't depend on a temp dir
	out := "roundtrip_test_chunk0patch300.rpkg"
	defer os.Remove(out)
	if err := WritePatchArchive(out, in); err != nil {
		t.Fatal(err)
	}
	got, err := ReadArchive(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(in) {
		t.Fatalf("got %d resources, want %d", len(got), len(in))
	}
	want := map[uint64]*Resource{}
	for _, r := range in {
		want[r.Hash] = r
	}
	for _, g := range got {
		w := want[g.Hash]
		if w == nil || g.Type != w.Type || g.DataSize != w.DataSize || len(g.Refs) != len(w.Refs) {
			t.Fatalf("header mismatch for %016X", g.Hash)
		}
		gd, err := g.Data()
		if err != nil {
			t.Fatal(err)
		}
		wd, err := w.Data()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gd, wd) {
			t.Fatalf("data mismatch for %016X", g.Hash)
		}
	}
}
