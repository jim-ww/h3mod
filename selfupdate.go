package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

// Self-update replaces a release install (the folder a release archive was
// unpacked to, marked by release.txt) with the latest release. Only the
// entries the release has are replaced; configs, mods and caches next to
// them stay. The archive is checked against the release's checksums and
// unpacked completely before anything changes; the swap is a series of
// renames recorded in a journal, which the next start finishes if it was
// interrupted, so an install is always one whole version.

var selfReleases = "https://api.github.com/repos/jim-ww/h3mod/releases/latest"

const (
	releaseMarker = "release.txt"
	updateStage   = ".h3mod-update"
	updateJournal = ".h3mod-update.json"
	oldPrefix     = ".h3mod-old-"
)

type selfRelease struct {
	Version   string // without the v
	Archive   string // download URLs
	Checksums string
	Notes     string
}

// selfUpdatable says why this program can't update itself, or nil.
func selfUpdatable() error { return selfUpdatableIn(exeDir()) }

func selfUpdatableIn(dir string) error {
	switch {
	case version == "dev":
		return errors.New("h3mod was built from source; rebuild it to update")
	case strings.HasPrefix(dir, "/nix/store/"):
		return errors.New("h3mod is installed with nix; update it with nix")
	}
	if _, err := os.Stat(filepath.Join(dir, releaseMarker)); err != nil {
		return errors.New("h3mod isn't a release install (no release.txt next to it); update it the way it was installed")
	}
	return nil
}

// checkSelfUpdate looks for a newer h3mod release.
func checkSelfUpdate(ctx context.Context) (*selfRelease, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, selfReleases, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("h3mod releases: %s", resp.Status)
	}
	var r struct {
		Tag    string `json:"tag_name"`
		Body   string `json:"body"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return nil, false, err
	}
	rel := &selfRelease{Version: strings.TrimPrefix(r.Tag, "v"), Notes: strings.TrimSpace(r.Body)}
	archive := fmt.Sprintf("h3mod-%s-linux-x64.tar.gz", rel.Version)
	if runtime.GOOS == "windows" {
		archive = fmt.Sprintf("h3mod-%s-windows-x64.zip", rel.Version)
	}
	for _, a := range r.Assets {
		switch a.Name {
		case archive:
			rel.Archive = a.URL
		case "checksums.txt":
			rel.Checksums = a.URL
		}
	}
	latest, err := semver.NewVersion(rel.Version)
	if err != nil {
		return nil, false, fmt.Errorf("h3mod release %q has no version", r.Tag)
	}
	cur, err := semver.NewVersion(version)
	newer := err != nil || latest.GreaterThan(cur)
	if newer && (rel.Archive == "" || rel.Checksums == "") {
		return nil, false, fmt.Errorf("h3mod %s has no %s with checksums", rel.Version, archive)
	}
	return rel, newer, nil
}

// applySelfUpdate downloads, checks and installs a release over this one.
func applySelfUpdate(ctx context.Context, rel *selfRelease, logf func(string, ...any)) error {
	return applySelfUpdateIn(ctx, rel, exeDir(), logf)
}

func applySelfUpdateIn(ctx context.Context, rel *selfRelease, dir string, logf func(string, ...any)) error {
	if err := selfUpdatableIn(dir); err != nil {
		return err
	}
	unlock, err := lockFileAt(filepath.Join(dir, ".h3mod-update.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	finishSelfUpdateIn(dir) // a previous, interrupted update first

	stage := filepath.Join(dir, updateStage)
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.Mkdir(stage, 0o755); err != nil {
		return err
	}
	logf("downloading h3mod %s", rel.Version)
	archive, err := download(ctx, rel.Archive, stage)
	if err != nil {
		return err
	}
	sums, err := download(ctx, rel.Checksums, stage)
	if err != nil {
		return err
	}
	if err := verifyChecksum(archive, sums, filepath.Base(rel.Archive)); err != nil {
		return err
	}
	unpacked := filepath.Join(stage, "unpacked")
	if err := os.Mkdir(unpacked, 0o755); err != nil {
		return err
	}
	if err := extractArchive(ctx, archive, unpacked); err != nil {
		return err
	}
	// the archive holds one folder: h3mod-linux-x64 or h3mod-windows-x64
	ents, err := os.ReadDir(unpacked)
	if err != nil || len(ents) != 1 || !ents[0].IsDir() {
		return errors.New("the release archive isn't one folder")
	}
	root := filepath.Join(unpacked, ents[0].Name())
	if _, err := os.Stat(filepath.Join(root, releaseMarker)); err != nil {
		return errors.New("the release archive has no release.txt")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	j := updateJournalData{Version: rel.Version, Root: root}
	for _, e := range entries {
		j.Entries = append(j.Entries, e.Name())
	}
	// folders first, programs last: the programs are what people start
	slices.SortStableFunc(j.Entries, func(a, b string) int {
		return compareBool(isProgram(a), isProgram(b))
	})
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, updateJournal), b); err != nil {
		return err
	}
	if err := j.apply(dir); err != nil {
		return fmt.Errorf("updating (it completes on the next start): %w", err)
	}
	logf("updated h3mod to %s", rel.Version)
	return nil
}

func isProgram(name string) bool {
	n := strings.TrimSuffix(name, ".exe")
	return n == "h3mod" || n == "h3mod-gui"
}

type updateJournalData struct {
	Version string   `json:"version"`
	Root    string   `json:"root"`    // the unpacked release
	Entries []string `json:"entries"` // in the order they're swapped
}

// apply moves every staged entry into dir. It can run again after an
// interruption: entries already moved are skipped.
func (j *updateJournalData) apply(dir string) error {
	for _, e := range j.Entries {
		src, dst := filepath.Join(j.Root, e), filepath.Join(dir, e)
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue // moved already
		}
		st, err := os.Lstat(src)
		if err != nil {
			return err
		}
		// Files replace atomically where the OS allows it (not a running
		// program on Windows); folders, and the rest, move the old one aside.
		if !st.IsDir() && runtime.GOOS != "windows" {
			if err := os.Rename(src, dst); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Lstat(dst); err == nil {
			old := filepath.Join(dir, fmt.Sprintf("%s%s-%d", oldPrefix, e, time.Now().UnixNano()))
			if err := os.Rename(dst, old); err != nil {
				return err
			}
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(dir, updateJournal)); err != nil {
		return err
	}
	cleanupSelfUpdate(dir)
	return nil
}

// finishSelfUpdate completes an interrupted update and removes what a
// finished one left (on Windows, programs that were running). Runs at start.
func finishSelfUpdate() { finishSelfUpdateIn(exeDir()) }

func finishSelfUpdateIn(dir string) {
	b, err := os.ReadFile(filepath.Join(dir, updateJournal))
	if err == nil {
		var j updateJournalData
		if json.Unmarshal(b, &j) == nil {
			j.apply(dir)
		}
		return
	}
	cleanupSelfUpdate(dir)
}

func cleanupSelfUpdate(dir string) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), oldPrefix) {
			os.RemoveAll(filepath.Join(dir, e.Name())) // a running program stays until next time
		}
	}
	if _, err := os.Stat(filepath.Join(dir, updateJournal)); err != nil {
		os.RemoveAll(filepath.Join(dir, updateStage))
	}
}

// verifyChecksum checks a file against its line in a checksums.txt.
func verifyChecksum(path, sums, name string) error {
	f, err := os.Open(sums)
	if err != nil {
		return err
	}
	defer f.Close()
	want := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			want = fields[0]
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt has no %s", name)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(b); hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s doesn't match its checksum; not updating", name)
	}
	return nil
}
