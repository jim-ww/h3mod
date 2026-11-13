package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// rtlvJob is an HMLanguageTools rtlv.json (localised videos with their
// subtitles), generated through ResourceTool like HMLanguages does.
type rtlvJob struct {
	path string
	out  *Resource // placeholder in the plan, filled in by build
}

func newRTLVJob(path string) (*rtlvJob, error) {
	j, err := readJSONFile(path)
	if err != nil {
		return nil, err
	}
	h, err := parseHash(j.strOf("hash"))
	if err != nil {
		return nil, fmt.Errorf("hash: %w", err)
	}
	return &rtlvJob{path: path, out: &Resource{Hash: h, Type: "RTLV"}}, nil
}

// rtlvSource converts rtlv.json into ResourceLib's RTLV JSON and the video
// dependencies.
func rtlvSource(path string) ([]byte, []Ref, error) {
	j, err := readJSONFile(path)
	if err != nil {
		return nil, nil, err
	}
	langs := h3Languages
	if lm := j.strOf("langmap"); lm != "" {
		langs = strings.Split(lm, ",")
	}
	videos, subs := j.get("videos"), j.get("subtitles")
	if videos == nil || len(videos.keys) == 0 {
		return nil, nil, errors.New("videos object is empty")
	}
	type rid struct {
		High uint32 `json:"m_IDHigh"`
		Low  uint32 `json:"m_IDLow"`
	}
	rl := struct {
		AudioLanguages             []string `json:"AudioLanguages"`
		VideoRidsPerAudioLanguage  []rid    `json:"VideoRidsPerAudioLanguage"`
		SubtitleLanguages          []string `json:"SubtitleLanguages"`
		SubtitleMarkupsPerLanguage []string `json:"SubtitleMarkupsPerLanguage"`
	}{AudioLanguages: []string{}, VideoRidsPerAudioLanguage: []rid{}, SubtitleLanguages: []string{}, SubtitleMarkupsPerLanguage: []string{}}
	var refs []Ref
	for _, lang := range videos.keys {
		i := slices.Index(langs, lang)
		if i < 0 {
			return nil, nil, fmt.Errorf("language %q is not in the language map", lang)
		}
		v, _ := videos.obj[lang].text()
		h, err := parseHash(v)
		if err != nil {
			return nil, nil, fmt.Errorf("video %q: %w", v, err)
		}
		rl.AudioLanguages = append(rl.AudioLanguages, lang)
		rl.VideoRidsPerAudioLanguage = append(rl.VideoRidsPerAudioLanguage, rid{uint32(h >> 32), uint32(h)})
		refs = append(refs, Ref{h, byte(0x80 + i)})
	}
	if subs != nil {
		for _, lang := range subs.keys {
			s, _ := subs.obj[lang].text()
			rl.SubtitleLanguages = append(rl.SubtitleLanguages, lang)
			rl.SubtitleMarkupsPerLanguage = append(rl.SubtitleMarkupsPerLanguage, s)
		}
	}
	// ResourceLib doesn't decode \u escapes, so keep <, > and & literal.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err = enc.Encode(rl)
	return buf.Bytes(), refs, err
}

func (j *rtlvJob) build(ctx context.Context, tools Tools, cacheDir, workDir string) error {
	key, err := fileKey("h3mod rtlv v1\n"+tools.ResourceTool, j.path)
	if err != nil {
		return err
	}
	dir := filepath.Join(cacheDir, key)
	if res, err := loadResources(dir, 1); err == nil {
		*j.out = *res[0]
		return nil
	}
	src, refs, err := rtlvSource(j.path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(workDir, "rtlv-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := os.WriteFile(filepath.Join(work, "in.json"), src, 0o644); err != nil {
		return err
	}
	if err := tools.run(ctx, work, tools.ResourceTool, "HM3", "generate", "RTLV", "in.json", "out.RTLV"); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(work, "out.RTLV"))
	if err != nil {
		return err
	}
	r := rawResource(j.out.Hash, "RTLV", refs, data)
	if err := storeResources(dir, []*Resource{r}); err != nil {
		return err
	}
	*j.out = *r
	return nil
}
