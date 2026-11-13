package main

import (
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"
)

// decodeBlock decodes one block of our encoders' output (BC7: mode 6 only).
func decodeBlock(format uint16, in []byte) (out [16][4]float64) {
	switch format {
	case fmtDXT1:
		c0, c1 := binary.LittleEndian.Uint16(in), binary.LittleEndian.Uint16(in[2:])
		e0, e1 := from565(c0), from565(c1)
		pal := [][4]float64{e0, e1, lerp(e0, e1, 1.0/3), lerp(e0, e1, 2.0/3)}
		if c0 <= c1 {
			pal = [][4]float64{e0, e1, lerp(e0, e1, 0.5), {0, 0, 0, 0}}
		}
		idx := binary.LittleEndian.Uint32(in[4:])
		for i := range out {
			out[i] = pal[idx>>(2*i)&3]
		}
	case fmtBC5:
		for ch, half := range [][]byte{in[:8], in[8:]} {
			e0, e1 := float64(half[0]), float64(half[1])
			var bits uint64
			for i := range 6 {
				bits |= uint64(half[2+i]) << (8 * i)
			}
			for i := range out {
				k := int(bits >> (3 * i) & 7)
				v := e0
				switch {
				case k == 1:
					v = e1
				case k > 1:
					v = (float64(8-k)*e0 + float64(k-1)*e1) / 7
				}
				out[i][ch] = v
			}
		}
	case fmtBC7:
		lo, hi := binary.LittleEndian.Uint64(in), binary.LittleEndian.Uint64(in[8:])
		pos := uint(0)
		get := func(n uint) int {
			v := 0
			for i := range n {
				p := pos + i
				var bit uint64
				if p < 64 {
					bit = lo >> p & 1
				} else {
					bit = hi >> (p - 64) & 1
				}
				v |= int(bit) << i
			}
			pos += n
			return v
		}
		get(7)
		var e [2][4]int
		for ch := range 4 {
			e[0][ch], e[1][ch] = get(7), get(7)
		}
		p0, p1 := get(1), get(1)
		for ch := range 4 {
			e[0][ch], e[1][ch] = e[0][ch]<<1|p0, e[1][ch]<<1|p1
		}
		for i := range out {
			bits := uint(4)
			if i == 0 {
				bits = 3
			}
			w := bc7Weights4[get(bits)]
			for ch := range 4 {
				out[i][ch] = math.Floor(((64-w)*float64(e[0][ch]) + w*float64(e[1][ch]) + 32) / 64)
			}
		}
	}
	return out
}

func psnr(img *rgbaImage, data []byte, format uint16, channels int) float64 {
	size := 16
	if format == fmtDXT1 {
		size = 8
	}
	bw := (img.w + 3) / 4
	var mse float64
	for by := range (img.h + 3) / 4 {
		for bx := range bw {
			dec := decodeBlock(format, data[(by*bw+bx)*size:])
			for i := range 16 {
				x, y := bx*4+i%4, by*4+i/4
				p := img.pix[(y*img.w+x)*4:]
				for c := range channels {
					d := dec[i][c] - float64(p[c])
					mse += d * d
				}
			}
		}
	}
	mse /= float64(img.w * img.h * channels)
	return 10 * math.Log10(255*255/mse)
}

func TestModTextures(t *testing.T) {
	files, _ := filepath.Glob("../SMF/Mods/Hitman 2 characters/content/chunk*/*.texture.tga")
	if len(files) == 0 {
		t.Skip("no texture.tga files")
	}
	idx, err := loadIndex("../Runtime")
	if err != nil {
		t.Skip("no game files: ", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f)[:16], func(t *testing.T) {
			meta, err := readTextureMeta(f + ".meta")
			if err != nil {
				t.Fatal(err)
			}
			img, err := readTGA(f)
			if err != nil {
				t.Fatal(err)
			}
			res, err := buildTexture(f, func(h uint64) *Resource { return idx.res[h] })
			if err != nil {
				t.Fatal(err)
			}
			text, texd := res[0], res[1]
			// The top mip of the TEXD must decode close to the source image.
			d, err := lz4Decode(texd.data[:binary.LittleEndian.Uint32(text.data[80:])], int(binary.LittleEndian.Uint32(text.data[24:])))
			if err != nil {
				t.Fatal(err)
			}
			channels := map[uint16]int{fmtDXT1: 3, fmtBC5: 2, fmtBC7: 4}[meta.format]
			if q := psnr(img, d, meta.format, channels); q < 30 {
				t.Errorf("format %#x: PSNR %.1f dB", meta.format, q)
			} else {
				t.Logf("format %#x: PSNR %.1f dB, TEXT %d bytes, TEXD %d bytes", meta.format, q, len(text.data), len(texd.data))
			}
			// Header fields that must match the vanilla texture this replaces.
			vr := idx.res[text.Hash]
			if vr == nil {
				return // a new texture
			}
			v, err := vr.Data()
			if err != nil {
				t.Fatal(err)
			}
			for _, off := range []int{0, 2, 8, 12, 14, 16, 18, 20, 24, 28, 140, 144, 145, 146} {
				if v[off] != text.data[off] {
					t.Errorf("header byte %#x: vanilla %#x, ours %#x", off, v[off], text.data[off])
				}
			}
		})
	}
}
