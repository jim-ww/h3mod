package main

import "encoding/binary"

// lz4Compress encodes src as one LZ4 block (greedy matching, 64 KiB window),
// which lz4Decode and the game read back.
func lz4Compress(src []byte) []byte {
	const (
		minMatch   = 4
		lastLits   = 5  // the block must end with literals
		matchLimit = 12 // no match may start within the last 12 bytes
		hashBits   = 16
		maxOffset  = 65535
	)
	dst := make([]byte, 0, len(src)+len(src)/255+16)
	var table [1 << hashBits]int32
	for i := range table {
		table[i] = -1
	}
	hash := func(i int) uint32 {
		return (binary.LittleEndian.Uint32(src[i:]) * 2654435761) >> (32 - hashBits)
	}
	emit := func(lits []byte, offset, matchLen int) {
		tokLen := min(len(lits), 15)
		tok := byte(tokLen) << 4
		ml := matchLen - minMatch
		if matchLen > 0 {
			tok |= byte(min(ml, 15))
		}
		dst = append(dst, tok)
		if len(lits) >= 15 {
			dst = appendLen(dst, len(lits)-15)
		}
		dst = append(dst, lits...)
		if matchLen == 0 {
			return
		}
		dst = append(dst, byte(offset), byte(offset>>8))
		if ml >= 15 {
			dst = appendLen(dst, ml-15)
		}
	}

	anchor, i := 0, 0
	for i+matchLimit <= len(src) {
		h := hash(i)
		ref := int(table[h])
		table[h] = int32(i)
		if ref < 0 || i-ref > maxOffset || binary.LittleEndian.Uint32(src[ref:]) != binary.LittleEndian.Uint32(src[i:]) {
			i++
			continue
		}
		n := minMatch
		for i+n < len(src)-lastLits && src[ref+n] == src[i+n] {
			n++
		}
		emit(src[anchor:i], i-ref, n)
		i += n
		anchor = i
	}
	emit(src[anchor:], 0, 0)
	return dst
}

func appendLen(dst []byte, n int) []byte {
	for n >= 255 {
		dst = append(dst, 255)
		n -= 255
	}
	return append(dst, byte(n))
}
