package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"strconv"
	"strings"
)

// Dialogue events (DLGE) from HMLanguageTools' dlge.json, ported from
// TonyTools' HMLanguages (DLGE::Rebuild) for HITMAN 3.

// HashList is TonyTools' hash_list.hmla: names of sound tags and switches.
type HashList struct {
	tags     map[string]uint32
	switches map[string]uint32
	lines    map[string]uint32 // localisation string names
}

func loadHashList(path string) (*HashList, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 16 || string(b[:4]) != "HMLA" {
		return nil, fmt.Errorf("%s: not an hmla hash list", path)
	}
	if crc32.ChecksumIEEE(b[12:]) != binary.LittleEndian.Uint32(b[8:]) {
		return nil, fmt.Errorf("%s: checksum mismatch", path)
	}
	r := &byteReader{b: b, i: 12}
	hl := &HashList{}
	for _, m := range []*map[string]uint32{&hl.tags, &hl.switches, &hl.lines} {
		n := r.u32()
		*m = make(map[string]uint32, n)
		for range n {
			h := r.u32()
			(*m)[r.cstring()] = h
		}
	}
	if r.err != nil {
		return nil, fmt.Errorf("%s: %w", path, r.err)
	}
	return hl, nil
}

type byteReader struct {
	b   []byte
	i   int
	err error
}

func (r *byteReader) u32() uint32 {
	if r.err != nil || r.i+4 > len(r.b) {
		r.err = errors.New("truncated")
		return 0
	}
	v := binary.LittleEndian.Uint32(r.b[r.i:])
	r.i += 4
	return v
}

func (r *byteReader) cstring() string {
	j := bytes.IndexByte(r.b[r.i:], 0)
	if r.err != nil || j < 0 {
		r.err = errors.New("truncated string")
		return ""
	}
	s := string(r.b[r.i : r.i+j])
	r.i += j + 1
	return s
}

// key resolves a name through a table, or a hex number, or else hashes it.
func key(table map[string]uint32, s string) uint32 {
	if v, ok := table[s]; ok {
		return v
	}
	if v, err := strconv.ParseUint(s, 16, 32); err == nil {
		return uint32(v)
	}
	return crc32.ChecksumIEEE([]byte(s))
}

type dlgeFile struct {
	Hash    string        `json:"hash"`
	DITL    string        `json:"DITL"`
	CLNG    string        `json:"CLNG"`
	LangMap string        `json:"langmap"`
	Root    dlgeContainer `json:"rootContainer"`
}

type dlgeContainer struct {
	Type       string                     `json:"type"`
	WavName    string                     `json:"wavName"`
	SoundTag   string                     `json:"soundtag"`
	DefaultWav *string                    `json:"defaultWav"`
	DefaultFfx *string                    `json:"defaultFfx"`
	Languages  map[string]json.RawMessage `json:"languages"`
	Weight     json.RawMessage            `json:"weight"`
	SwitchKey  string                     `json:"switchKey"`
	Default    string                     `json:"default"`
	Cases      []string                   `json:"cases"`
	Containers []dlgeContainer            `json:"containers"`
}

var dlgeTypes = map[string]uint8{"WavFile": 1, "Random": 2, "Switch": 3, "Sequence": 4}

type dlgeBuilder struct {
	hl      *HashList
	langs   []string
	locale  string
	buf     bytes.Buffer
	deps    []string // dependency strings in reference order
	depIdx  map[string]int
	flags   map[string]string
	counter [5]uint32 // per type index; [0] counts containers globally
}

// buildDLGE converts a dlge.json file into a DLGE resource.
func buildDLGE(path string, hl *HashList) (*Resource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f dlgeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	hash, err := parseHash(f.Hash)
	if err != nil {
		return nil, fmt.Errorf("hash: %w", err)
	}
	db := &dlgeBuilder{
		hl:     hl,
		langs:  []string{"xx", "en", "fr", "it", "de", "es", "ru", "cn", "tc", "jp"},
		locale: "en", // HMLanguageTools' default, which SMF uses
		depIdx: map[string]int{},
		flags:  map[string]string{},
	}
	if f.LangMap != "" {
		db.langs = strings.Split(f.LangMap, ",")
	}
	for i := range db.counter {
		db.counter[i] = math.MaxUint32
	}
	db.u32(0)
	db.addDep(f.DITL, "1F")
	db.u32(1)
	db.addDep(f.CLNG, "1F")
	if err := db.container(f.Root); err != nil {
		return nil, err
	}
	t := dlgeTypes[f.Root.Type]
	db.u16(uint16(t)<<12 | uint16(db.counter[countIndex(t)]&0xFFF))

	refs := make([]Ref, len(db.deps))
	for i, d := range db.deps {
		h, err := parseHash(d)
		if err != nil {
			return nil, fmt.Errorf("dependency %q: %w", d, err)
		}
		flag, _ := strconv.ParseUint(db.flags[d], 16, 8)
		refs[i] = Ref{Hash: h, Flag: byte(flag)}
	}
	data := db.buf.Bytes()
	return &Resource{
		Hash:     hash,
		Type:     "DLGE",
		Refs:     refs,
		DataSize: uint32(len(data)),
		SysMem:   math.MaxUint32,
		VidMem:   math.MaxUint32,
		data:     data,
	}, nil
}

func (db *dlgeBuilder) u16(v uint16) { binary.Write(&db.buf, binary.LittleEndian, v) }
func (db *dlgeBuilder) u32(v uint32) { binary.Write(&db.buf, binary.LittleEndian, v) }
func (db *dlgeBuilder) u64(v uint64) { binary.Write(&db.buf, binary.LittleEndian, v) }

// countIndex is the counter a reference to a container of type t uses: wav
// files count separately, everything else uses the global container count.
func countIndex(t uint8) uint8 {
	if t == 1 {
		return 1
	}
	return 0
}

// addDep registers a dependency, or returns the index it already has.
func (db *dlgeBuilder) addDep(dep, flag string) uint32 {
	if i, ok := db.depIdx[dep]; ok {
		return uint32(i)
	}
	db.depIdx[dep] = len(db.deps)
	db.deps = append(db.deps, dep)
	db.flags[dep] = flag
	return uint32(len(db.deps) - 1)
}

func (db *dlgeBuilder) writeDep(dep string, lang int) {
	db.u32(db.addDep(dep, fmt.Sprintf("%02X", 0x80+lang)))
}

// subtitle writes XTEA-encrypted text with its length; empty means none.
func (db *dlgeBuilder) subtitle(s string) {
	if s == "" {
		db.u32(0)
		return
	}
	enc := xteaEncrypt(locKey, []byte(s))
	db.u32(uint32(len(enc)))
	db.buf.Write(enc)
}

func (db *dlgeBuilder) container(c dlgeContainer) error {
	switch c.Type {
	case "WavFile":
		return db.wav(c)
	case "Random":
		return db.random(c)
	case "Switch":
		return db.switchC(c)
	case "Sequence":
		return db.sequence(c)
	}
	return fmt.Errorf("invalid container type %q", c.Type)
}

func (db *dlgeBuilder) wav(c dlgeContainer) error {
	db.buf.WriteByte(1)
	db.u32(key(db.hl.tags, c.SoundTag))
	db.u32(key(nil, c.WavName))
	db.u32(0)
	for i, lang := range db.langs {
		raw, ok := c.Languages[lang]
		if lang == db.locale {
			if c.DefaultWav != nil && c.DefaultFfx != nil {
				db.writeDep(*c.DefaultWav, i)
				db.writeDep(*c.DefaultFfx, i)
			} else {
				db.u64(math.MaxUint64)
			}
			var s string
			if ok {
				json.Unmarshal(raw, &s)
			}
			db.subtitle(s)
			continue
		}
		if !ok {
			db.u64(math.MaxUint64)
			db.u32(0)
			continue
		}
		var full struct {
			Wav      string `json:"wav"`
			Ffx      string `json:"ffx"`
			Subtitle string `json:"subtitle"`
		}
		if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
			if err := json.Unmarshal(raw, &full); err != nil {
				return fmt.Errorf("language %s: %w", lang, err)
			}
			db.writeDep(full.Wav, i)
			db.writeDep(full.Ffx, i)
			db.subtitle(full.Subtitle)
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("language %s: %w", lang, err)
		}
		db.u64(math.MaxUint64)
		db.subtitle(s)
	}
	db.counter[1]++
	return nil
}

// meta is one child reference of a container: type and index, plus values.
type dlgeMeta struct {
	typeIndex uint16
	values    []uint32
}

func (db *dlgeBuilder) writeContainer(typ uint8, group, def uint32, metas []dlgeMeta) {
	db.buf.WriteByte(typ)
	db.u32(group)
	db.u32(def)
	db.u32(uint32(len(metas)))
	for _, m := range metas {
		db.u16(m.typeIndex)
		db.u32(uint32(len(m.values)))
		for _, v := range m.values {
			db.u32(v)
		}
	}
}

func (db *dlgeBuilder) random(c dlgeContainer) error {
	var metas []dlgeMeta
	for _, child := range c.Containers {
		if child.Type != "WavFile" {
			return errors.New("random container with a non-WavFile child")
		}
		if err := db.wav(child); err != nil {
			return err
		}
		w, err := dlgeWeight(child.Weight)
		if err != nil {
			return err
		}
		metas = append(metas, dlgeMeta{1<<12 | uint16(db.counter[1]&0xFFF), []uint32{w}})
	}
	db.writeContainer(2, 0, 0, metas)
	db.counter[0]++
	db.counter[2]++
	return nil
}

func dlgeWeight(raw json.RawMessage) (uint32, error) {
	if len(raw) == 0 {
		return 0, errors.New("random container child without weight")
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		v, err := strconv.ParseUint(s, 16, 32)
		if err != nil {
			return 0, fmt.Errorf("invalid weight %q", s)
		}
		return uint32(v), nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, err
	}
	return uint32(math.Round(f * 0xFFFFFF)), nil
}

func (db *dlgeBuilder) switchC(c dlgeContainer) error {
	if db.counter[3] != math.MaxUint32 {
		return errors.New("more than one switch container")
	}
	var metas []dlgeMeta
	for _, child := range c.Containers {
		t := dlgeTypes[child.Type]
		if t != 1 && t != 2 {
			return fmt.Errorf("switch container with a %s child", child.Type)
		}
		if err := db.container(child); err != nil {
			return err
		}
		cases := make([]uint32, len(child.Cases))
		for i, cs := range child.Cases {
			cases[i] = key(db.hl.switches, cs)
		}
		metas = append(metas, dlgeMeta{uint16(t)<<12 | uint16(db.counter[t]&0xFFF), cases})
		db.counter[3]++
	}
	db.writeContainer(3, key(db.hl.switches, c.SwitchKey), key(db.hl.switches, c.Default), metas)
	db.counter[0]++
	db.counter[3]++
	return nil
}

func (db *dlgeBuilder) sequence(c dlgeContainer) error {
	if db.counter[4] != math.MaxUint32 {
		return errors.New("more than one sequence container")
	}
	var metas []dlgeMeta
	for _, child := range c.Containers {
		t := dlgeTypes[child.Type]
		if err := db.container(child); err != nil {
			return err
		}
		metas = append(metas, dlgeMeta{uint16(t)<<12 | uint16(db.counter[countIndex(t)]&0xFFF), nil})
		db.counter[4]++
	}
	db.writeContainer(4, 0, 0, metas)
	db.counter[0]++
	db.counter[4]++
	return nil
}
