package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"time"
)

// State records what the last deploy wrote, in state.json next to the
// config. It lets a deploy skip archives whose inputs didn't change.
type State struct {
	Deployed time.Time         `json:"deployed,omitzero"`
	Mods     []string          `json:"mods,omitempty"`
	Archives map[string]Output `json:"archives,omitempty"` // by file name in Runtime
}

type Output struct {
	Inputs string `json:"inputs"` // fingerprint of everything in the archive
	Size   int64  `json:"size"`
}

func loadState(path string) (*State, error) {
	s := &State{}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

func (s *State) save(path string) error {
	b, err := json.MarshalIndent(s, "", "\t")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(b, '\n'))
}

// fingerprint identifies an archive's contents without reading file-backed
// data: those are identified by file, offset, length, size and mtime.
func fingerprint(res []*Resource) (string, error) {
	h := sha256.New()
	io.WriteString(h, "h3mod archive v2\n") // bump when the archive format changes
	stats := map[string]string{}
	sorted := slices.Clone(res)
	slices.SortFunc(sorted, func(a, b *Resource) int {
		switch {
		case a.Hash < b.Hash:
			return -1
		case a.Hash > b.Hash:
			return 1
		}
		return 0
	})
	for _, r := range sorted {
		fmt.Fprintf(h, "%016X %s %d %d %d %d %v\n", r.Hash, r.Type, r.DataSize, r.SysMem, r.VidMem, r.SizeFlag, r.Refs)
		if r.data != nil {
			sum := sha256.Sum256(r.data)
			h.Write(sum[:])
			continue
		}
		id, ok := stats[r.path]
		if !ok {
			st, err := os.Stat(r.path)
			if err != nil {
				return "", err
			}
			id = fmt.Sprintf("%s %d %d", r.path, st.Size(), st.ModTime().UnixNano())
			stats[r.path] = id
		}
		fmt.Fprintf(h, "%s %d %d\n", id, r.offset, r.length)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
