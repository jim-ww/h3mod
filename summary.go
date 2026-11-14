package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Peacock reads SMF's deploy summary to serve modded unlockables and
// contracts and to run mods' plugins. h3mod writes the same file, marked as
// its own, so it never touches one SMF wrote.

// serverState is SMF's lastServerSideStates.
type serverState struct {
	unlockables *node // the whole patched unlockables list
	contracts   *node // Metadata.Id -> contract
	plugins     []string
}

func (s *serverState) addContract(id string, doc *node) {
	if s.contracts == nil {
		s.contracts = &node{obj: map[string]*node{}}
	}
	s.contracts.set(id, doc)
}

func (s *serverState) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	field := func(name string, v *node) {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, "%q:", name)
		v.encode(&buf)
	}
	if s.unlockables != nil {
		field("unlockables", s.unlockables)
	}
	if s.contracts != nil {
		field("contracts", s.contracts)
	}
	if len(s.plugins) > 0 {
		arr := &node{isArr: true}
		for _, p := range s.plugins {
			// Peacock maps Z:\ paths back on Linux; under Wine they work as is.
			raw, _ := marshalScalar(`Z:` + strings.ReplaceAll(p, "/", `\`))
			arr.arr = append(arr.arr, &node{raw: raw})
		}
		field("peacockPlugins", arr)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

type deploySummary struct {
	H3mod                bool                `json:"h3mod"`
	RuntimePath          string              `json:"runtimePath"`
	RetailPath           string              `json:"retailPath"`
	SkipIntro            bool                `json:"skipIntro"`
	LoadOrder            []string            `json:"loadOrder"`
	ModOptions           map[string][]string `json:"modOptions"`
	Platform             string              `json:"platform"`
	LastServerSideStates *serverState        `json:"lastServerSideStates"`
}

// summaryPath is where Peacock running on Linux looks first.
func summaryPath(cfg *Config) string {
	switch cfg.DeploySummary {
	case "off":
		return ""
	case "":
		dir := os.Getenv("XDG_DATA_HOME")
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			dir = filepath.Join(home, ".local", "share")
		}
		return filepath.Join(dir, "app.simple-mod-framework", "deploySummary.json")
	}
	return cfg.abs(cfg.DeploySummary)
}

// ownSummary reports whether path is free or holds a summary h3mod wrote.
func ownSummary(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var s struct {
		H3mod bool `json:"h3mod"`
	}
	return json.Unmarshal(b, &s) == nil && s.H3mod, nil
}

// writeDeploySummary writes the summary to path, unless path is empty.
func writeDeploySummary(cfg *Config, p *Plan, path string) error {
	if path == "" {
		return nil
	}
	if own, err := ownSummary(path); err != nil || !own {
		if err == nil {
			err = fmt.Errorf("%s was not written by h3mod; set \"deploySummary\" in the config", path)
		}
		return err
	}
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return err
	}
	s := deploySummary{
		H3mod:                true,
		RuntimePath:          cfg.runtimeDir(),
		RetailPath:           filepath.Join(cfg.abs(cfg.Game), "Retail"),
		SkipIntro:            cfg.skipIntro(),
		LoadOrder:            []string{},
		ModOptions:           map[string][]string{},
		Platform:             cfg.platform(),
		LastServerSideStates: &p.Server,
	}
	for _, dir := range cfg.Enabled {
		m := findMod(mods, dir)
		if m == nil {
			continue
		}
		s.LoadOrder = append(s.LoadOrder, m.ID())
		if sel, _ := cfg.selection(m); sel != nil {
			s.ModOptions[m.ID()] = sel
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, b)
}

func removeDeploySummary(path string) error {
	if path == "" {
		return nil
	}
	if own, err := ownSummary(path); err != nil || !own {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
