package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

const (
	repoHash = 0x00204D1AFD76AB13
	// Patch numbers from managedPatchMin up are mod output (ours or SMF's),
	// never base game data.
	managedPatchMin = 200
	outputPatch     = 300
)

var archiveName = regexp.MustCompile(`^chunk(\d+)(?:patch(\d+))?\.rpkg$`)

// baseArchives lists vanilla archives of a chunk, newest patch first.
func baseArchives(runtime string, chunk int) ([]string, error) {
	ents, err := os.ReadDir(runtime)
	if err != nil {
		return nil, err
	}
	type arch struct {
		path  string
		patch int
	}
	var out []arch
	for _, e := range ents {
		m := archiveName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		if c, _ := strconv.Atoi(m[1]); c != chunk {
			continue
		}
		p, _ := strconv.Atoi(m[2])
		if p >= managedPatchMin {
			continue
		}
		out = append(out, arch{filepath.Join(runtime, e.Name()), p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].patch > out[j].patch })
	paths := make([]string, len(out))
	for i, a := range out {
		paths[i] = a.path
	}
	return paths, nil
}

// baseResource finds the newest vanilla version of a resource in a chunk.
func baseResource(runtime string, chunk int, hash uint64) (*Resource, error) {
	paths, err := baseArchives(runtime, chunk)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		res, err := ReadArchive(p)
		if err != nil {
			return nil, err
		}
		for _, r := range res {
			if r.Hash == hash {
				return r, nil
			}
		}
	}
	return nil, fmt.Errorf("%016X not found in chunk%d", hash, chunk)
}

// patchRepository applies repository.json patches (item ID -> partial item)
// to the base REPO, in order.
func patchRepository(base *Resource, patches []*node) (*Resource, error) {
	data, err := base.Data()
	if err != nil {
		return nil, err
	}
	repo, err := parseJSON(data)
	if err != nil {
		return nil, fmt.Errorf("parse REPO: %w", err)
	}
	if !repo.isArr {
		return nil, fmt.Errorf("REPO is not an array")
	}
	byID := map[string]*node{}
	for _, it := range repo.arr {
		if it.obj != nil {
			byID[it.str("ID_")] = it
		}
	}

	refs := append([]Ref(nil), base.Refs...)
	known := map[uint64]bool{}
	for _, r := range refs {
		known[r.Hash] = true
	}
	for _, p := range patches {
		if p.obj == nil {
			return nil, fmt.Errorf("repository patch is not an object")
		}
		for _, id := range p.keys {
			item, ok := byID[id]
			if !ok {
				item = &node{obj: map[string]*node{}}
				idRaw, _ := marshalScalar(id)
				item.set("ID_", &node{raw: idRaw})
				byID[id] = item
				repo.arr = append(repo.arr, item)
			}
			mergeInto(item, p.obj[id])
			// Like SMF: an item's runtime entity must be a REPO dependency.
			if rt, err := strconv.ParseUint(item.str("Runtime"), 10, 64); err == nil && rt != 0 && !known[rt] {
				refs = append(refs, Ref{Hash: rt, Flag: 0x9F})
				known[rt] = true
			}
		}
	}

	var buf bytes.Buffer
	repo.encode(&buf)
	return &Resource{
		Hash:     base.Hash,
		Type:     base.Type,
		Refs:     refs,
		DataSize: uint32(buf.Len()),
		SysMem:   base.SysMem,
		VidMem:   base.VidMem,
		data:     buf.Bytes(),
	}, nil
}
