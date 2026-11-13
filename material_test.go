package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// matiTree renders a MATI's header and property tree as text, independent
// of where the data is laid out in the file.
func matiTree(b []byte) (string, error) {
	le := binary.LittleEndian
	var out strings.Builder
	cstr := func(at uint32) string {
		if int(at) >= len(b) {
			return "<bad offset>"
		}
		end := bytes.IndexByte(b[at:], 0)
		return string(b[at : int(at)+end])
	}
	h := le.Uint32(b)
	fmt.Fprintf(&out, "type=%s textures=%d class=%#x instance=%#x eres=%d\n",
		cstr(le.Uint32(b[h:])), le.Uint32(b[h+4:]), le.Uint32(b[h+8:]), le.Uint32(b[h+12:]), int32(le.Uint32(b[h+16:])))
	var walk func(at uint32, depth int) error
	walk = func(at uint32, depth int) error {
		if int(at)+16 > len(b) {
			return fmt.Errorf("entry out of range at %#x", at)
		}
		name := string([]byte{b[at+3], b[at+2], b[at+1], b[at]})
		data, size, typ := le.Uint32(b[at+4:]), le.Uint32(b[at+8:]), le.Uint32(b[at+12:])
		fmt.Fprintf(&out, "%s%s ", strings.Repeat("  ", depth), name)
		switch typ {
		case 0:
			if size == 1 {
				fmt.Fprintf(&out, "%g\n", math.Float32frombits(data))
				return nil
			}
			for i := range size {
				fmt.Fprintf(&out, "%g ", math.Float32frombits(le.Uint32(b[data+4*i:])))
			}
			out.WriteString("\n")
		case 1:
			fmt.Fprintf(&out, "%q\n", cstr(data))
		case 2:
			fmt.Fprintf(&out, "%d\n", data)
		case 3:
			out.WriteString("\n")
			for i := range size {
				if err := walk(data+16*i, depth+1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unknown type %d at %#x", typ, at)
		}
		return nil
	}
	err := walk(le.Uint32(b[h+28:]), 0)
	return out.String(), err
}

func treeLines(tree string) []string {
	lines := strings.Split(strings.ReplaceAll(tree, " -0 ", " 0 "), "\n")
	slices.Sort(lines)
	return lines
}

// A rebuilt material must have the same property tree as the game's, apart
// from what the mod changed.
func TestModMaterialMatchesVanilla(t *testing.T) {
	files, _ := filepath.Glob("../SMF/Mods/Hitman 2 characters/content/chunk*/*.material.json")
	if len(files) == 0 {
		t.Skip("no material.json files")
	}
	idx, err := loadIndex("../Runtime")
	if err != nil {
		t.Skip("no game files: ", err)
	}
	for _, f := range files {
		res, err := buildMaterial(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		ours, err := matiTree(res[0].data)
		if err != nil {
			t.Fatalf("our MATI: %v", err)
		}
		v := idx.res[res[0].Hash]
		if v == nil {
			continue
		}
		vd, err := v.Data()
		if err != nil {
			t.Fatal(err)
		}
		van, err := matiTree(vd)
		if err != nil {
			t.Fatalf("vanilla MATI: %v", err)
		}
		// Binder entries follow the order of the JSON, which mods may change,
		// so compare the entries regardless of order.
		ol, vl := treeLines(ours), treeLines(van)
		if !slices.Equal(ol, vl) {
			t.Errorf("property trees differ:\n vanilla %v\n ours    %v", vl, ol)
		}
	}
}
