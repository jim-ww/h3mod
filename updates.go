package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
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
