package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"strconv"
	"strings"
)

// Localisation (LOCR) and small language resources (DITL, CLNG), ported
// from TonyTools' HMLanguages, plus SMF's manifest localisation features.

const globalLOCR = 0x00F5817876E691F1 // the LOCR SMF adds manifest strings to

var h3Languages = []string{"xx", "en", "fr", "it", "de", "es", "ru", "cn", "tc", "jp"}

// smfLanguages maps SMF manifest language names to LOCR language codes.
var smfLanguages = map[string]string{
	"english": "en", "french": "fr", "italian": "it", "german": "de", "spanish": "es",
	"russian": "ru", "chineseSimplified": "cn", "chineseTraditional": "tc", "japanese": "jp",
}

type locString struct {
	hash uint32
	text string
}

// LOCR is a localisation table: strings per language, in file order.
type LOCR struct {
	langs   []string
	strings map[string][]locString
}

func parseLOCR(b []byte) (*LOCR, error) {
	if len(b) < 5 {
		return nil, errors.New("LOCR too short")
	}
	n := int(binary.LittleEndian.Uint32(b[1:])-1) / 4
	if n > len(h3Languages) || 1+4*n > len(b) {
		return nil, fmt.Errorf("LOCR has %d languages", n)
	}
	l := &LOCR{langs: h3Languages[:n], strings: map[string][]locString{}}
	for i, lang := range l.langs {
		off := binary.LittleEndian.Uint32(b[1+4*i:])
		if off == 0xFFFFFFFF {
			continue
		}
		r := &byteReader{b: b, i: int(off)}
		count := r.u32()
		for range count {
			h, size := r.u32(), int(r.u32())
			if r.err != nil || r.i+size+1 > len(b) {
				return nil, errors.New("LOCR truncated")
			}
			text := xteaDecryptText(locKey, b[r.i:r.i+size])
			r.i += size + 1
			l.strings[lang] = append(l.strings[lang], locString{h, text})
		}
		if r.err != nil {
			return nil, r.err
		}
	}
	return l, nil
}

func (l *LOCR) encode() []byte {
	var buf bytes.Buffer
	buf.WriteByte(0)
	buf.Write(make([]byte, 4*len(l.langs)))
	for i, lang := range l.langs {
		ss := l.strings[lang]
		if len(ss) == 0 {
			binary.LittleEndian.PutUint32(buf.Bytes()[1+4*i:], 0xFFFFFFFF)
			continue
		}
		binary.LittleEndian.PutUint32(buf.Bytes()[1+4*i:], uint32(buf.Len()))
		binary.Write(&buf, binary.LittleEndian, uint32(len(ss)))
		for _, s := range ss {
			enc := xteaEncrypt(locKey, []byte(s.text))
			binary.Write(&buf, binary.LittleEndian, s.hash)
			binary.Write(&buf, binary.LittleEndian, uint32(len(enc)))
			buf.Write(enc)
			buf.WriteByte(0)
		}
	}
	return buf.Bytes()
}

// set replaces a string, or appends it if the language doesn't have it.
func (l *LOCR) set(lang string, hash uint32, text string) {
	for i, s := range l.strings[lang] {
		if s.hash == hash {
			l.strings[lang][i].text = text
			return
		}
	}
	l.strings[lang] = append(l.strings[lang], locString{hash, text})
}

// lineKey is how HMLanguageTools turns a string key into a hash: a known
// line name, a hex number, or else the CRC32 of the key.
func lineKey(hl *HashList, s string) uint32 {
	if hl != nil {
		if v, ok := hl.lines[s]; ok {
			return v
		}
	}
	return key(nil, s)
}

func locrResource(hash uint64, l *LOCR) *Resource {
	return rawResource(hash, "LOCR", nil, l.encode())
}

// buildLOCRJSON converts an HMLanguageTools locr.json.
func buildLOCRJSON(path string, hl *HashList) (*Resource, error) {
	j, err := readJSONFile(path)
	if err != nil {
		return nil, err
	}
	hash, err := parseHash(j.strOf("hash"))
	if err != nil {
		return nil, fmt.Errorf("hash: %w", err)
	}
	langs := j.get("languages")
	if langs == nil || langs.obj == nil {
		return nil, errors.New("no languages")
	}
	l := &LOCR{langs: langs.keys, strings: map[string][]locString{}}
	for _, lang := range langs.keys {
		ss := langs.obj[lang]
		for _, k := range ss.keys {
			text, err := ss.obj[k].text()
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", lang, k, err)
			}
			l.strings[lang] = append(l.strings[lang], locString{lineKey(hl, k), text})
		}
	}
	return locrResource(hash, l), nil
}

// buildDITL converts an HMLanguageTools ditl.json (sound tag templates).
func buildDITL(path string, hl *HashList) (*Resource, error) {
	j, err := readJSONFile(path)
	if err != nil {
		return nil, err
	}
	hash, err := parseHash(j.strOf("hash"))
	if err != nil {
		return nil, fmt.Errorf("hash: %w", err)
	}
	tags := j.get("soundtags")
	if tags == nil || tags.obj == nil {
		return nil, errors.New("no soundtags")
	}
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(len(tags.keys)))
	var deps []string
	index := map[string]int{}
	for _, name := range tags.keys {
		dep, err := tags.obj[name].text()
		if err != nil {
			return nil, fmt.Errorf("soundtag %s: %w", name, err)
		}
		i, ok := index[dep]
		if !ok {
			i = len(deps)
			index[dep] = i
			deps = append(deps, dep)
		}
		binary.Write(&buf, binary.LittleEndian, uint32(i))
		binary.Write(&buf, binary.LittleEndian, key(hl.tags, name))
	}
	refs := make([]Ref, len(deps))
	for i, d := range deps {
		h, err := parseHash(d)
		if err != nil {
			return nil, err
		}
		refs[i] = Ref{h, 0x1F}
	}
	return rawResource(hash, "DITL", refs, buf.Bytes()), nil
}

// buildCLNG converts an HMLanguageTools clng.json (which languages cascade).
func buildCLNG(path string) (*Resource, error) {
	j, err := readJSONFile(path)
	if err != nil {
		return nil, err
	}
	hash, err := parseHash(j.strOf("hash"))
	if err != nil {
		return nil, fmt.Errorf("hash: %w", err)
	}
	langs := j.get("languages")
	if langs == nil || langs.obj == nil {
		return nil, errors.New("no languages")
	}
	var data []byte
	for _, k := range langs.keys {
		data = append(data, map[bool]byte{false: 0, true: 1}[string(langs.obj[k].raw) == "true"])
	}
	return rawResource(hash, "CLNG", nil, data), nil
}

// lineResource is SMF's localisedLines: a LINE resource naming a string of
// the global LOCR by the CRC32 of its upper-cased ID.
func lineResource(hash uint64, id string) *Resource {
	data := binary.LittleEndian.AppendUint32(nil, crc32.ChecksumIEEE([]byte(strings.ToUpper(id))))
	return rawResource(hash, "LINE", []Ref{{globalLOCR, 0x1F}}, append(data, 0))
}

// locEdit is one string a manifest sets in a LOCR.
type locEdit struct {
	locr uint64
	lang string // LOCR language code
	key  uint32
	text string
}

// manifestLocalisation turns a manifest's localisation (string IDs) and
// localisationOverrides (decimal hashes, per LOCR) into edits, like SMF.
func manifestLocalisation(c ManifestContent, hl *HashList) ([]locEdit, error) {
	var out []locEdit
	add := func(locr uint64, lang string, k uint32, text string) error {
		code, ok := smfLanguages[lang]
		if !ok {
			return fmt.Errorf("unknown language %q", lang)
		}
		out = append(out, locEdit{locr, code, k, text})
		if code == "en" {
			out = append(out, locEdit{locr, "xx", k, text})
		}
		return nil
	}
	if n := c.Localisation; n != nil && n.obj != nil {
		for _, lang := range n.keys {
			ss := n.obj[lang]
			for _, id := range ss.keys {
				text, err := ss.obj[id].text()
				if err != nil {
					return nil, err
				}
				if err := add(globalLOCR, lang, lineKey(hl, strings.ToUpper(id)), text); err != nil {
					return nil, err
				}
			}
		}
	}
	if n := c.LocalisationOverrides; n != nil && n.obj != nil {
		for _, h := range n.keys {
			locr, err := parseHash(h)
			if err != nil {
				return nil, fmt.Errorf("localisationOverrides: %w", err)
			}
			for _, lang := range n.obj[h].keys {
				ss := n.obj[h].obj[lang]
				for _, id := range ss.keys {
					k, err := strconv.ParseUint(id, 10, 32)
					if err != nil {
						return nil, fmt.Errorf("localisationOverrides: string ID %q is not a decimal hash", id)
					}
					text, err := ss.obj[id].text()
					if err != nil {
						return nil, err
					}
					if err := add(locr, lang, uint32(k), text); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return out, nil
}

func readJSONFile(path string) (*node, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseJSON(stripJSONC(b))
}

func (n *node) strOf(k string) string {
	s, _ := n.get(k).text()
	return s
}

// xteaDecryptText decrypts XTEA-encrypted text, dropping NUL padding.
func xteaDecryptText(key [4]uint32, data []byte) string {
	out := make([]byte, len(data)-len(data)%8)
	for i := 0; i < len(out); i += 8 {
		v0, v1 := binary.LittleEndian.Uint32(data[i:]), binary.LittleEndian.Uint32(data[i+4:])
		sum := uint32(xteaDelta * 32 & 0xFFFFFFFF)
		for range 32 {
			v1 -= ((v0<<4 ^ v0>>5) + v0) ^ (sum + key[sum>>11&3])
			sum -= xteaDelta
			v0 -= ((v1<<4 ^ v1>>5) + v1) ^ (sum + key[sum&3])
		}
		binary.LittleEndian.PutUint32(out[i:], v0)
		binary.LittleEndian.PutUint32(out[i+4:], v1)
	}
	if i := bytes.IndexByte(out, 0); i >= 0 {
		out = out[:i]
	}
	return string(out)
}
