package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Blobs are files a mod serves as the game's online resources
// (cloudstorage), registered in the blobs ORES by path, like SMF.

type blob struct {
	hash uint64
	path string // under cloudstorage/resources, lowercase
}

func (p *Plan) addBlobsFolder(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		bp := strings.ToLower(filepath.ToSlash(rel))
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		suffix, typ := ext, strings.ToUpper(ext)
		switch {
		case strings.HasPrefix(ext, "jp") || ext == "png":
			suffix, typ = "gfx", "GFXI"
		case ext == "json":
			typ = "JSON"
		}
		if len(typ) != 4 {
			return fmt.Errorf("blob %s: type %q is not 4 characters", rel, typ)
		}
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		h := ioiHash("[assembly:/_pro/online/default/cloudstorage/resources/" + bp + "].pc_" + suffix)
		p.add(0, &Resource{Hash: h, Type: typ, DataSize: uint32(st.Size()), SysMem: 0xFFFFFFFF, VidMem: 0xFFFFFFFF,
			path: path, length: st.Size()})
		p.Blobs = append(p.Blobs, blob{h, bp})
		return nil
	})
}

// registerBlobs adds the plan's blobs to the blobs ORES.
func (d *Deployer) registerBlobs(p *Plan) error {
	if len(p.Blobs) == 0 {
		return nil
	}
	chunk, base := p.staged(blobsORES)
	if base == nil {
		idx, err := d.game.index()
		if err != nil {
			return err
		}
		if base, err = idx.lookup(blobsORES); err != nil {
			return err
		}
		chunk = idx.chunkOf(base)
	}
	data, err := base.Data()
	if err != nil {
		return err
	}
	entries, err := parseNameTable(data)
	if err != nil {
		return fmt.Errorf("blobs ORES: %w", err)
	}
	refs := append([]Ref(nil), base.Refs...)
	for _, b := range p.Blobs {
		found := false
		for i := range entries {
			if entries[i].hash == b.hash {
				entries[i].id, found = b.path, true
			}
		}
		if !found {
			entries = append(entries, nameEntry{b.path, b.hash})
		}
		known := false
		for _, r := range refs {
			known = known || r.Hash == b.hash
		}
		if !known {
			refs = append(refs, Ref{b.hash, 0x9F})
		}
	}
	r := rawResource(blobsORES, "ORES", refs, buildNameTable(entries))
	r.SysMem, r.VidMem = base.SysMem, base.VidMem
	p.put(chunk, r)
	return nil
}
