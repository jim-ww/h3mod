package main

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// metaJSON is rpkg-tool's JSON form of a resource header, which QuickEntity
// reads and writes alongside entity files.
type metaJSON struct {
	HashValue          string    `json:"hash_value"`
	HashOffset         uint64    `json:"hash_offset"`
	HashSize           uint64    `json:"hash_size"`
	HashResourceType   string    `json:"hash_resource_type"`
	ReferenceTableSize uint64    `json:"hash_reference_table_size"`
	ReferenceDummy     uint64    `json:"hash_reference_table_dummy"`
	SizeFinal          uint64    `json:"hash_size_final"`
	SizeInMemory       uint64    `json:"hash_size_in_memory"`
	SizeInVideoMemory  uint64    `json:"hash_size_in_video_memory"`
	References         []metaRef `json:"hash_reference_data"`
}

type metaRef struct {
	Hash string `json:"hash"`
	Flag string `json:"flag"`
}

func hashString(h uint64) string { return fmt.Sprintf("%016X", h) }

// parseHash accepts a hex hash or a resource path, which is hashed the way
// the game does: md5 of the lowercase path, top byte cleared.
func parseHash(s string) (uint64, error) {
	if len(s) == 16 && !strings.ContainsAny(s, ":[]/") {
		return strconv.ParseUint(s, 16, 64)
	}
	if !strings.ContainsAny(s, ":[") {
		return 0, fmt.Errorf("invalid hash %q", s)
	}
	sum := md5.Sum([]byte(strings.ToLower(s)))
	return binary.BigEndian.Uint64(sum[:8]) & 0x00FFFFFFFFFFFFFF, nil
}

func encodeMetaJSON(r *Resource) ([]byte, error) {
	m := metaJSON{
		HashValue:          hashString(r.Hash),
		HashResourceType:   r.Type,
		ReferenceTableSize: uint64(len(encodeRefs(r.Refs))),
		SizeFinal:          uint64(r.DataSize),
		SizeInMemory:       uint64(r.SysMem),
		SizeInVideoMemory:  uint64(r.VidMem),
		References:         make([]metaRef, len(r.Refs)),
	}
	for i, ref := range r.Refs {
		m.References[i] = metaRef{Hash: hashString(ref.Hash), Flag: fmt.Sprintf("%02X", ref.Flag)}
	}
	return json.Marshal(m)
}

func decodeMetaRefs(b []byte) ([]Ref, error) {
	var m metaJSON
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	refs := make([]Ref, len(m.References))
	for i, mr := range m.References {
		h, err := parseHash(mr.Hash)
		if err != nil {
			return nil, err
		}
		f, err := hex.DecodeString(mr.Flag)
		if err != nil || len(f) != 1 {
			return nil, fmt.Errorf("invalid reference flag %q", mr.Flag)
		}
		refs[i] = Ref{Hash: h, Flag: f[0]}
	}
	return refs, nil
}

// bin1Resource builds a resource from ResourceTool output. For BIN1 data the
// game's in-memory size is the payload size from the BIN1 header.
func bin1Resource(hash uint64, typ string, data []byte, refs []Ref) (*Resource, error) {
	if len(data) < 16 || string(data[:4]) != "BIN1" {
		return nil, fmt.Errorf("%016X.%s: not BIN1 data", hash, typ)
	}
	return &Resource{
		Hash:     hash,
		Type:     typ,
		Refs:     refs,
		DataSize: uint32(len(data)),
		SysMem:   binary.BigEndian.Uint32(data[8:]),
		VidMem:   0xFFFFFFFF,
		data:     data,
	}, nil
}
