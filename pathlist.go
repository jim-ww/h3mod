package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
)

// resolvePaths looks up resource paths for hashes in a hash_list.txt
// ("HASH.TYPE,path" lines). Like rpkg-tool, a path is only used when it
// hashes back to its hash.
func resolvePaths(listPath string, want map[uint64]bool) (map[uint64]string, error) {
	f, err := os.Open(listPath)
	if err != nil {
		return nil, fmt.Errorf("hash list: %w", err)
	}
	defer f.Close()
	out := make(map[uint64]string, len(want))
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) < 18 || line[0] == '#' || line[16] != '.' {
			continue
		}
		h, err := strconv.ParseUint(string(line[:16]), 16, 64)
		if err != nil || !want[h] {
			continue
		}
		i := bytes.IndexByte(line, ',')
		if i < 0 || i == len(line)-1 {
			continue
		}
		path := string(line[i+1:])
		if ph, err := parseHash(path); err == nil && ph == h {
			out[h] = path
		}
	}
	return out, sc.Err()
}
