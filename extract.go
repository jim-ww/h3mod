package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

// extractArchive unpacks a zip, 7z or rar archive (told apart by content,
// not name) into dst, refusing links and paths that would leave dst.
func extractArchive(ctx context.Context, src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	magic := make([]byte, 8)
	n, _ := io.ReadFull(f, magic)
	f.Close()
	magic = magic[:n]
	switch {
	case bytes.HasPrefix(magic, []byte("PK\x03\x04")), bytes.HasPrefix(magic, []byte("PK\x05\x06")):
		err = extractZip(ctx, src, dst)
	case bytes.HasPrefix(magic, []byte("7z\xbc\xaf\x27\x1c")):
		err = extract7z(ctx, src, dst)
	case bytes.HasPrefix(magic, []byte("Rar!\x1a\x07")):
		err = extractRar(ctx, src, dst)
	case bytes.HasPrefix(magic, []byte("\x1f\x8b")):
		err = extractTarGz(ctx, src, dst)
	default:
		return fmt.Errorf("%s is not a zip, 7z, rar or tar.gz archive", filepath.Base(src))
	}
	if err != nil {
		return fmt.Errorf("extracting %s: %w", filepath.Base(src), err)
	}
	return nil
}

func extractZip(ctx context.Context, src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if err := writeEntry(ctx, dst, f.Name, f.Mode(), f.Open); err != nil {
			return err
		}
	}
	return nil
}

func extract7z(ctx context.Context, src, dst string) error {
	r, err := sevenzip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if err := writeEntry(ctx, dst, f.Name, f.Mode(), f.Open); err != nil {
			return err
		}
	}
	return nil
}

func extractRar(ctx context.Context, src, dst string) error {
	r, err := rardecode.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		open := func() (io.ReadCloser, error) { return io.NopCloser(r), nil }
		if err := writeEntry(ctx, dst, h.Name, h.Mode(), open); err != nil {
			return err
		}
	}
}

func extractTarGz(ctx context.Context, src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		open := func() (io.ReadCloser, error) { return io.NopCloser(r), nil }
		if err := writeEntry(ctx, dst, h.Name, h.FileInfo().Mode(), open); err != nil {
			return err
		}
	}
}

// writeEntry writes one archive entry under dst, keeping the executable
// bit (tar archives carry it).
func writeEntry(ctx context.Context, dst, name string, mode fs.FileMode, open func() (io.ReadCloser, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rel := filepath.FromSlash(strings.ReplaceAll(name, `\`, "/"))
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("%q would be written outside the folder", name)
	}
	path := filepath.Join(dst, rel)
	switch {
	case mode.IsDir():
		return os.MkdirAll(path, 0o755)
	case !mode.IsRegular():
		return fmt.Errorf("archive contains a link or special file: %s", name)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	in, err := open()
	if err != nil {
		return err
	}
	defer in.Close()
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
