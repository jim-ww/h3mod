package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// HITMAN 3 textures from texture.tga files, following TonyTools'
// HMTextureTools (Texture::H3::Rebuild). A file named TEXT~TEXD.texture.tga
// becomes a TEXT (header and a downscaled copy) plus a TEXD (full size).

const textHeaderSize = 0x98

// Texture formats, as stored in TEXT headers.
const (
	fmtRGBA8 = 0x1C
	fmtA8    = 0x42
	fmtDXT1  = 0x49
	fmtBC4   = 0x52
	fmtBC5   = 0x55
	fmtBC7   = 0x5A
)

// textureMeta is TonyTools' 16-byte .texture.tga.meta for H3.
type textureMeta struct {
	typ        uint16
	flags      uint32
	format     uint16
	compressed bool // mips are LZ4 compressed
	scaleW     uint8
	scaleH     uint8
}

func readTextureMeta(path string) (textureMeta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return textureMeta{}, err
	}
	if len(b) < 13 {
		return textureMeta{}, fmt.Errorf("%s: too short", path)
	}
	return textureMeta{
		typ:        binary.LittleEndian.Uint16(b),
		flags:      binary.LittleEndian.Uint32(b[4:]),
		format:     binary.LittleEndian.Uint16(b[8:]),
		compressed: b[10] != 0,
		scaleW:     b[11],
		scaleH:     b[12],
	}, nil
}

// rgbaImage holds 8-bit RGBA pixels.
type rgbaImage struct {
	w, h int
	pix  []byte
}

func readTGA(path string) (*rgbaImage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 18 {
		return nil, errors.New("tga: too short")
	}
	idLen, cmapType, typ := int(b[0]), b[1], b[2]
	w, h := int(binary.LittleEndian.Uint16(b[12:])), int(binary.LittleEndian.Uint16(b[14:]))
	bpp, desc := int(b[16]), b[17]
	if cmapType != 0 || (typ != 2 && typ != 10) || (bpp != 24 && bpp != 32) {
		return nil, fmt.Errorf("tga: unsupported (type %d, %d bpp, color map %d)", typ, bpp, cmapType)
	}
	src := b[18+idLen:]
	n := bpp / 8
	raw := make([]byte, 0, w*h*n)
	if typ == 2 {
		if len(src) < w*h*n {
			return nil, errors.New("tga: truncated")
		}
		raw = src[:w*h*n]
	} else {
		for i := 0; len(raw) < w*h*n; {
			if i >= len(src) {
				return nil, errors.New("tga: truncated RLE data")
			}
			c := int(src[i]&0x7F) + 1
			if src[i]&0x80 != 0 {
				if i+1+n > len(src) {
					return nil, errors.New("tga: truncated RLE data")
				}
				for range c {
					raw = append(raw, src[i+1:i+1+n]...)
				}
				i += 1 + n
			} else {
				if i+1+c*n > len(src) {
					return nil, errors.New("tga: truncated RLE data")
				}
				raw = append(raw, src[i+1:i+1+c*n]...)
				i += 1 + c*n
			}
		}
	}
	img := &rgbaImage{w: w, h: h, pix: make([]byte, w*h*4)}
	topDown := desc&0x20 != 0
	for y := range h {
		sy := y
		if !topDown {
			sy = h - 1 - y
		}
		for x := range w {
			s, d := (sy*w+x)*n, (y*w+x)*4
			img.pix[d], img.pix[d+1], img.pix[d+2] = raw[s+2], raw[s+1], raw[s] // BGR(A)
			img.pix[d+3] = 255
			if n == 4 {
				img.pix[d+3] = raw[s+3]
			}
		}
	}
	return img, nil
}

// half downsamples by two with a box filter (a dimension of 1 stays 1).
func (img *rgbaImage) half() *rgbaImage {
	w, h := max(img.w/2, 1), max(img.h/2, 1)
	out := &rgbaImage{w: w, h: h, pix: make([]byte, w*h*4)}
	for y := range h {
		for x := range w {
			for c := range 4 {
				sum, n := 0, 0
				for dy := range 2 {
					for dx := range 2 {
						sx, sy := min(x*2+dx, img.w-1), min(y*2+dy, img.h-1)
						sum += int(img.pix[(sy*img.w+sx)*4+c])
						n++
					}
				}
				out.pix[(y*w+x)*4+c] = byte((sum + n/2) / n)
			}
		}
	}
	return out
}

func maxMips(w, h int) int {
	n := 1
	for w > 1 || h > 1 {
		w, h = max(w/2, 1), max(h/2, 1)
		n++
	}
	return n
}

// textScale is how much smaller than the TEXD the TEXT copy is.
func textScale(w, h int) int {
	switch w * h {
	case 32768, 65536:
		return 2
	case 131072, 262144:
		return 4
	case 524288, 1048576:
		return 8
	case 2097152, 4194304:
		return 16
	case 8388608, 16777216:
		return 32
	}
	return 1
}

// builtMips is a compressed mip chain with cumulative sizes, like the TEXT
// header stores them.
type builtMips struct {
	w, h      int
	count     int
	sizes     [14]uint32 // cumulative block-compressed size
	lz4Sizes  [14]uint32 // cumulative LZ4 size
	pixels    []byte
	lz4Pixels []byte
}

func buildMips(img *rgbaImage, format uint16, count int, lz4 bool) (*builtMips, error) {
	if count > 14 {
		return nil, fmt.Errorf("texture has %d mips, at most 14 fit", count)
	}
	bm := &builtMips{w: img.w, h: img.h, count: count}
	total, ltotal := 0, 0
	for i := range count {
		if i > 0 {
			img = img.half()
		}
		data, err := encodeImage(img, format)
		if err != nil {
			return nil, err
		}
		total += len(data)
		bm.sizes[i] = uint32(total)
		bm.pixels = append(bm.pixels, data...)
		if lz4 {
			c := lz4Compress(data)
			ltotal += len(c)
			bm.lz4Sizes[i] = uint32(ltotal)
			bm.lz4Pixels = append(bm.lz4Pixels, c...)
		}
	}
	return bm, nil
}

// encodeImage block-compresses an image, blocks in parallel by rows.
func encodeImage(img *rgbaImage, format uint16) ([]byte, error) {
	var size int
	var enc func(*block, []byte)
	switch format {
	case fmtDXT1:
		size, enc = 8, encodeBC1
	case fmtBC4:
		size, enc = 8, func(b *block, o []byte) { encodeBC4(b, 0, o) }
	case fmtBC5:
		size, enc = 16, encodeBC5
	case fmtBC7:
		size, enc = 16, encodeBC7
	case fmtRGBA8:
		return append([]byte(nil), img.pix...), nil
	case fmtA8:
		out := make([]byte, img.w*img.h)
		for i := range out {
			out[i] = img.pix[i*4]
		}
		return out, nil
	default:
		return nil, fmt.Errorf("texture format %#x is not supported", format)
	}
	bw, bh := (img.w+3)/4, (img.h+3)/4
	out := make([]byte, bw*bh*size)
	rows := make(chan int)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Go(func() {
			var b block
			for by := range rows {
				for bx := range bw {
					for i := range 16 {
						x, y := min(bx*4+i%4, img.w-1), min(by*4+i/4, img.h-1)
						p := img.pix[(y*img.w+x)*4:]
						b[i] = [4]float64{float64(p[0]), float64(p[1]), float64(p[2]), float64(p[3])}
					}
					if format == fmtBC5 {
						for i := range b {
							b[i][2] = 255 // like HMTextureTools
						}
					}
					enc(&b, out[(by*bw+bx)*size:])
				}
			}
		})
	}
	for by := range bh {
		rows <- by
	}
	close(rows)
	wg.Wait()
	return out, nil
}

// buildTexture converts a .texture.tga (with its .meta) to TEXT and, for
// TEXT~TEXD names, TEXD resources. vanilla looks up the game's version of a
// resource, whose video memory size is reused for the same dimensions.
func buildTexture(path string, vanilla func(uint64) *Resource) ([]*Resource, error) {
	name := strings.SplitN(filepath.Base(path), ".", 2)[0]
	textName, texdName, both := strings.Cut(name, "~")
	textHash, err := strconv.ParseUint(textName, 16, 64)
	if err != nil {
		return nil, fmt.Errorf("texture name %q: %w", name, err)
	}
	meta, err := readTextureMeta(path + ".meta")
	if err != nil {
		return nil, err
	}
	img, err := readTGA(path)
	if err != nil {
		return nil, err
	}
	header := func(w, h, mips int, sizes, blockSizes [14]uint32, fileSize, textMips int) []byte {
		b := make([]byte, textHeaderSize)
		le := binary.LittleEndian
		le.PutUint16(b, 1)
		le.PutUint16(b[2:], meta.typ)
		le.PutUint32(b[4:], uint32(fileSize))
		le.PutUint32(b[8:], meta.flags)
		le.PutUint16(b[12:], uint16(w))
		le.PutUint16(b[14:], uint16(h))
		le.PutUint16(b[16:], meta.format)
		b[18] = byte(mips)
		b[20] = 1 // interpretAs
		for i := range 14 {
			le.PutUint32(b[24+i*4:], sizes[i])
			le.PutUint32(b[80+i*4:], blockSizes[i])
		}
		le.PutUint32(b[140:], textHeaderSize) // atlas offset
		b[144] = 0xFF
		b[147] = byte(textMips)
		return b
	}
	pick := func(bm *builtMips) ([]byte, [14]uint32) {
		if meta.compressed {
			return bm.lz4Pixels, bm.lz4Sizes
		}
		return bm.pixels, bm.sizes
	}
	vidMem := func(hash uint64, w, h int) uint32 {
		if v := vanilla(hash); v != nil {
			if d, err := v.Data(); err == nil && len(d) >= 16 && v.Type == "TEXT" &&
				int(binary.LittleEndian.Uint16(d[12:])) == w && int(binary.LittleEndian.Uint16(d[14:])) == h {
				return v.VidMem
			}
		}
		return 688128 // what SMF writes
	}

	if !both {
		bm, err := buildMips(img, meta.format, 1, meta.compressed)
		if err != nil {
			return nil, err
		}
		px, blocks := pick(bm)
		data := append(header(bm.w, bm.h, 1, bm.sizes, blocks, textHeaderSize+len(px), 1), px...)
		return []*Resource{{Hash: textHash, Type: "TEXT", DataSize: uint32(len(data)), SysMem: 0xFFFFFFFF,
			VidMem: vidMem(textHash, bm.w, bm.h), data: data}}, nil
	}

	texdHash, err := strconv.ParseUint(texdName, 16, 64)
	if err != nil {
		return nil, fmt.Errorf("texture name %q: %w", name, err)
	}
	sf := textScale(img.w, img.h)
	small := img
	for s := sf; s > 1; s /= 2 {
		small = small.half()
	}
	text, err := buildMips(small, meta.format, maxMips(small.w, small.h), meta.compressed)
	if err != nil {
		return nil, err
	}
	texd := text
	if sf > 1 {
		if texd, err = buildMips(img, meta.format, maxMips(img.w, img.h), meta.compressed); err != nil {
			return nil, err
		}
	}
	textPx, _ := pick(text)
	texdPx, texdBlocks := pick(texd)
	h := header(texd.w, texd.h, texd.count, texd.sizes, texdBlocks, textHeaderSize+len(texdPx), text.count)
	h[145], h[146] = meta.scaleW, meta.scaleH
	textData := append(h, textPx...)
	vm := vidMem(textHash, texd.w, texd.h)
	return []*Resource{
		{Hash: textHash, Type: "TEXT", Refs: []Ref{{Hash: texdHash, Flag: 0x9F}}, DataSize: uint32(len(textData)),
			SysMem: 0xFFFFFFFF, VidMem: vm, data: textData},
		{Hash: texdHash, Type: "TEXD", DataSize: uint32(len(texdPx)), SysMem: 0xFFFFFFFF, VidMem: vm, data: texdPx},
	}, nil
}

// textureJob is a texture.tga whose resources the plan already holds as
// placeholders; build fills them in, from the cache when possible.
type textureJob struct {
	path string
	out  []*Resource // TEXT, then TEXD when there is one
}

func newTextureJob(path string) (*textureJob, error) {
	name := strings.SplitN(filepath.Base(path), ".", 2)[0]
	j := &textureJob{path: path}
	for i, part := range strings.Split(name, "~") {
		h, err := strconv.ParseUint(part, 16, 64)
		if err != nil || i > 1 {
			return nil, fmt.Errorf("texture name %q: want TEXT or TEXT~TEXD hashes", name)
		}
		j.out = append(j.out, &Resource{Hash: h, Type: []string{"TEXT", "TEXD"}[i]})
	}
	return j, nil
}

func (j *textureJob) build(cacheDir string, vanilla func(uint64) *Resource) error {
	key, err := fileKey("h3mod texture v1", j.path, j.path+".meta")
	if err != nil {
		return err
	}
	dir := filepath.Join(cacheDir, key)
	res, err := loadResources(dir, len(j.out))
	if err != nil {
		if res, err = buildTexture(j.path, vanilla); err != nil {
			return err
		}
		if err := storeResources(dir, res); err != nil {
			return err
		}
	}
	for i, r := range res {
		*j.out[i] = *r
	}
	return nil
}
