package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// deltaJob is a HASH~TYPE.delta file: an xdelta3 patch against the game's
// version of a resource, like SMF applies them.
type deltaJob struct {
	path string
	out  *Resource // placeholder in the plan, filled in by build
}

func newDeltaJob(path string) (*deltaJob, error) {
	name := strings.SplitN(filepath.Base(path), ".", 2)[0]
	hs, typ, ok := strings.Cut(name, "~")
	h, err := strconv.ParseUint(hs, 16, 64)
	if !ok || err != nil || len(typ) != 4 {
		return nil, fmt.Errorf("delta name %q: want HASH~TYPE.delta", filepath.Base(path))
	}
	return &deltaJob{path: path, out: &Resource{Hash: h, Type: strings.ToUpper(typ)}}, nil
}

func (j *deltaJob) build(ctx context.Context, idx *Index, cacheDir, workDir string) error {
	base, err := idx.lookup(j.out.Hash)
	if err != nil {
		return err
	}
	data, err := base.Data()
	if err != nil {
		return err
	}
	delta, err := os.ReadFile(j.path)
	if err != nil {
		return err
	}
	h := sha256.New()
	fmt.Fprintf(h, "h3mod delta v1\n%d\n", len(data))
	h.Write(data)
	h.Write(delta)
	dir := filepath.Join(cacheDir, hex.EncodeToString(h.Sum(nil)))
	if res, err := loadResources(dir, 1); err == nil {
		*j.out = *res[0]
		return nil
	}

	xdelta, err := toolPath("xdelta3")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(workDir, "delta-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	src, dst := filepath.Join(work, "base"), filepath.Join(work, "out")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, xdelta, "-d", "-s", src, j.path, dst)
	hideWindow(cmd)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("xdelta3: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	patched, err := os.ReadFile(dst)
	if err != nil {
		return err
	}
	r := rawResource(j.out.Hash, j.out.Type, base.Refs, patched)
	r.SysMem, r.VidMem = base.SysMem, base.VidMem
	if err := storeResources(dir, []*Resource{r}); err != nil {
		return err
	}
	*j.out = *r
	return nil
}
