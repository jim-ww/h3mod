package main

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// ORES resources: the unlockables list (a JSON string) and the contracts
// table (contract ID -> resource), both small BIN1 files.

const (
	unlockablesORES = 0x0057C2C3941115CA
	contractsORES   = 0x002B07020D21D727
	bin1Relocations = 0x12EBA5ED
	zstringStatic   = 0x40000000
)

// bin1Data returns the data section of a BIN1 file.
func bin1Data(b []byte) ([]byte, error) {
	if len(b) < 16 || string(b[:4]) != "BIN1" {
		return nil, errors.New("not BIN1 data")
	}
	n := int(binary.BigEndian.Uint32(b[8:]))
	if 16+n > len(b) {
		return nil, errors.New("BIN1 data truncated")
	}
	return b[16 : 16+n], nil
}

// bin1File wraps data, whose pointers sit at relocs, as a BIN1 file.
func bin1File(data []byte, relocs []uint32) []byte {
	out := []byte("BIN1\x00\x08\x01\x00")
	out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
	out = append(out, 0, 0, 0, 0)
	out = append(out, data...)
	out = binary.LittleEndian.AppendUint32(out, bin1Relocations)
	out = binary.LittleEndian.AppendUint32(out, uint32(4+4*len(relocs)))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(relocs)))
	for _, r := range relocs {
		out = binary.LittleEndian.AppendUint32(out, r)
	}
	return out
}

// bin1String writes a string's data (length, characters, NUL) at the end of
// buf and returns the offset of the characters.
func bin1String(buf *bytes.Buffer, s string) uint64 {
	binary.Write(buf, binary.LittleEndian, uint32(len(s)+1))
	at := uint64(buf.Len())
	buf.WriteString(s)
	buf.WriteByte(0)
	return at
}

func zstringAt(d []byte, at int) (string, error) {
	if at+16 > len(d) {
		return "", errors.New("string header out of range")
	}
	n := int(binary.LittleEndian.Uint32(d[at:]) &^ zstringStatic)
	p := int(binary.LittleEndian.Uint64(d[at+8:]))
	if p+n > len(d) {
		return "", errors.New("string out of range")
	}
	return string(d[p : p+n]), nil
}

func parseUnlockablesORES(b []byte) ([]byte, error) {
	d, err := bin1Data(b)
	if err != nil {
		return nil, err
	}
	s, err := zstringAt(d, 0)
	return []byte(s), err
}

func buildUnlockablesORES(json []byte) []byte {
	var buf bytes.Buffer
	buf.Write(make([]byte, 16))
	at := bin1String(&buf, string(json))
	d := buf.Bytes()
	binary.LittleEndian.PutUint32(d, uint32(len(json))|zstringStatic)
	binary.LittleEndian.PutUint64(d[8:], at)
	return bin1File(d, []uint32{8})
}

type contractEntry struct {
	id   string
	hash uint64
}

func parseContractsORES(b []byte) ([]contractEntry, error) {
	d, err := bin1Data(b)
	if err != nil {
		return nil, err
	}
	if len(d) < 24 {
		return nil, errors.New("contracts ORES too short")
	}
	begin, end := int(binary.LittleEndian.Uint64(d)), int(binary.LittleEndian.Uint64(d[8:]))
	if begin > end || end > len(d) || (end-begin)%24 != 0 {
		return nil, errors.New("contracts ORES array out of range")
	}
	var out []contractEntry
	for at := begin; at < end; at += 24 {
		id, err := zstringAt(d, at)
		if err != nil {
			return nil, err
		}
		hi, lo := binary.LittleEndian.Uint32(d[at+16:]), binary.LittleEndian.Uint32(d[at+20:])
		out = append(out, contractEntry{id, uint64(hi)<<32 | uint64(lo)})
	}
	return out, nil
}

func buildContractsORES(entries []contractEntry) []byte {
	const head = 0x20 // array pointers, padding and the element count
	strs := head + 24*len(entries)
	var buf bytes.Buffer
	buf.Write(make([]byte, strs))
	relocs := []uint32{0, 8, 16}
	ptrs := make([]uint64, len(entries))
	for i, e := range entries {
		for buf.Len()%4 != 0 {
			buf.WriteByte(0)
		}
		ptrs[i] = bin1String(&buf, e.id)
	}
	d := buf.Bytes()
	le := binary.LittleEndian
	le.PutUint64(d, head)
	le.PutUint64(d[8:], uint64(strs))
	le.PutUint64(d[16:], uint64(strs))
	le.PutUint32(d[0x1C:], uint32(len(entries)))
	for i, e := range entries {
		at := head + 24*i
		le.PutUint32(d[at:], uint32(len(e.id))|zstringStatic)
		le.PutUint64(d[at+8:], ptrs[i])
		le.PutUint32(d[at+16:], uint32(e.hash>>32))
		le.PutUint32(d[at+20:], uint32(e.hash))
		relocs = append(relocs, uint32(at+8))
	}
	return bin1File(d, relocs)
}

// contractHash is the resource SMF stores a new contract under.
func contractHash(id string) uint64 { return ioiHash("smfContract" + id) }
