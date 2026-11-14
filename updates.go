package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

// modUpdate is what a mod's updateCheck URL serves.
type modUpdate struct {
	Version   string `json:"version"`
	Changelog string `json:"changelog"`
	URL       string `json:"url"`
}

// checkUpdate fetches a mod's update info; ok reports a newer version.
func checkUpdate(ctx context.Context, m *Mod) (u modUpdate, ok bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	url := m.Manifest.UpdateCheck + "?t=" + strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return u, false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return u, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return u, false, fmt.Errorf("%s: %s", m.Manifest.UpdateCheck, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&u); err != nil {
		return u, false, fmt.Errorf("%s: %w", m.Manifest.UpdateCheck, err)
	}
	latest, err := semver.StrictNewVersion(u.Version)
	if err != nil || u.URL == "" {
		return u, false, fmt.Errorf("%s: invalid update info", m.Manifest.UpdateCheck)
	}
	cur, err := semver.NewVersion(m.Manifest.Version)
	return u, err != nil || latest.GreaterThan(cur), nil
}

var githubAPI = "https://api.github.com"

var githubUpdates = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+)/releases/latest/download/updates\.json$`)

// releaseNotes gathers the notes of every GitHub release newer than the
// installed version, oldest first, merging "##" sections like SMF's Mod
// Manager. It returns "" for mods not released on GitHub.
func releaseNotes(ctx context.Context, m *Mod) (string, error) {
	repo := githubUpdates.FindStringSubmatch(m.Manifest.UpdateCheck)
	if repo == nil {
		return "", nil
	}
	cur, err := semver.NewVersion(m.Manifest.Version)
	if err != nil {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPI+"/repos/"+repo[1]+"/releases", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub releases of %s: %s", repo[1], resp.Status)
	}
	var releases []struct {
		Tag  string `json:"tag_name"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&releases); err != nil {
		return "", err
	}
	var order []string
	sections := map[string][]string{}
	for _, r := range slices.Backward(releases) { // the API lists newest first
		v, err := semver.NewVersion(r.Tag)
		if err != nil || !v.GreaterThan(cur) {
			continue
		}
		section := ""
		for line := range strings.SplitSeq(r.Body, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case line == "" || strings.Contains(line, "hitman-resources.netlify.app/smf-install-link"):
				continue
			case strings.HasPrefix(line, "##"):
				section = line
			default:
				if _, ok := sections[section]; !ok {
					order = append(order, section)
				}
				sections[section] = append(sections[section], line)
			}
		}
	}
	var b strings.Builder
	for _, s := range order {
		if s != "" {
			b.WriteString(s + "\n")
		}
		b.WriteString(strings.Join(sections[s], "\n") + "\n")
	}
	return strings.TrimSpace(b.String()), nil
}

// changelog is what to show for an update: the GitHub release notes when
// there are any, the update file's changelog otherwise.
func changelog(ctx context.Context, m *Mod, u modUpdate) string {
	if notes, err := releaseNotes(ctx, m); err == nil && notes != "" {
		return notes
	}
	return strings.TrimSpace(u.Changelog)
}

// foundUpdate is a newer version of a mod, with what changed.
type foundUpdate struct {
	m     *Mod
	u     modUpdate
	notes string
}

// findUpdates checks the mods that have an update URL; failures go to logf.
func findUpdates(ctx context.Context, mods []*Mod, logf func(string, ...any)) []foundUpdate {
	var out []foundUpdate
	for _, m := range mods {
		if m.Manifest == nil || m.Manifest.UpdateCheck == "" {
			continue
		}
		u, newer, err := checkUpdate(ctx, m)
		switch {
		case err != nil:
			logf("%s: %v", m.Dir, err)
		case newer:
			out = append(out, foundUpdate{m, u, changelog(ctx, m, u)})
		}
	}
	return out
}

// installUpdate downloads an update and installs it into the mod's folder.
func installUpdate(ctx context.Context, cfg *Config, m *Mod, u modUpdate) error {
	work := cfg.abs(filepath.Join("cache", "work"))
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	file, err := download(ctx, u.URL, work)
	if err != nil {
		return err
	}
	defer os.Remove(file)
	if _, err := installMods(ctx, cfg, file, "", true, m.Dir, m.ID()); err != nil {
		return fmt.Errorf("%s: %w", m.Dir, err)
	}
	return nil
}

// download saves url to a new file in dir.
func download(ctx context.Context, url, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	f, err := os.CreateTemp(dir, "download-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, resp.Body)
	err = errors.Join(err, f.Close())
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
