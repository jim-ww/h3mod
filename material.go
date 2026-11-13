package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
)

// Materials (MATI, MATT, MATB) from material.json, ported from RPKG-Tool's
// material::material(json_path, output_path), which SMF runs.

type matiType int

const (
	matiFloat matiType = iota
	matiString
	matiInt
	matiProperty
	matiUnknown
)

var matiCodes = map[string]string{
	"Alpha Reference": "AREF", "Alpha Test Enabled": "ATST", "Blend Enabled": "BENA",
	"Binder": "BIND", "Blend Mode": "BMOD", "Color": "COLO", "Culling Mode": "CULL",
	"Decal Blend Diffuse": "DBDE", "Decal Blend Emission": "DBEE", "Decal Blend Normal": "DBNE",
	"Decal Blend Roughness": "DBRE", "Decal Blend Specular": "DBSE", "Enabled": "ENAB",
	"Fog Enabled": "FENA", "Float Value": "FLTV", "Instance": "INST", "Name": "NAME",
	"Opacity": "OPAC", "Render State": "RSTA", "Subsurface Value": "SSBW",
	"Subsurface Blue": "SSVB", "Subsurface Green": "SSVG", "Subsurface Red": "SSVR",
	"Tags": "TAGS", "Texture": "TEXT", "Tiling U": "TILU", "Tiling V": "TILV",
	"Texture Id": "TXID", "Type": "TYPE", "Value": "VALU", "Z Bias": "ZBIA", "Z Offset": "ZOFF",
}

var matiTypes = map[string]matiType{
	"AREF": matiInt, "ATST": matiInt, "BENA": matiInt, "BIND": matiProperty, "BMOD": matiString,
	"COLO": matiProperty, "CULL": matiString, "DBDE": matiInt, "DBEE": matiInt, "DBNE": matiInt,
	"DBRE": matiInt, "DBSE": matiInt, "ENAB": matiInt, "FENA": matiInt, "FLTV": matiProperty,
	"INST": matiProperty, "NAME": matiString, "OPAC": matiFloat, "RSTA": matiProperty,
	"SSBW": matiFloat, "SSVB": matiFloat, "SSVG": matiFloat, "SSVR": matiFloat, "TAGS": matiString,
	"TEXT": matiProperty, "TILU": matiString, "TILV": matiString, "TXID": matiInt,
	"TYPE": matiString, "VALU": matiFloat, "ZBIA": matiInt, "ZOFF": matiFloat,
}

type flagBit struct {
	bit   uint32
	names []string
}

// Flags in ascending bit order, as RPKG-Tool iterates its std::map.
var matClassFlags = []flagBit{
	{0x1, []string{"REFLECTION2D"}}, {0x2, []string{"REFRACTION2D"}}, {0x4, []string{"LIGHTING"}},
	{0x8, []string{"EMISSIVE"}}, {0x10, []string{"DISCARD"}}, {0x20, []string{"LM_SKIN"}},
	{0x40, []string{"PRIMCLASS_STANDARD"}}, {0x80, []string{"PRIMCLASS_LINKED"}},
	{0x100, []string{"PRIMCLASS_WEIGHTED"}}, {0x200, []string{"DOFOVERRIDE"}},
	{0x400, []string{"USES_DEFAULT_VS"}}, {0x800, []string{"USES_SPRITE_SA_VS"}},
	{0x1000, []string{"USES_SPRITE_AO_VS"}}, {0x2000, []string{"ALPHA"}},
	{0x4000, []string{"USES_SIMPLE_SHADER"}}, {0x8000, []string{"DISABLE_INSTANCING"}},
	{0x10000, []string{"LM_HAIR"}}, {0x20000, []string{"SAMPLE_LIGHTING", "UNKNOWN_1"}},
	{0x40000, []string{"HORIZONMAPPING", "UNKNOWN_2"}}, {0x80000, []string{"UNKNOWN_3"}},
	{0x100000, []string{"UNKNOWN_4"}}, {0x200000, []string{"UNKNOWN_5"}},
}

var matInstanceFlags = []flagBit{
	{0x1, []string{"OPAQUE_EMISSIVE"}}, {0x2, []string{"TRANS_EMISSIVE"}},
	{0x4, []string{"TRANSADD_EMISSIVE"}}, {0x6, []string{"TRANSALL"}}, {0x8, []string{"OPAQUE_LIT"}},
	{0x10, []string{"TRANS_LIT"}}, {0x20, []string{"DECAL"}}, {0x40, []string{"REFRACTIVE"}},
	{0x80, []string{"LM_SKIN"}}, {0x100, []string{"LM_HAIR"}}, {0x200, []string{"FORCE_EMISSIVE"}},
	{0x400, []string{"DISABLE_SHADER_LOD"}}, {0x800, []string{"DISCARD"}},
	{0x1000, []string{"DECAL_EMISSIVE"}}, {0x1020, []string{"DECAL_ALL"}},
	{0x2000, []string{"WATER_CLIPPING"}}, {0x4000, []string{"SAMPLE_LIGHTING"}},
	{0x8000, []string{"EXCLUDE_GLOBAL_SHADOWS"}}, {0xFFFFFFFF, []string{"ALL"}},
}

func encodeFlags(flags []flagBit, n *node) uint32 {
	var v uint32
	if n == nil || n.obj == nil {
		return 0
	}
	for _, f := range flags {
		for _, name := range f.names {
			if b, ok := n.obj[name]; ok {
				if string(b.raw) == "true" {
					v += f.bit
				}
				break
			}
		}
	}
	return v
}

// get walks object keys and array indices.
func (n *node) get(path ...any) *node {
	for _, p := range path {
		if n == nil {
			return nil
		}
		switch k := p.(type) {
		case string:
			n = n.obj[k]
		case int:
			if !n.isArr || k >= len(n.arr) {
				return nil
			}
			n = n.arr[k]
		}
	}
	return n
}

func (n *node) size() int {
	switch {
	case n == nil:
		return 0
	case n.obj != nil:
		return len(n.keys)
	case n.isArr:
		return len(n.arr)
	case string(n.raw) == "null":
		return 0
	}
	return 1
}

var errMissing = errors.New("missing value")

func (n *node) float() (float32, error) {
	if n == nil {
		return 0, errMissing
	}
	var f float64
	err := json.Unmarshal(n.raw, &f)
	return float32(f), err
}

func (n *node) text() (string, error) {
	if n == nil {
		return "", errMissing
	}
	var s string
	err := json.Unmarshal(n.raw, &s)
	return s, err
}

func matCode(name string) string {
	if c, ok := matiCodes[name]; ok {
		return c
	}
	return name
}

type matWriter struct{ bytes.Buffer }

func (w *matWriter) u32(v uint32)  { binary.Write(w, binary.LittleEndian, v) }
func (w *matWriter) f32(v float32) { binary.Write(w, binary.LittleEndian, math.Float32bits(v)) }
func (w *matWriter) str(s string)  { w.WriteString(s); w.WriteByte(0) }
func (w *matWriter) align() {
	for w.Len()%0x10 != 0 {
		w.WriteByte(0)
	}
}

// name writes a four-character code reversed, as the game stores them.
func (w *matWriter) name(code string) {
	for i := len(code) - 1; i >= 0; i-- {
		w.WriteByte(code[i])
	}
}

// typ writes a property's type, or nothing for unknown codes (as RPKG-Tool).
func (w *matWriter) typ(code string) {
	if t, ok := matiTypes[code]; ok {
		w.u32(uint32(t))
	}
}

func (w *matWriter) patch(at int, v uint32) {
	binary.LittleEndian.PutUint32(w.Bytes()[at:], v)
}

// pending is a property whose data is written later and pointed to.
type pending struct {
	at   int
	typ  matiType
	data *node
}

// buildMaterial converts a material.json into MATI and, when the file names
// them, MATT and MATB resources.
func buildMaterial(path string) ([]*Resource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	j, err := parseJSON(stripJSONC(b))
	if err != nil {
		return nil, err
	}
	str := func(k string) string { s, _ := j.get(k).text(); return s }
	hashOf := func(k string) (uint64, error) {
		s := str(k)
		if s == "" {
			return 0, nil
		}
		return parseHash(s)
	}
	mati, err := hashOf("MATI")
	if err != nil {
		return nil, err
	}
	matt, err := hashOf("MATT")
	if err != nil {
		return nil, err
	}
	matb, err := hashOf("MATB")
	if err != nil {
		return nil, err
	}
	eres, err := hashOf("ERES")
	if err != nil {
		return nil, err
	}
	mate, err := hashOf("MATE")
	if err != nil {
		return nil, err
	}
	withMATT := matt != 0 && matb != 0

	inst := j.get("Material", "Instance", 0)
	binder := inst.get("Binder", 0)
	var texDeps []uint64
	var txids []uint32
	var texNames []string
	if t := binder.get("Texture"); t != nil {
		for _, tex := range t.arr {
			id, name := tex.get("Texture Id"), tex.get("Name")
			if id == nil || name == nil {
				continue
			}
			s, _ := id.text()
			if s == "" {
				txids = append(txids, 0xFFFFFFFF)
			} else {
				h, err := parseHash(s)
				if err != nil {
					return nil, fmt.Errorf("texture %q: %w", s, err)
				}
				txids = append(txids, uint32(len(texDeps)))
				texDeps = append(texDeps, h)
			}
			n, _ := name.text()
			texNames = append(texNames, n)
		}
	}

	var mattTex []bool
	var mattDeps []uint64
	overrides := j.get("Overrides")
	if withMATT {
		if t := overrides.get("Texture"); t != nil {
			for _, k := range t.keys {
				v, _ := t.obj[k].text()
				used := v != "" && slices.Contains(texNames, "map"+k)
				if used {
					h, err := parseHash(v)
					if err != nil {
						return nil, fmt.Errorf("override %q: %w", v, err)
					}
					mattDeps = append(mattDeps, h)
				}
				mattTex = append(mattTex, used)
			}
		}
	}

	// MATI references: textures, then MATE, then ERES.
	var matiRefs []Ref
	for _, h := range texDeps {
		matiRefs = append(matiRefs, Ref{h, 0x5F})
	}
	eresIndex := uint32(len(texDeps))
	if mate != 0 {
		matiRefs = append(matiRefs, Ref{mate, 0x1F})
		eresIndex++
	}
	if eres != 0 {
		matiRefs = append(matiRefs, Ref{eres, 0x5F})
	} else {
		eresIndex = 0xFFFFFFFF
	}

	typ := str("TYPE")
	var w matWriter
	w.u32(uint32((len(typ)/0x10+1)*0x10 + 0x10))
	w.align()
	w.str(typ)
	w.align()
	instanceOffset := uint32(w.Len() + 0x30)
	for _, v := range []uint32{0x10, uint32(len(texDeps)),
		encodeFlags(matClassFlags, j.get("Flags", "Class")),
		encodeFlags(matInstanceFlags, j.get("Flags", "Instance")),
		eresIndex, 0, 0, instanceOffset, 3, 0, 0, 0} {
		w.u32(v)
	}

	start := w.Len()
	w.name("INST")
	w.u32(uint32(start + 0x10))
	w.u32(uint32(inst.size()))
	w.typ("INST")

	var later []pending
	start = w.Len()
	for _, key := range []string{"Name", "Tags"} {
		code := matCode(key)
		s, _ := inst.get(key).text()
		w.name(code)
		later = append(later, pending{w.Len(), matiString, inst.get(key)})
		w.u32(0)
		w.u32(uint32(len(s) + 1))
		w.typ(code)
	}
	bindCount := 0
	for _, k := range binder.keys {
		bindCount += binder.obj[k].size()
	}
	w.name("BIND")
	w.u32(uint32(start + 0x30))
	w.u32(uint32(bindCount))
	w.typ("BIND")
	for _, k := range binder.keys {
		for _, item := range binder.obj[k].arr {
			w.name(matCode(k))
			later = append(later, pending{w.Len(), matiProperty, item})
			w.u32(0)
			w.u32(uint32(item.size()))
			w.typ(matCode(k))
		}
	}

	txid := 0
	for i := 0; i < len(later); i++ {
		pr := later[i]
		if pr.typ != matiProperty {
			continue
		}
		w.patch(pr.at, uint32(w.Len()))
		for _, k := range pr.data.keys {
			v := pr.data.obj[k]
			code := matCode(k)
			switch matiTypes[code] {
			case matiFloat:
				w.name(code)
				if v.isArr {
					later = append(later, pending{w.Len(), matiFloat, v})
					w.u32(0)
					w.u32(uint32(len(v.arr)))
				} else {
					f, err := v.float()
					if err != nil {
						return nil, fmt.Errorf("%s: %w", k, err)
					}
					w.f32(f)
					w.u32(1)
				}
				w.typ(code)
			case matiString:
				s, _ := v.text()
				w.name(code)
				later = append(later, pending{w.Len(), matiString, v})
				w.u32(0)
				w.u32(uint32(len(s)))
				w.typ(code)
			case matiInt:
				w.name(code)
				if k == "Texture Id" {
					if txid >= len(txids) {
						return nil, fmt.Errorf("texture ids out of range")
					}
					w.u32(txids[txid])
					txid++
				} else {
					var n int64
					if err := json.Unmarshal(v.raw, &n); err != nil {
						return nil, fmt.Errorf("%s: %w", k, err)
					}
					w.u32(uint32(n))
				}
				w.u32(1)
				w.typ(code)
			}
		}
	}
	for _, pr := range later {
		switch {
		case pr.typ == matiFloat && pr.data.isArr:
			w.patch(pr.at, uint32(w.Len()))
			for _, e := range pr.data.arr {
				f, err := e.float()
				if err != nil {
					return nil, err
				}
				w.f32(f)
			}
			w.align()
		case pr.typ == matiString:
			w.patch(pr.at, uint32(w.Len()))
			s, _ := pr.data.text()
			w.str(s)
			w.align()
		}
	}
	res := []*Resource{rawResource(mati, "MATI", matiRefs, w.Bytes())}
	if !withMATT {
		return res, nil
	}

	var tw, bw matWriter
	if overrides != nil {
		for _, k := range overrides.keys {
			v := overrides.obj[k]
			entry := func(typ byte, name string) {
				bw.WriteByte(typ)
				bw.u32(uint32(len(name) + 1))
				bw.str(name)
			}
			floats := func(arr []*node) error {
				for _, e := range arr {
					f, err := e.float()
					if err != nil {
						return err
					}
					tw.f32(f)
				}
				return nil
			}
			switch {
			case k == "Texture":
				index, n := uint32(3), 0
				for _, tk := range v.keys {
					if mattTex[n] {
						tw.WriteByte(1)
						tw.u32(index)
						entry(1, tk)
						index++
					}
					n++
				}
			case k == "Color":
				for _, ck := range v.keys {
					c := v.obj[ck]
					if !c.isArr {
						continue
					}
					t := map[int]byte{3: 2, 4: 3}[len(c.arr)]
					tw.WriteByte(t)
					if err := floats(c.arr); err != nil {
						return nil, err
					}
					entry(t, ck)
				}
			case v.isArr:
				t := map[int]byte{2: 5, 3: 6, 4: 7}[len(v.arr)]
				tw.WriteByte(t)
				if err := floats(v.arr); err != nil {
					return nil, err
				}
				entry(t, k)
			default:
				f, err := v.float()
				if err != nil {
					return nil, fmt.Errorf("override %s: %w", k, err)
				}
				tw.WriteByte(4)
				tw.f32(f)
				entry(4, k)
			}
		}
	}
	const (
		renderMaterialCPPT = 0x00B4B11DA327CAD0 // [modules:/zrendermaterialentity.class].pc_entitytype
		renderMaterialCBLU = 0x00A1595C0918E2C9 // [modules:/zrendermaterialentity.class].pc_entityblueprint
	)
	mattRefs := []Ref{{renderMaterialCPPT, 0x1F}, {matb, 0x1F}, {mati, 0x5F}}
	for _, h := range mattDeps {
		mattRefs = append(mattRefs, Ref{h, 0x5F})
	}
	return append(res,
		rawResource(matt, "MATT", mattRefs, tw.Bytes()),
		rawResource(matb, "MATB", []Ref{{renderMaterialCBLU, 0x1F}}, bw.Bytes()),
	), nil
}

func rawResource(hash uint64, typ string, refs []Ref, data []byte) *Resource {
	return &Resource{Hash: hash, Type: typ, Refs: refs, DataSize: uint32(len(data)),
		SysMem: 0xFFFFFFFF, VidMem: 0xFFFFFFFF, data: data}
}
