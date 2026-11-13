package main

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// dlgeShape lists the records of a DLGE: wav tag/name and per-language ref slots, and
// containers with their keys and child references.
func dlgeShape(b []byte, nlang int) ([]string, error) {
	var out []string
	p := 8
	for p < len(b)-2 {
		t := b[p]
		switch t {
		case 1:
			tag, name := binary.LittleEndian.Uint32(b[p+1:]), binary.LittleEndian.Uint32(b[p+5:])
			q := p + 13
			var langs []string
			for range nlang {
				wi, fi := int32(binary.LittleEndian.Uint32(b[q:])), int32(binary.LittleEndian.Uint32(b[q+4:]))
				n := binary.LittleEndian.Uint32(b[q+8:])
				langs = append(langs, fmt.Sprintf("%v/%d", wi >= 0 && fi >= 0, n))
				q += 12 + int(n)
			}
			_ = name
			out = append(out, fmt.Sprintf("wav tag=%08x %s", tag, strings.Join(langs, " ")))
			p = q
		case 2, 3, 4:
			g, d := binary.LittleEndian.Uint32(b[p+1:]), binary.LittleEndian.Uint32(b[p+5:])
			n := binary.LittleEndian.Uint32(b[p+9:])
			q := p + 13
			var metas []string
			for range n {
				ti := binary.LittleEndian.Uint16(b[q:])
				c := binary.LittleEndian.Uint32(b[q+2:])
				var vals []string
				for k := range c {
					vals = append(vals, fmt.Sprintf("%x", binary.LittleEndian.Uint32(b[q+6+int(k)*4:])))
				}
				metas = append(metas, fmt.Sprintf("%04x:%s", ti, strings.Join(vals, ",")))
				q += 6 + int(c)*4
			}
			out = append(out, fmt.Sprintf("c%d %08x %08x [%s]", t, g, d, strings.Join(metas, " ")))
			p = q
		default:
			return out, fmt.Errorf("bad type %d at %d", t, p)
		}
	}
	out = append(out, fmt.Sprintf("root %04x", binary.LittleEndian.Uint16(b[len(b)-2:])))
	return out, nil
}

// TestBuildModDLGE builds every dlge.json of an installed mod and checks the
// result parses as a well-formed DLGE.
func TestBuildModDLGE(t *testing.T) {
	hl, err := loadHashList("../SMF/Third-Party/hash_list.hmla")
	if err != nil {
		t.Skip("no hash list: ", err)
	}
	files, _ := filepath.Glob("../SMF/Mods/Accent Overhaul/*/chunk*/*/*.dlge.json")
	if len(files) == 0 {
		t.Skip("no dlge.json files")
	}
	for _, f := range files {
		r, err := buildDLGE(f, hl)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if _, err := dlgeShape(r.data, 10); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
}
