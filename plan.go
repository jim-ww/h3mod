package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

var (
	rawFileName = regexp.MustCompile(`^([0-9A-Fa-f]{16})\.([0-9A-Za-z]{4})$`)
	chunkDir    = regexp.MustCompile(`(?i)^chunk(\d+)$`)
	chunkInName = regexp.MustCompile(`(?i)^chunk(\d+)`)
)

// Plan is everything a deploy writes, collected from mods in load order.
type Plan struct {
	Chunks    map[int]map[uint64]*Resource
	Repo      []*node
	Entities  []*entityJob
	Textures  []*textureJob             // built at deploy time into resources already placed
	Loc       []locEdit                 // strings to set in LOCRs
	JSONEdits []*jsonEdit               // unlockables, contracts and JSON patches, in order
	PD        []PDChange                // packagedefinition changes
	Thumbs    []string                  // lines for thumbs.dat's [Hitman5] section
	Notes     []string                  // option reconciliation and similar
	Skipped   map[string]map[string]int // mod -> unsupported feature -> count

	entity   map[uint64]*entityJob // by TEMP hash
	fallback []placed              // added only where nothing else is
	game     *gameData
}

type placed struct {
	chunk int
	res   *Resource
}

func newPlan(game *gameData) *Plan {
	return &Plan{
		Chunks:  map[int]map[uint64]*Resource{},
		Skipped: map[string]map[string]int{},
		entity:  map[uint64]*entityJob{},
		game:    game,
	}
}

// gameData loads game-wide lookups on first use.
type gameData struct {
	index    func() (*Index, error)
	hashList func() (*HashList, error)
	pathList string // hash_list.txt with resource paths
}

func newGameData(cfg *Config) *gameData {
	pathList := os.Getenv("H3MOD_PATH_LIST") // set by the nix package
	if pathList == "" {
		pathList = filepath.Join(cfg.abs(cfg.Game), "SMF", "Third-Party", "hash_list.txt")
	}
	return &gameData{
		pathList: pathList,
		index:    sync.OnceValues(func() (*Index, error) { return loadIndex(cfg.runtimeDir()) }),
		hashList: sync.OnceValues(func() (*HashList, error) {
			// The nix package points H3MOD_HASH_LIST at a pinned hash list;
			// SMF ships the same file.
			path := os.Getenv("H3MOD_HASH_LIST")
			if path == "" {
				path = filepath.Join(cfg.abs(cfg.Game), "SMF", "Third-Party", "hash_list.hmla")
			}
			return loadHashList(path)
		}),
	}
}

// buildPlan collects the enabled mods in load order. Missing mods are an
// error when strict, and skipped otherwise.
func buildPlan(cfg *Config, game *gameData, strict bool) (*Plan, error) {
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return nil, err
	}
	p := newPlan(game)
	enabled := map[string]bool{}
	for _, dir := range cfg.Enabled {
		if m := findMod(mods, dir); m != nil {
			enabled[strings.ToLower(m.ID())] = true
		}
	}
	for _, dir := range cfg.Enabled {
		i := slices.IndexFunc(mods, func(m *Mod) bool { return m.Dir == dir })
		if i < 0 {
			if strict {
				return nil, fmt.Errorf("enabled mod %q not found in %s", dir, cfg.abs(cfg.Mods))
			}
			continue
		}
		m := mods[i]
		sel, notes := cfg.selection(m)
		for _, n := range notes {
			p.Notes = append(p.Notes, m.Dir+": "+n)
		}
		p.checkRelations(m, sel, enabled)
		if err := p.addMod(m, sel); err != nil {
			return nil, fmt.Errorf("%s: %w", m.Dir, err)
		}
	}
	for _, f := range p.fallback {
		if _, ok := p.Chunks[f.chunk][f.res.Hash]; !ok {
			p.put(f.chunk, f.res)
		}
	}
	return p, nil
}

// put places a resource in a chunk's output.
// checkRelations notes required mods that aren't enabled and incompatible
// ones that are.
func (p *Plan) checkRelations(m *Mod, selected []string, enabled map[string]bool) {
	if m.Manifest == nil {
		return
	}
	for _, c := range m.Manifest.content(selected) {
		for _, r := range c.Requirements {
			if !enabled[strings.ToLower(r)] {
				p.Notes = append(p.Notes, fmt.Sprintf("%s: requires %s, which is not enabled", m.Dir, r))
			}
		}
	}
	for _, r := range m.Manifest.Incompatibilities {
		if enabled[strings.ToLower(r)] {
			p.Notes = append(p.Notes, fmt.Sprintf("%s: incompatible with enabled %s", m.Dir, r))
		}
	}
}

func (p *Plan) put(chunk int, r *Resource) {
	if p.Chunks[chunk] == nil {
		p.Chunks[chunk] = map[uint64]*Resource{}
	}
	p.Chunks[chunk][r.Hash] = r
}

// add places a resource a mod ships. A raw entity file replaces whatever
// patching happened to that entity before.
func (p *Plan) add(chunk int, r *Resource) {
	p.put(chunk, r)
	if r.Type == "TEMP" || r.Type == "TBLU" {
		for t, j := range p.entity {
			if j.temp == r.Hash || j.tblu == r.Hash {
				delete(p.entity, t)
				p.Entities = slices.DeleteFunc(p.Entities, func(e *entityJob) bool { return e == j })
			}
		}
	}
}

// staged finds a resource an earlier mod added, in any chunk.
func (p *Plan) staged(hash uint64) (int, *Resource) {
	for c, res := range p.Chunks {
		if r, ok := res[hash]; ok {
			return c, r
		}
	}
	return 0, nil
}

func (p *Plan) skip(mod, what string) {
	if p.Skipped[mod] == nil {
		p.Skipped[mod] = map[string]int{}
	}
	p.Skipped[mod][what]++
}

func (p *Plan) addMod(m *Mod, selected []string) error {
	if m.Manifest == nil {
		return p.addRPKGMod(m.Path)
	}
	for _, c := range m.Manifest.content(selected) {
		for _, u := range c.unsupported {
			p.skip(m.Dir, u)
		}
		for _, f := range c.ContentFolders {
			if err := p.addContentFolder(m, filepath.Join(m.Path, filepath.FromSlash(f))); err != nil {
				return err
			}
		}
		for _, d := range c.Dependencies {
			for _, e := range d.expand() {
				if err := p.addDependency(e); err != nil {
					return err
				}
			}
		}
		for _, s := range c.Scripts {
			if err := p.addScript(m, filepath.Join(m.Path, filepath.FromSlash(s)), selected); err != nil {
				return err
			}
		}
		if c.Localisation != nil || c.LocalisationOverrides != nil {
			hl, err := p.game.hashList()
			if err != nil {
				return err
			}
			edits, err := manifestLocalisation(c, hl)
			if err != nil {
				return err
			}
			p.Loc = append(p.Loc, edits...)
		}
		for h, id := range c.LocalisedLines {
			hash, err := parseHash(h)
			if err != nil {
				return fmt.Errorf("localisedLines: %w", err)
			}
			p.add(0, lineResource(hash, id))
		}
		p.PD = append(p.PD, c.Packagedefinition...)
		p.Thumbs = append(p.Thumbs, c.Thumbs...)
	}
	return nil
}

func (p *Plan) addRPKGMod(dir string) error {
	res, err := rpkgResources(dir)
	if err != nil {
		return err
	}
	for _, r := range res {
		p.add(r.chunk, r.res)
	}
	return nil
}

// rpkgResources reads every .rpkg under dir, in name order.
func rpkgResources(dir string) ([]placed, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".rpkg") {
			paths = append(paths, path)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	var out []placed
	for _, path := range paths {
		m := chunkInName.FindStringSubmatch(filepath.Base(path))
		if m == nil {
			return nil, fmt.Errorf("%s: no chunk in file name", path)
		}
		chunk, _ := strconv.Atoi(m[1])
		res, err := ReadArchive(path)
		if err != nil {
			return nil, err
		}
		for _, r := range res {
			out = append(out, placed{chunk, r})
		}
	}
	return out, nil
}

func (p *Plan) addContentFolder(m *Mod, dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == dir {
				return nil // SMF tolerates missing content folders
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		cm := chunkDir.FindStringSubmatch(strings.Split(filepath.ToSlash(rel), "/")[0])
		if cm == nil {
			p.skip(m.Dir, "file outside chunk folders")
			return nil
		}
		chunk, _ := strconv.Atoi(cm[1])
		if err := p.addContentFile(m, chunk, path); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		return nil
	})
}

func (p *Plan) addContentFile(m *Mod, chunk int, path string) error {
	name := filepath.Base(path)
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".meta"), strings.HasSuffix(lower, ".meta.json"):
		return nil // read with its data file
	case strings.HasSuffix(lower, ".repository.json"):
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n, err := parseJSON(stripJSONC(b))
		if err != nil {
			return err
		}
		p.Repo = append(p.Repo, n)
	case strings.HasSuffix(lower, ".entity.patch.json"):
		h, temp, tblu, err := readEntityHeader(path)
		if err != nil {
			return err
		}
		if h.PatchVersion.String() != "6" {
			p.skip(m.Dir, "entity.patch.json patchVersion "+h.PatchVersion.String())
			return nil
		}
		p.addEntityPatch(chunk, temp, tblu, path)
	case strings.HasSuffix(lower, ".entity.json"):
		h, temp, tblu, err := readEntityHeader(path)
		if err != nil {
			return err
		}
		if h.QuickEntityVersion.String() != "3.1" {
			p.skip(m.Dir, "entity.json quickEntityVersion "+h.QuickEntityVersion.String())
			return nil
		}
		j := &entityJob{chunk: chunk, temp: temp, tblu: tblu, qn: path}
		p.removeEntity(temp)
		p.entity[temp] = j
		p.Entities = append(p.Entities, j)
	case strings.HasSuffix(lower, ".dlge.json"):
		hl, err := p.game.hashList()
		if err != nil {
			return err
		}
		r, err := buildDLGE(path, hl)
		if err != nil {
			return err
		}
		p.add(chunk, r)
	case strings.HasSuffix(lower, ".locr.json"), strings.HasSuffix(lower, ".ditl.json"):
		hl, err := p.game.hashList()
		if err != nil {
			return err
		}
		build := buildLOCRJSON
		if strings.HasSuffix(lower, ".ditl.json") {
			build = buildDITL
		}
		r, err := build(path, hl)
		if err != nil {
			return err
		}
		p.add(chunk, r)
	case strings.HasSuffix(lower, ".clng.json"):
		r, err := buildCLNG(path)
		if err != nil {
			return err
		}
		p.add(chunk, r)
	case strings.HasSuffix(lower, ".unlockables.json"), strings.HasSuffix(lower, ".contract.json"),
		strings.HasSuffix(lower, ".json.patch.json"):
		kind := "unlockables"
		switch {
		case strings.HasSuffix(lower, ".contract.json"):
			kind = "contract"
		case strings.HasSuffix(lower, ".json.patch.json"):
			kind = "patch"
		}
		e, err := readJSONEdit(path, kind)
		if err != nil {
			return err
		}
		p.JSONEdits = append(p.JSONEdits, e)
	case strings.HasSuffix(lower, ".material.json"):
		res, err := buildMaterial(path)
		if err != nil {
			return err
		}
		for _, r := range res {
			p.add(chunk, r)
		}
	case strings.HasSuffix(lower, ".texture.tga"):
		j, err := newTextureJob(path)
		if err != nil {
			return err
		}
		for _, r := range j.out {
			p.add(chunk, r)
		}
		p.Textures = append(p.Textures, j)
	case rawFileName.MatchString(name):
		r, err := readRawFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			p.skip(m.Dir, "raw file without .meta")
			return nil
		}
		if err != nil {
			return err
		}
		p.add(chunk, r)
	default:
		// SMF ignores files that are neither resources nor special files.
		if _, typ, ok := strings.Cut(lower, "."); ok && slices.Contains(specialFiles, typ) {
			p.skip(m.Dir, typ)
		}
	}
	return nil
}

// specialFiles are SMF content types h3mod doesn't convert yet.
var specialFiles = []string{
	"sfx.wem", "delta", "rtlv.json",
}

func (p *Plan) removeEntity(temp uint64) {
	if j := p.entity[temp]; j != nil {
		delete(p.entity, temp)
		p.Entities = slices.DeleteFunc(p.Entities, func(e *entityJob) bool { return e == j })
	}
}

func (p *Plan) addEntityPatch(chunk int, temp, tblu uint64, path string) {
	j := p.entity[temp]
	if j == nil {
		j = &entityJob{chunk: chunk, temp: temp, tblu: tblu}
		// Patch raw files an earlier mod staged, in their chunk.
		if c, r := p.staged(temp); r != nil {
			j.baseTemp, j.chunk = r, c
		}
		if _, r := p.staged(tblu); r != nil {
			j.baseTblu = r
		}
		p.entity[temp] = j
		p.Entities = append(p.Entities, j)
	}
	j.patches = append(j.patches, path)
}

// readRawFile reads a resource file with its rpkg-tool .meta (binary) or
// .meta.json. The file name is authoritative; metas are sometimes stale.
func readRawFile(path string) (*Resource, error) {
	m := rawFileName.FindStringSubmatch(filepath.Base(path))
	hash, _ := strconv.ParseUint(m[1], 16, 64)
	typ := strings.ToUpper(m[2])
	// SMF accepts HASH.TYPE.meta and HASH.meta, binary or JSON.
	byHash := filepath.Join(filepath.Dir(path), m[1])
	r, err := readMeta(path+".meta", path)
	if errors.Is(err, fs.ErrNotExist) {
		r, err = readMeta(byHash+".meta", path)
	}
	if errors.Is(err, fs.ErrNotExist) {
		r, err = readMetaJSON(path+".meta.json", path)
	}
	if errors.Is(err, fs.ErrNotExist) {
		r, err = readMetaJSON(byHash+".meta.json", path)
	}
	if err != nil {
		return nil, err
	}
	r.Hash, r.Type = hash, typ
	return r, nil
}

func readMetaJSON(metaPath, dataPath string) (*Resource, error) {
	b, err := readFileFold(metaPath)
	if err != nil {
		return nil, err
	}
	refs, err := decodeMetaRefs(b)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(dataPath)
	if err != nil {
		return nil, err
	}
	var m metaJSON
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &Resource{
		Refs:     refs,
		DataSize: uint32(st.Size()),
		SysMem:   uint32(m.SizeInMemory),
		VidMem:   uint32(m.SizeInVideoMemory),
		path:     dataPath,
		length:   st.Size(),
	}, nil
}

// readFileFold reads path, also accepting a different case of its file name
// (mods ship .meta.json and .meta.JSON).
func readFileFold(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return b, err
	}
	ents, derr := os.ReadDir(filepath.Dir(path))
	if derr != nil {
		return nil, err
	}
	for _, e := range ents {
		if strings.EqualFold(e.Name(), filepath.Base(path)) {
			return os.ReadFile(filepath.Join(filepath.Dir(path), e.Name()))
		}
	}
	return nil, err
}

// addDependency copies a game resource and the part of its dependency tree
// outside the base chunks into a chunk, like rpkg-tool's
// extract_non_base_hash_depends_from (chunk0 and chunk1 are base) and
// extract_non_boot_hash_depends_from (only chunk0, for portFromChunk1).
// Files already staged win, as in SMF.
func (p *Plan) addDependency(d Dependency) error {
	idx, err := p.game.index()
	if err != nil {
		return err
	}
	root, err := parseHash(d.Hash)
	if err != nil {
		return fmt.Errorf("dependency: %w", err)
	}
	inBase := func(h uint64) bool {
		return idx.inChunk(h, 0) || (!d.PortFromChunk1 && idx.inChunk(h, 1))
	}
	seen := map[uint64]bool{}
	queue := []uint64{root}
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if seen[h] || inBase(h) {
			continue
		}
		seen[h] = true
		r := idx.res[h]
		if r == nil {
			continue // referenced but not shipped, as with rpkg-tool
		}
		if _, ok := p.Chunks[d.ToChunk][h]; !ok {
			p.put(d.ToChunk, r)
		}
		for _, ref := range r.Refs {
			queue = append(queue, ref.Hash)
		}
	}
	return nil
}

// jpFilesMarker identifies Piepieonline's standard ModJPFiles deploy script,
// which h3mod implements natively instead of running JavaScript.
const jpFilesMarker = "ModJPFiles deployment script"

func (p *Plan) addScript(m *Mod, path string, selected []string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), jpFilesMarker) {
		p.skip(m.Dir, "script "+filepath.Base(path))
		return nil
	}
	// The script copies runtime/*.rpkg into Runtime as patch archives below
	// SMF's output, so their files apply only where no mod placed one.
	res, err := rpkgResources(filepath.Join(filepath.Dir(path), "runtime"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	p.fallback = append(p.fallback, res...)
	for _, k := range selected {
		if strings.HasSuffix(k, "(ModJPFilesContent)") {
			p.skip(m.Dir, "ModJPFiles retail content")
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "retail")); err == nil {
		p.skip(m.Dir, "ModJPFiles retail content")
	}
	return nil
}
