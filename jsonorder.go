package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// node is a JSON value that keeps object key order, so rewritten game JSON
// (the repository) differs from the original only where it was patched.
type node struct {
	keys  []string
	obj   map[string]*node // non-nil for objects
	arr   []*node          // used when isArr
	raw   json.RawMessage  // scalars
	isArr bool
}

func parseJSON(b []byte) (*node, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	n, err := decodeNode(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return n, nil
}

func decodeNode(d *json.Decoder) (*node, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			n := &node{obj: map[string]*node{}}
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				k := kt.(string)
				v, err := decodeNode(d)
				if err != nil {
					return nil, err
				}
				if _, dup := n.obj[k]; !dup {
					n.keys = append(n.keys, k)
				}
				n.obj[k] = v
			}
			_, err := d.Token()
			return n, err
		case '[':
			n := &node{isArr: true}
			for d.More() {
				v, err := decodeNode(d)
				if err != nil {
					return nil, err
				}
				n.arr = append(n.arr, v)
			}
			_, err := d.Token()
			return n, err
		}
		return nil, fmt.Errorf("unexpected %v", t)
	default:
		raw, err := marshalScalar(t)
		if err != nil {
			return nil, err
		}
		return &node{raw: raw}, nil
	}
}

// marshalScalar encodes like the game files do: no HTML escaping of <, > and &.
func marshalScalar(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

func (n *node) encode(w *bytes.Buffer) {
	switch {
	case n.obj != nil:
		w.WriteByte('{')
		for i, k := range n.keys {
			if i > 0 {
				w.WriteByte(',')
			}
			kb, _ := marshalScalar(k)
			w.Write(kb)
			w.WriteByte(':')
			n.obj[k].encode(w)
		}
		w.WriteByte('}')
	case n.isArr:
		w.WriteByte('[')
		for i, v := range n.arr {
			if i > 0 {
				w.WriteByte(',')
			}
			v.encode(w)
		}
		w.WriteByte(']')
	default:
		w.Write(n.raw)
	}
}

func (n *node) set(k string, v *node) {
	if _, ok := n.obj[k]; !ok {
		n.keys = append(n.keys, k)
	}
	n.obj[k] = v
}

func (n *node) str(k string) string {
	v := n.obj[k]
	if v == nil || v.raw == nil {
		return ""
	}
	var s string
	if json.Unmarshal(v.raw, &s) != nil {
		return ""
	}
	return s
}

// mergeInto merges src into dst like SMF (lodash mergeWith): objects merge
// recursively, everything else — arrays included — is replaced.
func mergeInto(dst, src *node) {
	for _, k := range src.keys {
		s := src.obj[k]
		if d, ok := dst.obj[k]; ok && d.obj != nil && s.obj != nil {
			mergeInto(d, s)
			continue
		}
		dst.set(k, s)
	}
}
