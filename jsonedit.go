package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// Edits to game JSON resources from unlockables.json, contract.json and
// JSON.patch.json files, applied in load order like SMF.

type jsonEdit struct {
	source string // file, for errors
	kind   string // "unlockables", "contract" or "patch"
	target uint64 // patched resource (patch only)
	typ    string // JSON, REPO or ORES (patch only)
	doc    *node
	ops    []patchOp
}

func readJSONEdit(path, kind string) (*jsonEdit, error) {
	doc, err := readJSONFile(path)
	if err != nil {
		return nil, err
	}
	e := &jsonEdit{source: path, kind: kind, doc: doc}
	if kind != "patch" {
		return e, nil
	}
	if e.target, err = parseHash(doc.strOf("file")); err != nil {
		return nil, fmt.Errorf("file: %w", err)
	}
	e.typ = doc.strOf("type")
	if e.typ == "" {
		e.typ = "JSON"
	}
	var raw bytes.Buffer
	if p := doc.get("patch"); p != nil {
		p.encode(&raw)
	}
	if err := json.Unmarshal(raw.Bytes(), &e.ops); err != nil {
		return nil, fmt.Errorf("patch: %w", err)
	}
	return e, nil
}

// editedDoc is a game resource being edited, decoded to JSON. Arrays of
// records (unlockables, the repository) become objects keyed by ID, as SMF
// treats them; the contracts table becomes {hash: contract ID}.
type editedDoc struct {
	chunk int
	base  *Resource
	doc   *node
	refs  []Ref
}

func keyedByID(arr *node, idKey string) *node {
	obj := &node{obj: map[string]*node{}}
	for _, it := range arr.arr {
		obj.set(it.str(idKey), it)
	}
	return obj
}

func (d *Deployer) applyJSONEdits(p *Plan) error {
	if len(p.JSONEdits) == 0 {
		return nil
	}
	idx, err := d.game.index()
	if err != nil {
		return err
	}
	docs := map[uint64]*editedDoc{}
	var order []uint64
	load := func(h uint64, typ string) (*editedDoc, error) {
		if ed := docs[h]; ed != nil {
			return ed, nil
		}
		chunk, base := p.staged(h)
		if base == nil {
			if base, err = idx.lookup(h); err != nil {
				return nil, err
			}
			chunk = idx.chunkOf(base)
		}
		data, err := base.Data()
		if err != nil {
			return nil, err
		}
		ed := &editedDoc{chunk: chunk, base: base, refs: base.Refs}
		switch {
		case h == unlockablesORES:
			j, err := parseUnlockablesORES(data)
			if err != nil {
				return nil, err
			}
			arr, err := parseJSON(j)
			if err != nil {
				return nil, err
			}
			ed.doc = keyedByID(arr, "Id")
		case h == contractsORES:
			entries, err := parseNameTable(data)
			if err != nil {
				return nil, err
			}
			ed.doc = &node{obj: map[string]*node{}}
			for _, e := range entries {
				id, _ := marshalScalar(e.id)
				ed.doc.set(hashString(e.hash), &node{raw: id})
			}
		default:
			doc, err := parseJSON(data)
			if err != nil {
				return nil, fmt.Errorf("%016X is not JSON: %w", h, err)
			}
			if typ == "REPO" {
				doc = keyedByID(doc, "ID_")
			}
			ed.doc = doc
		}
		docs[h] = ed
		order = append(order, h)
		return ed, nil
	}

	var dests []*node
	for _, e := range p.JSONEdits {
		if err := d.applyJSONEdit(p, e, load); err != nil {
			return fmt.Errorf("%s: %w", d.modFile(e.source), err)
		}
		if e.kind == "contract" {
			if add := e.doc.get("SMF", "destinations", "addToDestinations"); add != nil && string(add.raw) == "true" {
				dests = append(dests, e.doc)
			}
		}
	}
	if len(dests) > 0 {
		ed, err := load(destinationsJSON, "JSON")
		if err != nil {
			return err
		}
		if err := addToDestinations(ed.doc, dests); err != nil {
			return err
		}
	}

	for _, h := range order {
		ed := docs[h]
		var data []byte
		var buf bytes.Buffer
		switch {
		case h == unlockablesORES:
			(&node{isArr: true, arr: values(ed.doc)}).encode(&buf)
			data = buildUnlockablesORES(buf.Bytes())
		case h == contractsORES:
			var entries []nameEntry
			for _, k := range ed.doc.keys {
				hh, err := strconv.ParseUint(k, 16, 64)
				if err != nil {
					return fmt.Errorf("contracts table: %q is not a hash", k)
				}
				id, _ := ed.doc.obj[k].text()
				entries = append(entries, nameEntry{id, hh})
			}
			data = buildNameTable(entries)
		case ed.base.Type == "REPO":
			(&node{isArr: true, arr: values(ed.doc)}).encode(&buf)
			data = buf.Bytes()
		default:
			ed.doc.encode(&buf)
			data = buf.Bytes()
		}
		r := rawResource(h, ed.base.Type, ed.refs, data)
		r.SysMem, r.VidMem = ed.base.SysMem, ed.base.VidMem
		p.put(ed.chunk, r)

		switch {
		case h == unlockablesORES:
			p.Server.unlockables = &node{isArr: true, arr: values(ed.doc)}
		case ed.base.Type == "JSON":
			// a patched contract
			if id, err := ed.doc.get("Metadata", "Id").text(); err == nil && id != "" {
				p.Server.addContract(id, ed.doc)
			}
		}
	}
	return nil
}

// destinationsJSON lists the missions shown in the Destinations menu.
const destinationsJSON = 0x004F4B738474CEAD

// addToDestinations adds contracts to the Destinations registry the way SMF
// does, including where a placeBefore/placeAfter target is missing.
func addToDestinations(registry *node, contracts []*node) error {
	children := registry.get("Root", "Children")
	if children == nil || !children.isArr {
		return errors.New("destinations registry has no Root.Children")
	}
	for _, c := range contracts {
		id, _ := c.get("Metadata", "Id").text()
		dst := c.get("SMF", "destinations")
		before, _ := dst.get("placeBefore").text()
		after, _ := dst.get("placeAfter").text()
		context, _ := dst.get("narrativeContext").text()
		if context == "" {
			context = "Mission"
		}
		idJSON, _ := marshalScalar(id)
		contextJSON, _ := marshalScalar(context)
		entry, err := parseJSON(fmt.Appendf(nil,
			`{"Id":%s,"_comment":"Automatically added by SMF.","NarrativeContext":%s,"Meta":{"Ui":{"Row":3,"Col":5}}}`,
			idJSON, contextJSON))
		if err != nil {
			return err
		}
		find := func(want string) int {
			for i, ch := range children.arr {
				if v, _ := ch.get("Id").text(); v == want {
					return i
				}
			}
			return -1
		}
		// JavaScript's splice treats SMF's -1 from a failed lookup as
		// "before the last element".
		at := len(children.arr)
		switch {
		case before != "":
			at = find(before)
			if at < 0 {
				at = max(len(children.arr)-1, 0)
			}
		case after != "":
			at = find(after) + 1
		}
		children.arr = slices.Insert(children.arr, at, entry)
	}
	return nil
}

func values(obj *node) []*node {
	out := make([]*node, 0, len(obj.keys))
	for _, k := range obj.keys {
		out = append(out, obj.obj[k])
	}
	return out
}

func (d *Deployer) applyJSONEdit(p *Plan, e *jsonEdit, load func(uint64, string) (*editedDoc, error)) error {
	switch e.kind {
	case "unlockables":
		ed, err := load(unlockablesORES, "ORES")
		if err != nil {
			return err
		}
		for _, id := range e.doc.keys {
			item, ok := ed.doc.obj[id]
			if !ok {
				item = &node{obj: map[string]*node{}}
				raw, _ := marshalScalar(id)
				item.set("Id", &node{raw: raw})
				ed.doc.set(id, item)
			}
			mergeInto(item, e.doc.obj[id])
		}
	case "contract":
		ed, err := load(contractsORES, "ORES")
		if err != nil {
			return err
		}
		id, err := e.doc.get("Metadata", "Id").text()
		if err != nil || id == "" {
			return fmt.Errorf("contract without Metadata.Id")
		}
		hash := uint64(0)
		for _, k := range ed.doc.keys {
			if v, _ := ed.doc.obj[k].text(); v == id {
				hash, _ = strconv.ParseUint(k, 16, 64)
			}
		}
		if hash == 0 {
			hash = contractHash(id)
			raw, _ := marshalScalar(id)
			ed.doc.set(hashString(hash), &node{raw: raw})
			ed.refs = append(ed.refs, Ref{hash, 0x9F})
		}
		var buf bytes.Buffer
		e.doc.encode(&buf)
		p.put(0, rawResource(hash, "JSON", nil, buf.Bytes()))
		p.Server.addContract(id, e.doc)
	case "patch":
		ed, err := load(e.target, e.typ)
		if err != nil {
			return err
		}
		return applyPatch(ed.doc, e.ops)
	}
	return nil
}
