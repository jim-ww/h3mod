package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var le = binary.LittleEndian

// xorKey scrambles resource data when bit 31 of the index size field is set.
var xorKey = [8]byte{0xDC, 0x45, 0xA6, 0x9C, 0xD3, 0x72, 0x4C, 0xAB}

const (
	sizeScrambled = 0x80000000
	sizeMask      = 0x3FFFFFFF
)

// Ref is a dependency of a resource, as listed in its header.
type Ref struct {
	Hash uint64
	Flag byte
}

// Resource is one file of an rpkg: its header and where its stored bytes live.
// Stored bytes are kept as they are in the archive (possibly compressed and
// scrambled), so copying a resource never re-encodes it.
type Resource struct {
	Hash     uint64
	Type     string
	Refs     []Ref
	DataSize uint32 // decoded size
	SysMem   uint32
	VidMem   uint32
	SizeFlag uint32 // index size field: compressed size (0 = raw) | sizeScrambled

	// Exactly one source: an archive/file region, or bytes in memory.
	path   string
	offset int64
	length int64
	data   []byte
}

func (r *Resource) storedLen() int64 {
	if r.data != nil {
		return int64(len(r.data))
	}
	return r.length
}

// Stored returns the resource bytes as they would be written to an archive.
func (r *Resource) Stored() ([]byte, error) {
	if r.data != nil {
		return r.data, nil
	}
	f, err := os.Open(r.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, r.length)
	if _, err := f.ReadAt(b, r.offset); err != nil {
		return nil, fmt.Errorf("read %016X from %s: %w", r.Hash, r.path, err)
	}
	return b, nil
}

// Data returns the decoded resource bytes.
func (r *Resource) Data() ([]byte, error) {
	b, err := r.Stored()
	if err != nil {
		return nil, err
	}
	if r.SizeFlag&sizeScrambled != 0 {
		b = append([]byte(nil), b...)
		for i := range b {
			b[i] ^= xorKey[i%8]
		}
	}
	if r.SizeFlag&sizeMask != 0 {
		return lz4Decode(b, int(r.DataSize))
	}
	return b, nil
}

// ReadArchive reads the index and resource headers of an rpkg (v1 or v2).
func ReadArchive(path string) ([]*Resource, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	head := make([]byte, 29)
	if _, err := io.ReadFull(f, head); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var p int
	switch string(head[:4]) {
	case "GKPR":
		p = 4
	case "2KPR":
		p = 13
	default:
		return nil, fmt.Errorf("%s: not an rpkg", path)
	}
	count := le.Uint32(head[p:])
	indexSize := le.Uint32(head[p+4:])
	tableSize := le.Uint32(head[p+8:])
	p += 12

	isPatch := strings.Contains(strings.ToLower(filepath.Base(path)), "patch")
	if isPatch {
		var n [4]byte
		if _, err := f.ReadAt(n[:], int64(p)); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		p += 4 + int(le.Uint32(n[:]))*8
	}

	b := make([]byte, int(indexSize)+int(tableSize))
	if _, err := f.ReadAt(b, int64(p)); err != nil {
		return nil, fmt.Errorf("%s: tables: %w", path, err)
	}
	idx, tab := b[:indexSize], b[indexSize:]
	out := make([]*Resource, 0, count)
	q := 0
	for i := 0; i < int(count); i++ {
		e := idx[i*20:]
		r := &Resource{
			Hash:     le.Uint64(e),
			path:     path,
			offset:   int64(le.Uint64(e[8:])),
			SizeFlag: le.Uint32(e[16:]),
		}
		h := tab[q:]
		r.Type = string([]byte{h[3], h[2], h[1], h[0]})
		refSize := le.Uint32(h[4:])
		r.DataSize = le.Uint32(h[12:])
		r.SysMem = le.Uint32(h[16:])
		r.VidMem = le.Uint32(h[20:])
		r.Refs = parseRefs(h[24 : 24+refSize])
		q += 24 + int(refSize)

		r.length = int64(r.SizeFlag & sizeMask)
		if r.length == 0 {
			r.length = int64(r.DataSize)
		}
		out = append(out, r)
	}
	return out, nil
}

func parseRefs(b []byte) []Ref {
	if len(b) < 4 {
		return nil
	}
	n := int(le.Uint32(b) & sizeMask)
	refs := make([]Ref, n)
	for i := range refs {
		refs[i] = Ref{Hash: le.Uint64(b[4+n+i*8:]), Flag: b[4+i]}
	}
	return refs
}

func encodeRefs(refs []Ref) []byte {
	if len(refs) == 0 {
		return nil
	}
	n := len(refs)
	b := make([]byte, 4+n+n*8)
	le.PutUint32(b, uint32(n))
	for i, r := range refs {
		b[4+i] = r.Flag
		le.PutUint64(b[4+n+i*8:], r.Hash)
	}
	return b
}

// WritePatchArchive writes resources as a v1 patch rpkg with an empty
// deletion list, the same format the game loads from mod rpkgs.
func WritePatchArchive(path string, res []*Resource) error {
	sort.Slice(res, func(i, j int) bool { return res[i].Hash < res[j].Hash })

	var table []byte
	for _, r := range res {
		refs := encodeRefs(r.Refs)
		var h [24]byte
		copy(h[:4], []byte{r.Type[3], r.Type[2], r.Type[1], r.Type[0]})
		le.PutUint32(h[4:], uint32(len(refs)))
		le.PutUint32(h[12:], r.DataSize)
		le.PutUint32(h[16:], r.SysMem)
		le.PutUint32(h[20:], r.VidMem)
		table = append(table, h[:]...)
		table = append(table, refs...)
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	head := make([]byte, 20)
	copy(head, "GKPR")
	le.PutUint32(head[4:], uint32(len(res)))
	le.PutUint32(head[8:], uint32(len(res)*20))
	le.PutUint32(head[12:], uint32(len(table)))
	w.Write(head)

	offset := uint64(len(head) + len(res)*20 + len(table))
	idx := make([]byte, 20)
	for _, r := range res {
		le.PutUint64(idx, r.Hash)
		le.PutUint64(idx[8:], offset)
		le.PutUint32(idx[16:], r.SizeFlag)
		w.Write(idx)
		offset += uint64(r.storedLen())
	}
	w.Write(table)
	for _, r := range res {
		b, err := r.Stored()
		if err != nil {
			f.Close()
			return err
		}
		if _, err := w.Write(b); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// readMeta reads an rpkg-tool .meta file paired with a raw resource file.
func readMeta(metaPath, dataPath string) (*Resource, error) {
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	if len(b) < 44 {
		return nil, errors.New("meta too short")
	}
	st, err := os.Stat(dataPath)
	if err != nil {
		return nil, err
	}
	refSize := le.Uint32(b[24:])
	if 44+int(refSize) > len(b) {
		return nil, errors.New("meta reference table out of range")
	}
	return &Resource{
		Hash:     le.Uint64(b),
		Type:     string(b[20:24]),
		Refs:     parseRefs(b[44 : 44+refSize]),
		DataSize: uint32(st.Size()),
		SysMem:   le.Uint32(b[36:]),
		VidMem:   le.Uint32(b[40:]),
		path:     dataPath,
		length:   st.Size(),
	}, nil
}

func lz4Decode(src []byte, n int) ([]byte, error) {
	dst := make([]byte, 0, n)
	i := 0
	for i < len(src) {
		tok := src[i]
		i++
		l := int(tok >> 4)
		if l == 15 {
			for i < len(src) {
				x := src[i]
				i++
				l += int(x)
				if x != 255 {
					break
				}
			}
		}
		if i+l > len(src) {
			return nil, errors.New("lz4: literal out of range")
		}
		dst = append(dst, src[i:i+l]...)
		i += l
		if i >= len(src) {
			break
		}
		if i+2 > len(src) {
			return nil, errors.New("lz4: truncated offset")
		}
		off := int(src[i]) | int(src[i+1])<<8
		i += 2
		ml := int(tok & 15)
		if ml == 15 {
			for i < len(src) {
				x := src[i]
				i++
				ml += int(x)
				if x != 255 {
					break
				}
			}
		}
		ml += 4
		s := len(dst) - off
		if off == 0 || s < 0 {
			return nil, errors.New("lz4: bad match offset")
		}
		for k := 0; k < ml; k++ {
			dst = append(dst, dst[s+k])
		}
	}
	if len(dst) != n {
		return nil, fmt.Errorf("lz4: got %d bytes, want %d", len(dst), n)
	}
	return dst, nil
}
