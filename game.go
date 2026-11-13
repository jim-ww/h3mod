package main

import (
	"bytes"
	"fmt"
	"regexp"
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
