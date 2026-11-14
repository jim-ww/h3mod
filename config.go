package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Config is h3mod.json. Relative paths are relative to the config file.
type Config struct {
	Game    string                 `json:"game"`
	Mods    string                 `json:"mods"`
	Enabled []string               `json:"enabled"`           // mod folder names in load order; later ones win
	Options map[string]*ModOptions `json:"options,omitempty"` // by mod folder
	// SkipIntro starts the game at the main menu, like SMF's option; it
	// changes Retail/thumbs.dat, which undeploy restores.
	// Unset means on.
	SkipIntro *bool `json:"skipIntro,omitempty"`
	// DeploySummary is where Peacock looks for SMF's deploy summary;
	// empty means its default on Linux, "off" writes none.
	DeploySummary string `json:"deploySummary,omitempty"`
	// SMF is Simple Mod Framework's folder for importing; empty means SMF
	// in the game folder.
	SMF string `json:"smf,omitempty"`
	// CheckUpdates checks mods' update URLs on start (GUI) and in status.
	CheckUpdates bool `json:"checkUpdates,omitempty"`
	// Known are the mod folders seen so far; any other folder is new and
	// gets enabled.
	Known []string `json:"known"`

	path  string
	fresh bool // there was no config file
}

func loadConfig(path string) (*Config, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c := &Config{Game: "..", Mods: "mods", path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		c.fresh = true
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	return c, nil
}

// cacheDir is where built files are cached: next to the config, or in the
// user cache folder for a config in the user config folder.
func (c *Config) cacheDir() string {
	if c.path == xdgConfigPath() {
		if dir, err := os.UserCacheDir(); err == nil {
			return filepath.Join(dir, "h3mod")
		}
	}
	return c.abs("cache")
}

func (c *Config) skipIntro() bool { return c.SkipIntro == nil || *c.SkipIntro }

func (c *Config) save() error {
	b, err := json.MarshalIndent(c, "", "\t")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(c.path, append(b, '\n'))
}

func (c *Config) abs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(filepath.Dir(c.path), p)
}

func (c *Config) runtimeDir() string { return filepath.Join(c.abs(c.Game), "Runtime") }

func (c *Config) isEnabled(dir string) bool {
	for _, e := range c.Enabled {
		if e == dir {
			return true
		}
	}
	return false
}
