package main

import (
	"bytes"
	"encoding/json"
	"hash/crc32"
	"testing"
)

func TestGameLOCRRoundTrip(t *testing.T) {
	idx, err := loadIndex("../Runtime")
	if err != nil {
		t.Skip("no game files: ", err)
	}
	r, err := idx.lookup(globalLOCR)
	if err != nil {
		t.Fatal(err)
	}
	data, err := r.Data()
	if err != nil {
		t.Fatal(err)
	}
	l, err := parseLOCR(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.encode(); !bytes.Equal(got, data) {
		t.Fatalf("re-encoded LOCR differs (%d vs %d bytes)", len(got), len(data))
	}
	t.Logf("%d languages, %d English strings", len(l.langs), len(l.strings["en"]))
}

func TestManifestLocalisation(t *testing.T) {
	var c ManifestContent
	err := json.Unmarshal([]byte(`{
		"localisation": {"english": {"UI_MYMOD_NAME": "My item"}, "german": {"UI_MYMOD_NAME": "Mein Ding"}},
		"localisationOverrides": {"00F5817876E691F1": {"french": {"1234": "Bonjour"}}}
	}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	edits, err := manifestLocalisation(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := crc32.ChecksumIEEE([]byte("UI_MYMOD_NAME"))
	want := []locEdit{
		{globalLOCR, "en", id, "My item"},
		{globalLOCR, "xx", id, "My item"},
		{globalLOCR, "de", id, "Mein Ding"},
		{globalLOCR, "fr", 1234, "Bonjour"},
	}
	if len(edits) != len(want) {
		t.Fatalf("got %d edits, want %d: %+v", len(edits), len(want), edits)
	}
	for i := range want {
		if edits[i] != want[i] {
			t.Errorf("edit %d: got %+v, want %+v", i, edits[i], want[i])
		}
	}

	l := &LOCR{langs: h3Languages, strings: map[string][]locString{}}
	for _, e := range edits {
		l.set(e.lang, e.key, e.text)
	}
	back, err := parseLOCR(l.encode())
	if err != nil {
		t.Fatal(err)
	}
	if s := back.strings["de"]; len(s) != 1 || s[0].text != "Mein Ding" {
		t.Fatalf("round trip lost a string: %+v", back.strings)
	}
}

func TestLineResource(t *testing.T) {
	r := lineResource(0x00AABBCCDDEEFF00, "ui_mymod_name")
	want := crc32.ChecksumIEEE([]byte("UI_MYMOD_NAME"))
	if len(r.data) != 5 || r.data[4] != 0 || uint32(r.data[0])|uint32(r.data[1])<<8|uint32(r.data[2])<<16|uint32(r.data[3])<<24 != want {
		t.Fatalf("bad LINE data %x", r.data)
	}
	if len(r.Refs) != 1 || r.Refs[0].Hash != globalLOCR {
		t.Fatalf("bad refs %+v", r.Refs)
	}
}
