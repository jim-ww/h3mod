package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// smfConfig is the part of SMF's config.json h3mod imports.
type smfConfig struct {
	LoadOrder  []string            `json:"loadOrder"`
	ModOptions map[string][]string `json:"modOptions"`
	SkipIntro  *bool               `json:"skipIntro"`
}

// smfDir is SMF's folder: the config's "smf", or SMF in the game folder.
func (c *Config) smfDir() string {
	if c.SMF != "" {
		return c.abs(c.SMF)
	}
	return filepath.Join(c.abs(c.Game), "SMF")
}

// importSMF replaces load order, options and the intro setting with those
// of an SMF config. When h3mod has no deploy recorded but the game has SMF's
// mod archives, SMF's setup is recorded as what's deployed.
func importSMF(cfg *Config, path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sc smfConfig
	if err := json.Unmarshal(relaxedJSON(b), &sc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return nil, err
	}
	var notes []string
	cfg.Enabled = nil
	cfg.Options = nil
	for _, id := range sc.LoadOrder {
		m := findMod(mods, id)
		if m == nil {
			notes = append(notes, fmt.Sprintf("%s: not in %s, skipped", id, cfg.abs(cfg.Mods)))
			continue
		}
		if !slices.Contains(cfg.Enabled, m.Dir) {
			cfg.Enabled = append(cfg.Enabled, m.Dir)
		}
		opts, ok := sc.ModOptions[m.ID()]
		if !ok || m.Manifest == nil || len(m.Manifest.Options) == 0 {
			continue
		}
		keys := m.Manifest.optionKeys()
		var sel []string
		for _, o := range opts {
			if slices.Contains(keys, o) {
				sel = append(sel, o)
			} else {
				notes = append(notes, fmt.Sprintf("%s: option %q no longer exists", m.Dir, o))
			}
		}
		cfg.saveSelection(m, sel)
	}
	if sc.SkipIntro != nil {
		cfg.SkipIntro = sc.SkipIntro
	}
	// SMF decided about every mod, so none is new to enable
	cfg.Known = nil
	for _, m := range mods {
		cfg.Known = append(cfg.Known, m.Dir)
	}
	st, err := loadState(cfg.abs("state.json"))
	if err != nil {
		return nil, err
	}
	if st.Deployed.IsZero() && hasModArchives(cfg.runtimeDir()) {
		st.recordDeploy(cfg, mods, cfg.skipIntro())
		if err := st.save(cfg.abs("state.json")); err != nil {
			return nil, err
		}
		notes = append(notes, fmt.Sprintf("the game has SMF's deploy: recorded its %d mods as deployed", len(cfg.Enabled)))
	}
	return notes, nil
}

// hasModArchives reports whether a Runtime folder has mod archives (patch
// numbers SMF and h3mod write).
func hasModArchives(runtime string) bool {
	ents, err := os.ReadDir(runtime)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if m := archiveName.FindStringSubmatch(e.Name()); m != nil && m[2] != "" {
			if n, _ := strconv.Atoi(m[2]); n >= managedPatchMin && n <= outputPatch {
				return true
			}
		}
	}
	return false
}

// findMod matches a folder name or mod ID, case-insensitively.
func findMod(mods []*Mod, name string) *Mod {
	for _, m := range mods {
		if strings.EqualFold(m.Dir, name) || strings.EqualFold(m.ID(), name) {
			return m
		}
	}
	return nil
}

// relaxedJSON converts the JavaScript object literal SMF writes (unquoted
// keys, single-quoted strings) into JSON.
func relaxedJSON(b []byte) []byte {
	var out strings.Builder
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '"' || c == '\'':
			quote := c
			out.WriteByte('"')
			for i++; i < len(b) && b[i] != quote; i++ {
				switch {
				case b[i] == '\\' && i+1 < len(b):
					i++
					if b[i] == '\'' {
						out.WriteByte('\'')
					} else {
						out.WriteByte('\\')
						out.WriteByte(b[i])
					}
				case b[i] == '"':
					out.WriteString(`\"`)
				default:
					out.WriteByte(b[i])
				}
			}
			out.WriteByte('"')
		case isIdentStart(c):
			j := i
			for j < len(b) && (isIdentStart(b[j]) || (b[j] >= '0' && b[j] <= '9')) {
				j++
			}
			word := string(b[i:j])
			k := j
			for k < len(b) && (b[k] == ' ' || b[k] == '\t' || b[k] == '\r' || b[k] == '\n') {
				k++
			}
			if k < len(b) && b[k] == ':' {
				out.WriteString(`"` + word + `"`)
			} else {
				out.WriteString(word) // true, false, null
			}
			i = j - 1
		default:
			out.WriteByte(c)
		}
	}
	return stripJSONC([]byte(out.String()))
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// smfMods returns SMF's mods folder when it's worth offering on a first run:
// the config is new and the game folder has an SMF install with mods.
func (c *Config) smfMods() string {
	dir := filepath.Join(c.smfDir(), "Mods")
	if !c.fresh || c.abs(c.Mods) == dir {
		return ""
	}
	if mods, err := discoverMods(dir); err != nil || len(mods) == 0 {
		return ""
	}
	return dir
}

// useSMFMods switches to SMF's mods folder and takes SMF's load order and
// options when it has a config.
func (c *Config) useSMFMods(dir string) ([]string, error) {
	c.Mods = dir
	var notes []string
	smfConfig := filepath.Join(filepath.Dir(dir), "config.json")
	if _, err := os.Stat(smfConfig); err == nil {
		var err error
		if notes, err = importSMF(c, smfConfig); err != nil {
			return nil, err
		}
		notes = append(notes, fmt.Sprintf("took load order and options from %s", smfConfig))
	}
	return notes, c.save()
}
