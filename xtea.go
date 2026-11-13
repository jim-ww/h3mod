package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// packagedefinition.txt and thumbs.dat: a fixed magic, CRC32 of the
// plaintext, then the plaintext XTEA-encrypted in 8-byte blocks.
var (
	pdMagic = []byte{0x22, 0x3d, 0x6f, 0x9a, 0xb3, 0xf8, 0xfe, 0xb6, 0x61, 0xd9, 0xcc, 0x1c, 0x62, 0xde, 0x83, 0x41}
	pdKey   = [4]uint32{0x30F95282, 0x1F48C419, 0x295F8548, 0x2A78366D}
)

const xteaDelta = 0x9E3779B9

func decryptPD(b []byte) ([]byte, error) {
	if len(b) < 20 || !bytes.Equal(b[:16], pdMagic) {
		return nil, errors.New("not an encrypted packagedefinition")
	}
	body := b[20:]
	if len(body)%8 != 0 {
		return nil, errors.New("encrypted packagedefinition not block aligned")
	}
	out := make([]byte, len(body))
	for i := 0; i < len(body); i += 8 {
		v0, v1 := binary.LittleEndian.Uint32(body[i:]), binary.LittleEndian.Uint32(body[i+4:])
		sum := uint32(xteaDelta * 32 & 0xFFFFFFFF)
		for range 32 {
			v1 -= ((v0<<4 ^ v0>>5) + v0) ^ (sum + pdKey[sum>>11&3])
			sum -= xteaDelta
			v0 -= ((v1<<4 ^ v1>>5) + v1) ^ (sum + pdKey[sum&3])
		}
		binary.LittleEndian.PutUint32(out[i:], v0)
		binary.LittleEndian.PutUint32(out[i+4:], v1)
	}
	want := binary.LittleEndian.Uint32(b[16:])
	if crc32.ChecksumIEEE(out) == want {
		return out, nil
	}
	// h6xtea (SMF) pads with NULs and checksums the unpadded text.
	if t := bytes.TrimRight(out, "\x00"); crc32.ChecksumIEEE(t) == want {
		return t, nil
	}
	return nil, errors.New("packagedefinition checksum mismatch")
}

// encryptPD pads with a trailing comment line so no NUL padding is needed.
func encryptPD(plain []byte) []byte {
	if !bytes.HasSuffix(plain, []byte("\n")) {
		plain = append(plain, '\r', '\n')
	}
	if r := len(plain) % 8; r != 0 {
		pad := 8 - r
		for pad < 4 {
			pad += 8
		}
		plain = append(plain, '/', '/')
		plain = append(plain, bytes.Repeat([]byte{' '}, pad-4)...)
		plain = append(plain, '\r', '\n')
	}
	out := make([]byte, 20+len(plain))
	copy(out, pdMagic)
	binary.LittleEndian.PutUint32(out[16:], crc32.ChecksumIEEE(plain))
	body := out[20:]
	for i := 0; i < len(plain); i += 8 {
		v0, v1 := binary.LittleEndian.Uint32(plain[i:]), binary.LittleEndian.Uint32(plain[i+4:])
		var sum uint32
		for range 32 {
			v0 += ((v1<<4 ^ v1>>5) + v1) ^ (sum + pdKey[sum&3])
			sum += xteaDelta
			v1 += ((v0<<4 ^ v0>>5) + v0) ^ (sum + pdKey[sum>>11&3])
		}
		binary.LittleEndian.PutUint32(body[i:], v0)
		binary.LittleEndian.PutUint32(body[i+4:], v1)
	}
	return out
}
