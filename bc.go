package main

import "math"

// Block compression encoders for the texture formats mods use: BC1 (DXT1),
// BC4/BC5 and BC7. Each takes a 4x4 block of RGBA pixels, row by row.

type block [16][4]float64

// principalAxis returns the mean and dominant direction of the block's colors
// over the first n channels, found by power iteration on the covariance.
func principalAxis(b *block, n int) (mean, axis [4]float64) {
	for _, p := range b {
		for c := range n {
			mean[c] += p[c] / 16
		}
	}
	var cov [4][4]float64
	for _, p := range b {
		for i := range n {
			for j := range n {
				cov[i][j] += (p[i] - mean[i]) * (p[j] - mean[j])
			}
		}
	}
	axis = [4]float64{1, 1, 1, 1}
	for range 8 {
		var next [4]float64
		for i := range n {
			for j := range n {
				next[i] += cov[i][j] * axis[j]
			}
		}
		l := 0.0
		for i := range n {
			l += next[i] * next[i]
		}
		if l < 1e-12 {
			break
		}
		l = math.Sqrt(l)
		for i := range n {
			axis[i] = next[i] / l
		}
	}
	return mean, axis
}

// endpoints projects the block onto its principal axis and returns the two
// extreme points.
func endpoints(b *block, n int) (lo, hi [4]float64) {
	mean, axis := principalAxis(b, n)
	minT, maxT := math.Inf(1), math.Inf(-1)
	for _, p := range b {
		t := 0.0
		for c := range n {
			t += (p[c] - mean[c]) * axis[c]
		}
		minT, maxT = min(minT, t), max(maxT, t)
	}
	for c := range n {
		lo[c] = clamp255(mean[c] + axis[c]*minT)
		hi[c] = clamp255(mean[c] + axis[c]*maxT)
	}
	return lo, hi
}

func clamp255(v float64) float64 { return max(0, min(255, v)) }

func dist(a, b [4]float64, n int) float64 {
	d := 0.0
	for c := range n {
		d += (a[c] - b[c]) * (a[c] - b[c])
	}
	return d
}

func nearest(p [4]float64, palette [][4]float64, n int) (int, float64) {
	best, bestD := 0, math.Inf(1)
	for i, q := range palette {
		if d := dist(p, q, n); d < bestD {
			best, bestD = i, d
		}
	}
	return best, bestD
}

func to565(c [4]float64) uint16 {
	r := uint16(math.Round(c[0] * 31 / 255))
	g := uint16(math.Round(c[1] * 63 / 255))
	b := uint16(math.Round(c[2] * 31 / 255))
	return r<<11 | g<<5 | b
}

func from565(v uint16) [4]float64 {
	r, g, b := float64(v>>11&31), float64(v>>5&63), float64(v&31)
	return [4]float64{r * 255 / 31, g * 255 / 63, b * 255 / 31, 255}
}

// encodeBC1 writes 8 bytes. Blocks with transparent pixels use the 3-color
// mode, where index 3 is transparent.
func encodeBC1(b *block, out []byte) {
	transparent := false
	var opaque block
	n := 0
	for _, p := range b {
		if p[3] < 128 {
			transparent = true
			continue
		}
		opaque[n] = p
		n++
	}
	for i := n; i < 16 && n > 0; i++ {
		opaque[i] = opaque[i%n]
	}
	lo, hi := endpoints(&opaque, 3)
	c0, c1 := to565(hi), to565(lo)
	if transparent {
		if c0 > c1 {
			c0, c1 = c1, c0
		}
	} else if c0 < c1 {
		c0, c1 = c1, c0
	}
	e0, e1 := from565(c0), from565(c1)
	var palette [][4]float64
	if c0 > c1 {
		palette = [][4]float64{e0, e1, lerp(e0, e1, 1.0/3), lerp(e0, e1, 2.0/3)}
	} else {
		palette = [][4]float64{e0, e1, lerp(e0, e1, 0.5)}
	}
	var idx uint32
	for i, p := range b {
		k := 3
		if !transparent || p[3] >= 128 {
			k, _ = nearest(p, palette, 3)
		}
		if c0 == c1 && !transparent {
			k = 0
		}
		idx |= uint32(k) << (2 * i)
	}
	out[0], out[1], out[2], out[3] = byte(c0), byte(c0>>8), byte(c1), byte(c1>>8)
	out[4], out[5], out[6], out[7] = byte(idx), byte(idx>>8), byte(idx>>16), byte(idx>>24)
}

func lerp(a, b [4]float64, t float64) [4]float64 {
	var o [4]float64
	for c := range 4 {
		o[c] = a[c] + (b[c]-a[c])*t
	}
	return o
}

// encodeBC4 writes 8 bytes for one channel of the block.
func encodeBC4(b *block, ch int, out []byte) {
	lo, hi := 255.0, 0.0
	for _, p := range b {
		lo, hi = min(lo, p[ch]), max(hi, p[ch])
	}
	e0, e1 := byte(math.Round(hi)), byte(math.Round(lo))
	out[0], out[1] = e0, e1
	var palette [8]float64
	palette[0], palette[1] = float64(e0), float64(e1)
	for i := 1; i < 7; i++ {
		palette[i+1] = (float64(7-i)*float64(e0) + float64(i)*float64(e1)) / 7
	}
	var bits uint64
	for i, p := range b {
		k := 0
		if e0 != e1 {
			best := math.Inf(1)
			for j, v := range palette {
				if d := math.Abs(p[ch] - v); d < best {
					k, best = j, d
				}
			}
		}
		bits |= uint64(k) << (3 * i)
	}
	for i := range 6 {
		out[2+i] = byte(bits >> (8 * i))
	}
}

// encodeBC5 writes 16 bytes: red and green as two BC4 blocks.
func encodeBC5(b *block, out []byte) {
	encodeBC4(b, 0, out[:8])
	encodeBC4(b, 1, out[8:16])
}

var bc7Weights4 = [16]float64{0, 4, 9, 13, 17, 21, 26, 30, 34, 38, 43, 47, 51, 55, 60, 64}

// encodeBC7 writes 16 bytes using mode 6: one subset, RGBA endpoints with 7
// bits plus a p-bit each, and 4-bit indices.
func encodeBC7(b *block, out []byte) {
	lo, hi := endpoints(b, 4)
	type candidate struct {
		e      [2][4]int // 7-bit endpoint values
		p      [2]int
		idx    [16]int
		errSum float64
	}
	best := candidate{errSum: math.Inf(1)}
	for p0 := range 2 {
		for p1 := range 2 {
			var c candidate
			c.p = [2]int{p0, p1}
			var full [2][4]float64
			for ch := range 4 {
				for k, v := range [2]float64{lo[ch], hi[ch]} {
					q := int(math.Round((v - float64(c.p[k])) / 2))
					q = max(0, min(127, q))
					c.e[k][ch] = q
					full[k][ch] = float64(q<<1 | c.p[k])
				}
			}
			var palette [16][4]float64
			for i, w := range bc7Weights4 {
				for ch := range 4 {
					palette[i][ch] = math.Floor(((64-w)*full[0][ch] + w*full[1][ch] + 32) / 64)
				}
			}
			for i, px := range b {
				k, d := nearest(px, palette[:], 4)
				c.idx[i], c.errSum = k, c.errSum+d
			}
			if c.errSum < best.errSum {
				best = c
			}
		}
	}
	// The anchor index is stored with 3 bits, so its top bit must be clear.
	if best.idx[0] >= 8 {
		best.e[0], best.e[1] = best.e[1], best.e[0]
		best.p[0], best.p[1] = best.p[1], best.p[0]
		for i := range best.idx {
			best.idx[i] = 15 - best.idx[i]
		}
	}
	var w bitWriter
	w.put(1<<6, 7) // mode 6
	for ch := range 4 {
		w.put(uint64(best.e[0][ch]), 7)
		w.put(uint64(best.e[1][ch]), 7)
	}
	w.put(uint64(best.p[0]), 1)
	w.put(uint64(best.p[1]), 1)
	w.put(uint64(best.idx[0]), 3)
	for _, k := range best.idx[1:] {
		w.put(uint64(k), 4)
	}
	w.bytes(out)
}

// bitWriter packs values least significant bit first into 128 bits.
type bitWriter struct {
	lo, hi uint64
	n      uint
}

func (w *bitWriter) put(v uint64, bits uint) {
	for i := range bits {
		bit := v >> i & 1
		if w.n < 64 {
			w.lo |= bit << w.n
		} else {
			w.hi |= bit << (w.n - 64)
		}
		w.n++
	}
}

func (w *bitWriter) bytes(out []byte) {
	for i := range 8 {
		out[i] = byte(w.lo >> (8 * i))
		out[8+i] = byte(w.hi >> (8 * i))
	}
}
