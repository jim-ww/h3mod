package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Mod is a folder in the mods directory: an SMF framework mod (manifest.json)
// or a plain rpkg mod (folders of .rpkg files).
type Mod struct {
	Dir      string // folder name; identifies the mod in the config
	Path     string
	Manifest *Manifest
}

// ID is what SMF calls the mod: the manifest id, or the folder for rpkg mods.
func (m *Mod) ID() string {
	if m.Manifest != nil && m.Manifest.ID != "" {
		return m.Manifest.ID
	}
	return m.Dir
}

func (m *Mod) Title() string {
	if m.Manifest != nil && m.Manifest.Name != "" {
		return m.Manifest.Name
	}
	return m.Dir
}

func (m *Mod) Version() string {
	if m.Manifest == nil {
		return ""
	}
	return m.Manifest.Version
}

// Manifest is the part of an SMF manifest h3mod uses. Fields that the root
// and options share live in ManifestContent.
type Manifest struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Options []Option `json:"options"`
	// Incompatibilities are mod IDs that must not be enabled alongside.
	Incompatibilities []string `json:"incompatibilities"`
	ManifestContent
}

type ManifestContent struct {
	ContentFolders    []string     `json:"contentFolders"`
	Dependencies      []Dependency `json:"dependencies"`
	Scripts           []string     `json:"scripts"`
	Packagedefinition []PDChange   `json:"packagedefinition"`
	Thumbs            []string     `json:"thumbs"`
	Requirements      []string     `json:"requirements"` // mod IDs that must be enabled

	// Localisation is {language: {string ID: text}} for the global LOCR;
	// LocalisationOverrides is {LOCR: {language: {decimal hash: text}}};
	// LocalisedLines is {LINE hash: string ID}.
	Localisation          *node             `json:"localisation"`
	LocalisationOverrides *node             `json:"localisationOverrides"`
	LocalisedLines        map[string]string `json:"localisedLines"`

	unsupported []string
}

type Option struct {
	Name             string `json:"name"`
	Group            string `json:"group"`
	Type             string `json:"type"`
	Tooltip          string `json:"tooltip"`
	EnabledByDefault bool   `json:"enabledByDefault"`
	Condition        string `json:"condition"`
	ManifestContent
}

// Key identifies an option the way SMF stores it: "Group:Name" for select
// options, "Name" otherwise.
func (o *Option) Key() string {
	if o.Type == "select" && o.Group != "" {
		return o.Group + ":" + o.Name
	}
	return o.Name
}

// Dependency is a game resource a mod needs copied into a chunk, together with
// the parts of its dependency tree that aren't in the base chunks.
type Dependency struct {
	Hash           string
	ToChunk        int
	PortFromChunk1 bool
}

// UnmarshalJSON accepts "HASH", "HASH,HASH,..." and
// {"runtimeID": "HASH", "toChunk": n, "portFromChunk1": bool}.
func (d *Dependency) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		d.Hash = s
		return nil
	}
	var o struct {
		RuntimeID      string `json:"runtimeID"`
		ToChunk        int    `json:"toChunk"`
		PortFromChunk1 bool   `json:"portFromChunk1"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	*d = Dependency{Hash: o.RuntimeID, ToChunk: o.ToChunk, PortFromChunk1: o.PortFromChunk1}
	return nil
}

// expand splits comma-separated hash lists, which SMF also accepts.
func (d Dependency) expand() []Dependency {
	var out []Dependency
	for h := range strings.SplitSeq(d.Hash, ",") {
		if h = strings.TrimSpace(h); h != "" {
			e := d
			e.Hash = h
			out = append(out, e)
		}
	}
	return out
}

// SMF manifest features h3mod doesn't implement yet.
var unsupportedKeys = []string{
	"blobsFolders", "peacockPlugins",
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
		b, err := os.ReadFile(mp)
		switch {
		case err == nil:
			if m.Manifest, err = parseManifest(b); err != nil {
				return nil, fmt.Errorf("%s: %w", mp, err)
			}
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		case !hasRPKG(m.Path):
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
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	for i := range m.Options {
		m.Options[i].unsupported = usedKeys(raw.Options[i], unsupportedKeys)
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
		switch string(bytes.TrimSpace(m[k])) {
		case "", "null", "[]", "{}":
		default:
			out = append(out, k)
		}
	}
	return out
}

// stripJSONC turns the relaxed JSON SMF accepts in manifests (comments,
// trailing commas, raw control characters in strings) into strict JSON.
func stripJSONC(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			switch {
			case c == '\\' && i+1 < len(b):
				out = append(out, c, b[i+1])
				i++
			case c == '"':
				inStr = false
				out = append(out, c)
			case c == '\n':
				out = append(out, `\n`...)
			case c == '\r':
				out = append(out, `\r`...)
			case c == '\t':
				out = append(out, `\t`...)
			case c < 0x20:
				out = append(out, fmt.Sprintf(`\u%04x`, c)...)
			default:
				out = append(out, c)
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

// defaultSelection is what SMF enables for a mod nobody configured.
func (m *Manifest) defaultSelection() []string {
	var sel []string
	for _, o := range m.Options {
		if o.Type != "conditional" && o.EnabledByDefault {
			sel = append(sel, o.Key())
		}
	}
	return sel
}

func (m *Manifest) option(key string) *Option {
	for i := range m.Options {
		if m.Options[i].Key() == key {
			return &m.Options[i]
		}
	}
	return nil
}

func (m *Manifest) optionKeys() []string {
	keys := make([]string, 0, len(m.Options))
	for _, o := range m.Options {
		if o.Type != "conditional" {
			keys = append(keys, o.Key())
		}
	}
	return keys
}

// content returns the manifest content active for a selection: the root plus
// every selected option, in manifest order like SMF.
func (m *Manifest) content(selected []string) []ManifestContent {
	out := []ManifestContent{m.ManifestContent}
	for _, o := range m.Options {
		if o.Type == "conditional" {
			out = append(out, ManifestContent{unsupported: []string{"conditional option " + o.Name}})
			continue
		}
		if slices.Contains(selected, o.Key()) {
			out = append(out, o.ManifestContent)
		}
	}
	return out
}
