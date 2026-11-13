package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Rebuilding a game RTLV from an rtlv.json with its contents must decode to
// the game's contents (ResourceLib lays the file out differently from the
// game, as it does for SMF) and keep the game's references.
func TestRTLVMatchesGame(t *testing.T) {
	tools, err := findTools()
	if err != nil {
		t.Skip(err)
	}
	idx, err := loadIndex("../Runtime")
	if err != nil {
		t.Skip("no game files: ", err)
	}
	const h = 0x0088522A17BEA184
	v, err := idx.lookup(h)
	if err != nil {
		t.Skip(err)
	}
	vd, err := v.Data()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "v.RTLV"), vd, 0o644)
	if err := tools.run(t.Context(), dir, tools.ResourceTool, "HM3", "convert", "RTLV", "v.RTLV", "v.json"); err != nil {
		t.Fatal(err)
	}
	var rl struct {
		AudioLanguages             []string
		VideoRidsPerAudioLanguage  []struct{ M_IDHigh, M_IDLow uint32 }
		SubtitleLanguages          []string
		SubtitleMarkupsPerLanguage []string
	}
	b, _ := os.ReadFile(filepath.Join(dir, "v.json"))
	if err := json.Unmarshal(b, &rl); err != nil {
		t.Fatal(err)
	}
	videos, subs := map[string]string{}, map[string]string{}
	for i, l := range rl.AudioLanguages {
		videos[l] = hashString(uint64(rl.VideoRidsPerAudioLanguage[i].M_IDHigh)<<32 | uint64(rl.VideoRidsPerAudioLanguage[i].M_IDLow))
	}
	for i, l := range rl.SubtitleLanguages {
		subs[l] = rl.SubtitleMarkupsPerLanguage[i]
	}
	// map order is random, so write the object in the game's order
	var src bytes.Buffer
	src.WriteString(`{"hash":"` + hashString(h) + `","videos":{`)
	for i, l := range rl.AudioLanguages {
		if i > 0 {
			src.WriteString(",")
		}
		k, _ := json.Marshal(l)
		val, _ := json.Marshal(videos[l])
		src.Write(append(append(k, ':'), val...))
	}
	src.WriteString(`},"subtitles":{`)
	for i, l := range rl.SubtitleLanguages {
		if i > 0 {
			src.WriteString(",")
		}
		k, _ := json.Marshal(l)
		val, _ := marshalScalar(subs[l])
		src.Write(append(append(k, ':'), val...))
	}
	src.WriteString("}}")
	path := filepath.Join(dir, "x.rtlv.json")
	os.WriteFile(path, src.Bytes(), 0o644)

	j, err := newRTLVJob(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.build(t.Context(), tools, filepath.Join(dir, "cache"), filepath.Join(dir, "work")); err != nil {
		t.Fatal(err)
	}
	got, err := j.out.Data()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "got.RTLV"), got, 0o644)
	if err := tools.run(t.Context(), dir, tools.ResourceTool, "HM3", "convert", "RTLV", "got.RTLV", "got.json"); err != nil {
		t.Fatal(err)
	}
	if gj, _ := os.ReadFile(filepath.Join(dir, "got.json")); !bytes.Equal(gj, b) {
		t.Error("RTLV contents differ from the game's")
	}
	if len(j.out.Refs) != len(v.Refs) || j.out.Refs[0] != v.Refs[0] {
		t.Errorf("refs %v, game has %v", j.out.Refs, v.Refs)
	}
}
