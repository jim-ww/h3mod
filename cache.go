package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// fileKey hashes the contents of files, for cache entries derived from them.
func fileKey(salt string, paths ...string) (string, error) {
	h := sha256.New()
	io.WriteString(h, salt)
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "\n%s\n", filepath.Base(p))
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// cachedHeader is a resource header as stored next to its data in the cache.
type cachedHeader struct {
	Hash     string      `json:"hash"`
	Type     string      `json:"type"`
	Refs     []cachedRef `json:"refs"`
	DataSize uint32      `json:"dataSize"`
	SysMem   uint32      `json:"sysMem"`
	VidMem   uint32      `json:"vidMem"`
}

type cachedRef struct {
	Hash string `json:"hash"`
	Flag byte   `json:"flag"`
}

// storeResources writes resources into dir via a temp dir and rename, so an
// interrupted run never leaves a partial entry.
func storeResources(dir string, res []*Resource) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp := dir + tmpSuffix
	os.RemoveAll(tmp)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		return err
	}
	for i, r := range res {
		data, err := r.Data()
		if err != nil {
			return err
		}
		h := cachedHeader{Hash: hashString(r.Hash), Type: r.Type, DataSize: r.DataSize, SysMem: r.SysMem, VidMem: r.VidMem}
		for _, ref := range r.Refs {
			h.Refs = append(h.Refs, cachedRef{hashString(ref.Hash), ref.Flag})
		}
		hb, err := json.Marshal(h)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, strconv.Itoa(i)+".json"), hb, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, strconv.Itoa(i)+".bin"), data, 0o644); err != nil {
			return err
		}
	}
	os.RemoveAll(dir)
	return os.Rename(tmp, dir)
}

// loadResources reads n resources stored by storeResources. Their data stays
// on disk until written into an archive.
func loadResources(dir string, n int) ([]*Resource, error) {
	out := make([]*Resource, n)
	for i := range out {
		hb, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(i)+".json"))
		if err != nil {
			return nil, err
		}
		var h cachedHeader
		if err := json.Unmarshal(hb, &h); err != nil {
			return nil, err
		}
		hash, err := strconv.ParseUint(h.Hash, 16, 64)
		if err != nil {
			return nil, err
		}
		r := &Resource{Hash: hash, Type: h.Type, DataSize: h.DataSize, SysMem: h.SysMem, VidMem: h.VidMem}
		for _, ref := range h.Refs {
			rh, err := strconv.ParseUint(ref.Hash, 16, 64)
			if err != nil {
				return nil, err
			}
			r.Refs = append(r.Refs, Ref{Hash: rh, Flag: ref.Flag})
		}
		path := filepath.Join(dir, strconv.Itoa(i)+".bin")
		st, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		r.path, r.length = path, st.Size()
		out[i] = r
	}
	return out, nil
}
