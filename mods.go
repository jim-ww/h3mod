package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Mod is a folder in the mods directory: an SMF framework mod (manifest.json)
// or a plain rpkg mod (folders of .rpkg files).
type Mod struct {
	Dir      string // folder name; identifies the mod in the config
	Path     string
	Manifest *Manifest
}

type Manifest struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	ContentFolders []string `json:"contentFolders"`
	Options        []Option `json:"options"`

	unsupported []string
}

type Option struct {
	Name             string   `json:"name"`
	Group            string   `json:"group"`
	Type             string   `json:"type"`
	EnabledByDefault bool     `json:"enabledByDefault"`
	ContentFolders   []string `json:"contentFolders"`

	unsupported []string
}

// SMF manifest features h3mod doesn't implement yet.
var unsupportedKeys = []string{
	"blobsFolders", "localisation", "localisationOverrides", "localisedLines",
	"packagedefinition", "thumbs", "dependencies", "scripts", "peacockPlugins",
}

func (m *Mod) Title() string {
	if m.Manifest != nil && m.Manifest.Name != "" {
		return m.Manifest.Name
	}
	return m.Dir
}

func discoverMods(dir string) ([]*Mod, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var mods []*Mod
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		m := &Mod{Dir: e.Name(), Path: filepath.Join(dir, e.Name())}
		mp := filepath.Join(m.Path, "manifest.json")
		if b, err := os.ReadFile(mp); err == nil {
			if m.Manifest, err = parseManifest(b); err != nil {
				return nil, fmt.Errorf("%s: %w", mp, err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		} else if !hasRPKG(m.Path) {
			continue
		}
		mods = append(mods, m)
	}
	return mods, nil
}

func hasRPKG(dir string) bool {
	found := false
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".rpkg") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

func parseManifest(b []byte) (*Manifest, error) {
	b = stripJSONC(b)
	m := &Manifest{}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, err
	}
	m.unsupported = usedKeys(b, unsupportedKeys)
	var raw struct {
		Options []json.RawMessage `json:"options"`
	}
	json.Unmarshal(b, &raw)
	for i := range m.Options {
		if i < len(raw.Options) {
			m.Options[i].unsupported = usedKeys(raw.Options[i], unsupportedKeys)
		}
	}
	return m, nil
}

// usedKeys reports which of keys are present with a non-empty value.
func usedKeys(obj []byte, keys []string) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal(obj, &m) != nil {
		return nil
	}
	var out []string
	for _, k := range keys {
		v := bytes.TrimSpace(m[k])
		switch string(v) {
		case "", "null", "[]", "{}":
		default:
			out = append(out, k)
		}
	}
	return out
}

// stripJSONC removes comments and trailing commas, which SMF manifests allow.
func stripJSONC(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out = append(out, c)
			if c == '\\' && i+1 < len(b) {
				i++
				out = append(out, b[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
		case c == ',' && closesNext(b[i+1:]):
			// trailing comma
		default:
			out = append(out, c)
		}
	}
	return out
}

func closesNext(b []byte) bool {
	t := bytes.TrimLeft(b, " \t\r\n")
	return len(t) > 0 && (t[0] == '}' || t[0] == ']')
}

// activeFolders returns the content folders of a framework mod for the
// chosen options; with no choice saved, options enabled by default apply.
func (m *Mod) activeFolders(chosen []string, haveChoice bool) (folders, unsupported []string) {
	man := m.Manifest
	folders = append(folders, man.ContentFolders...)
	unsupported = append(unsupported, man.unsupported...)
	pick := map[string]bool{}
	for _, c := range chosen {
		pick[c] = true
	}
	for _, o := range man.Options {
		on := o.EnabledByDefault
		if haveChoice {
			on = pick[o.Name] || pick[o.Group+":"+o.Name]
		}
		if o.Type == "conditional" {
			unsupported = append(unsupported, "conditional option "+strconv.Quote(o.Name))
			continue
		}
		if on {
			folders = append(folders, o.ContentFolders...)
			unsupported = append(unsupported, o.unsupported...)
		}
	}
	return folders, unsupported
}

var (
	rawFileName = regexp.MustCompile(`^([0-9A-Fa-f]{16})\.([0-9A-Za-z]{4})$`)
	chunkDir    = regexp.MustCompile(`(?i)^chunk(\d+)$`)
	chunkInName = regexp.MustCompile(`(?i)^chunk(\d+)`)
)

// Plan is everything to deploy: resources per chunk, and repository patches.
type Plan struct {
	Chunks      map[int]map[uint64]*Resource
	Repo        []*node
	Unsupported map[string]map[string]int // mod -> feature -> count
}

func newPlan() *Plan {
	return &Plan{Chunks: map[int]map[uint64]*Resource{}, Unsupported: map[string]map[string]int{}}
}

func (p *Plan) add(chunk int, r *Resource) {
	if p.Chunks[chunk] == nil {
		p.Chunks[chunk] = map[uint64]*Resource{}
	}
	p.Chunks[chunk][r.Hash] = r
}

func (p *Plan) skip(mod, what string) {
	if p.Unsupported[mod] == nil {
		p.Unsupported[mod] = map[string]int{}
	}
	p.Unsupported[mod][what]++
}

func (p *Plan) addMod(m *Mod, cfg *Config) error {
	if m.Manifest == nil {
		return p.addRPKGMod(m)
	}
	chosen, have := cfg.Options[m.Dir]
	folders, unsupported := m.activeFolders(chosen, have)
	for _, u := range unsupported {
		p.skip(m.Dir, u)
	}
	for _, f := range folders {
		if err := p.addContentFolder(m, filepath.Join(m.Path, filepath.FromSlash(f))); err != nil {
			return fmt.Errorf("%s: %w", m.Dir, err)
		}
	}
	return nil
}

func (p *Plan) addRPKGMod(m *Mod) error {
	var paths []string
	err := filepath.WalkDir(m.Path, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".rpkg") {
			paths = append(paths, path)
		}
		return err
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		mm := chunkInName.FindStringSubmatch(filepath.Base(path))
		if mm == nil {
			p.skip(m.Dir, "rpkg without chunk in its name")
			continue
		}
		chunk, _ := strconv.Atoi(mm[1])
		res, err := ReadArchive(path)
		if err != nil {
			return fmt.Errorf("%s: %w", m.Dir, err)
		}
		for _, r := range res {
			p.add(chunk, r)
		}
	}
	return nil
}

func (p *Plan) addContentFolder(m *Mod, dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == dir {
				return nil // SMF tolerates missing option folders
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		cm := chunkDir.FindStringSubmatch(parts[0])
		name := d.Name()
		lower := strings.ToLower(name)
		switch {
		case cm == nil:
			p.skip(m.Dir, "file outside chunk folders")
		case strings.HasSuffix(lower, ".meta"):
			// read together with its data file
		case strings.HasSuffix(lower, ".repository.json"):
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			n, err := parseJSON(stripJSONC(b))
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			p.Repo = append(p.Repo, n)
		case rawFileName.MatchString(name):
			chunk, _ := strconv.Atoi(cm[1])
			r, err := readMeta(path+".meta", path)
			if errors.Is(err, fs.ErrNotExist) {
				p.skip(m.Dir, "raw file without binary .meta")
				return nil
			}
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			// The file name is authoritative; metas are sometimes stale.
			fm := rawFileName.FindStringSubmatch(name)
			r.Hash, _ = strconv.ParseUint(fm[1], 16, 64)
			r.Type = strings.ToUpper(fm[2])
			p.add(chunk, r)
		default:
			kind := name
			if i := strings.Index(name, "."); i >= 0 {
				kind = name[i:]
			}
			p.skip(m.Dir, kind)
		}
		return nil
	})
}
