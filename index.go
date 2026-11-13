package main

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
)

// Index locates vanilla resources across every game archive.
type Index struct {
	res    map[uint64]*Resource // newest version of each resource
	chunks map[uint64]uint64    // bit n set: the resource exists in chunk n
}

type archiveFile struct {
	path         string
	chunk, patch int
}

// vanillaArchives lists game archives (not mod output), oldest first.
func vanillaArchives(runtime string) ([]archiveFile, error) {
	ents, err := os.ReadDir(runtime)
	if err != nil {
		return nil, err
	}
	var out []archiveFile
	for _, e := range ents {
		m := archiveName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		c, _ := strconv.Atoi(m[1])
		p, _ := strconv.Atoi(m[2])
		if p >= managedPatchMin {
			continue
		}
		out = append(out, archiveFile{filepath.Join(runtime, e.Name()), c, p})
	}
	slices.SortFunc(out, func(a, b archiveFile) int {
		return cmp.Or(cmp.Compare(a.chunk, b.chunk), cmp.Compare(a.patch, b.patch))
	})
	return out, nil
}

func loadIndex(runtime string) (*Index, error) {
	archives, err := vanillaArchives(runtime)
	if err != nil {
		return nil, err
	}
	results := make([][]*Resource, len(archives))
	errs := make([]error, len(archives))
	var wg sync.WaitGroup
	for i, a := range archives {
		wg.Go(func() {
			results[i], errs[i] = ReadArchive(a.path)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	idx := &Index{res: map[uint64]*Resource{}, chunks: map[uint64]uint64{}}
	for i, a := range archives {
		for _, r := range results[i] {
			// A resource's home is its lowest chunk; within it, later patches win.
			if old, ok := idx.res[r.Hash]; !ok || idx.chunkOf(old) == a.chunk {
				idx.res[r.Hash] = r
			}
			if a.chunk < 64 {
				idx.chunks[r.Hash] |= 1 << a.chunk
			}
		}
	}
	return idx, nil
}

func (idx *Index) chunkOf(r *Resource) int {
	m := archiveName.FindStringSubmatch(filepath.Base(r.path))
	c, _ := strconv.Atoi(m[1])
	return c
}

func (idx *Index) lookup(hash uint64) (*Resource, error) {
	r := idx.res[hash]
	if r == nil {
		return nil, fmt.Errorf("%016X not found in game files", hash)
	}
	return r, nil
}

func (idx *Index) inChunk(hash uint64, chunk int) bool {
	return idx.chunks[hash]&(1<<chunk) != 0
}
