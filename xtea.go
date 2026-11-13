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

// locKey encrypts localised text (LOCR strings, DLGE subtitles).
var locKey = [4]uint32{0x53527737, 0x7506499E, 0xBD39AEE3, 0xA59E7268}

// xteaEncrypt encrypts data, NUL-padded to whole blocks.
func xteaEncrypt(key [4]uint32, data []byte) []byte {
	out := make([]byte, (len(data)+7)/8*8)
	copy(out, data)
	for i := 0; i < len(out); i += 8 {
		v0, v1 := binary.LittleEndian.Uint32(out[i:]), binary.LittleEndian.Uint32(out[i+4:])
		var sum uint32
		for range 32 {
			v0 += ((v1<<4 ^ v1>>5) + v1) ^ (sum + key[sum&3])
			sum += xteaDelta
			v1 += ((v0<<4 ^ v0>>5) + v0) ^ (sum + key[sum>>11&3])
		}
		binary.LittleEndian.PutUint32(out[i:], v0)
		binary.LittleEndian.PutUint32(out[i+4:], v1)
	}
	return out
}

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

// encryptPD encrypts like h6xtea, the tool SMF uses: the checksum covers
// the text, and the last block is padded with NULs.
func encryptPD(plain []byte) []byte {
	sum := crc32.ChecksumIEEE(plain)
	out := append(append([]byte(nil), pdMagic...), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[16:], sum)
	return append(out, xteaEncrypt(pdKey, plain)...)
}
