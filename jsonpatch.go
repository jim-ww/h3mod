package main

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// JSON Patch (RFC 6902) on order-preserving JSON, as SMF applies
// JSON.patch.json files.

type patchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	From  string `json:"from"`
	Value *node  `json:"value"`
}

func splitPointer(p string) ([]string, error) {
	if p == "" {
		return nil, nil
	}
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("invalid JSON pointer %q", p)
	}
	parts := strings.Split(p[1:], "/")
	for i, s := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

// locate returns the parent of the pointer's target and the last token.
func locate(doc *node, p string) (*node, string, error) {
	parts, err := splitPointer(p)
	if err != nil {
		return nil, "", err
	}
	if len(parts) == 0 {
		return nil, "", nil
	}
	cur := doc
	for _, k := range parts[:len(parts)-1] {
		if cur, err = child(cur, k); err != nil {
			return nil, "", fmt.Errorf("%s: %w", p, err)
		}
	}
	return cur, parts[len(parts)-1], nil
}

func child(n *node, k string) (*node, error) {
	switch {
	case n.obj != nil:
		if c, ok := n.obj[k]; ok {
			return c, nil
		}
	case n.isArr:
		if i, err := strconv.Atoi(k); err == nil && i >= 0 && i < len(n.arr) {
			return n.arr[i], nil
		}
	}
	return nil, fmt.Errorf("no %q", k)
}

func get(doc *node, p string) (*node, error) {
	parent, k, err := locate(doc, p)
	if err != nil || parent == nil {
		return doc, err
	}
	return child(parent, k)
}

func remove(doc *node, p string) (*node, error) {
	parent, k, err := locate(doc, p)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, errors.New("cannot remove the document root")
	}
	old, err := child(parent, k)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if parent.obj != nil {
		delete(parent.obj, k)
		for i, key := range parent.keys {
			if key == k {
				parent.keys = append(parent.keys[:i], parent.keys[i+1:]...)
				break
			}
		}
	} else {
		i, _ := strconv.Atoi(k)
		parent.arr = append(parent.arr[:i], parent.arr[i+1:]...)
	}
	return old, nil
}

func add(doc *node, p string, v *node) error {
	parent, k, err := locate(doc, p)
	if err != nil {
		return err
	}
	if parent == nil {
		*doc = *v
		return nil
	}
	switch {
	case parent.obj != nil:
		parent.set(k, v)
	case parent.isArr:
		i := len(parent.arr)
		if k != "-" {
			if i, err = strconv.Atoi(k); err != nil || i < 0 || i > len(parent.arr) {
				return fmt.Errorf("%s: invalid array index", p)
			}
		}
		parent.arr = append(parent.arr[:i], append([]*node{v}, parent.arr[i:]...)...)
	default:
		return fmt.Errorf("%s: parent is not a container", p)
	}
	return nil
}

// replace swaps a value in place, keeping object key order.
func replace(doc *node, p string, v *node) error {
	parent, k, err := locate(doc, p)
	if err != nil {
		return err
	}
	if parent == nil {
		*doc = *v
		return nil
	}
	if _, err := child(parent, k); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	if parent.obj != nil {
		parent.obj[k] = v
	} else {
		i, _ := strconv.Atoi(k)
		parent.arr[i] = v
	}
	return nil
}

func clone(n *node) *node {
	c := &node{raw: n.raw, isArr: n.isArr}
	if n.obj != nil {
		c.obj = make(map[string]*node, len(n.obj))
		c.keys = append([]string(nil), n.keys...)
		for k, v := range n.obj {
			c.obj[k] = clone(v)
		}
	}
	for _, v := range n.arr {
		c.arr = append(c.arr, clone(v))
	}
	return c
}

func equalNodes(a, b *node) bool {
	var x, y bytes.Buffer
	a.encode(&x)
	b.encode(&y)
	return bytes.Equal(x.Bytes(), y.Bytes())
}

func applyPatch(doc *node, ops []patchOp) error {
	for _, op := range ops {
		var err error
		switch op.Op {
		case "add":
			err = add(doc, op.Path, op.Value)
		case "remove":
			_, err = remove(doc, op.Path)
		case "replace":
			err = replace(doc, op.Path, op.Value)
		case "move":
			var v *node
			if v, err = remove(doc, op.From); err == nil {
				err = add(doc, op.Path, v)
			}
		case "copy":
			var v *node
			if v, err = get(doc, op.From); err == nil {
				err = add(doc, op.Path, clone(v))
			}
		case "test":
			var v *node
			if v, err = get(doc, op.Path); err == nil && !equalNodes(v, op.Value) {
				err = fmt.Errorf("test failed at %s", op.Path)
			}
		default:
			err = fmt.Errorf("unknown op %q", op.Op)
		}
		if err != nil {
			return fmt.Errorf("%s %s: %w", op.Op, op.Path, err)
		}
	}
	return nil
}
