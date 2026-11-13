package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// wwevJob replaces embedded wems of a game WWEV (sound event) with
// HASH~INDEX.sfx.wem files. Like SMF, all mods' wems for one WWEV are applied
// to the game's version together.
type wwevJob struct {
	chunk int
	wems  map[int]string // index -> wem file, later mods win
	out   *Resource
}

// parseWemName splits HASH~INDEX.sfx.wem.
func parseWemName(path string) (uint64, int, error) {
	name := strings.SplitN(filepath.Base(path), ".", 2)[0]
	hs, is, ok := strings.Cut(name, "~")
	h, err := strconv.ParseUint(hs, 16, 64)
	i, err2 := strconv.Atoi(is)
	if !ok || err != nil || err2 != nil || i < 0 {
		return 0, 0, fmt.Errorf("wem name %q: want HASH~INDEX.sfx.wem", filepath.Base(path))
	}
	return h, i, nil
}

// wwev is the layout of a WWEV: a name, an ID, embedded wems and a tail of
// streamed wem references kept as is.
type wwev struct {
	head []byte // name length, name, ID
	wems []wwevWem
	tail []byte
}

type wwevWem struct {
	id   uint32
	data []byte
}

func parseWWEV(b []byte) (*wwev, error) {
	errShort := errors.New("WWEV is truncated")
	if len(b) < 4 {
		return nil, errShort
	}
	pos := 4 + int(binary.LittleEndian.Uint32(b)) + 4
	if pos+4 > len(b) {
		return nil, errShort
	}
	w := &wwev{head: b[:pos]}
	n := binary.LittleEndian.Uint32(b[pos:])
	pos += 4
	for range n {
		if pos+8 > len(b) {
			return nil, errShort
		}
		id, size := binary.LittleEndian.Uint32(b[pos:]), int(binary.LittleEndian.Uint32(b[pos+4:]))
		pos += 8
		if pos+size > len(b) {
			return nil, errShort
		}
		w.wems = append(w.wems, wwevWem{id, b[pos : pos+size]})
		pos += size
	}
	w.tail = b[pos:]
	return w, nil
}

func (w *wwev) encode() []byte {
	out := append([]byte(nil), w.head...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(w.wems)))
	for _, e := range w.wems {
		out = binary.LittleEndian.AppendUint32(out, e.id)
		out = binary.LittleEndian.AppendUint32(out, uint32(len(e.data)))
		out = append(out, e.data...)
	}
	return append(out, w.tail...)
}

func (j *wwevJob) build(idx *Index) error {
	base, err := idx.lookup(j.out.Hash)
	if err != nil {
		return err
	}
	data, err := base.Data()
	if err != nil {
		return err
	}
	w, err := parseWWEV(data)
	if err != nil {
		return err
	}
	for i, path := range j.wems {
		if i >= len(w.wems) {
			return fmt.Errorf("%s: WWEV has %d wems", path, len(w.wems))
		}
		if w.wems[i].data, err = os.ReadFile(path); err != nil {
			return err
		}
	}
	*j.out = *rawResource(j.out.Hash, "WWEV", base.Refs, w.encode())
	return nil
}
