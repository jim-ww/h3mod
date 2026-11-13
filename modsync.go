package main

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
)

// syncMods brings the config up to date with the mods directory: mods that
// appeared since the last run are enabled at the end of the load order, and
// folders that are gone are dropped. It saves the config when it changed and
// returns what happened.
func (c *Config) syncMods() ([]*Mod, []string, error) {
	mods, err := discoverMods(c.abs(c.Mods))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("mods directory %s not found; create it or set \"mods\" in %s", c.abs(c.Mods), c.path)
	}
	if err != nil {
		return nil, nil, err
	}
	dirs := make([]string, len(mods))
	for i, m := range mods {
		dirs[i] = m.Dir
	}
	var notes []string
	changed := false
	if c.Known == nil && len(c.Enabled) > 0 {
		// A config from before mods were tracked: take what is there as known
		// and leave enabling as it was. Without enabled mods it is treated as
		// a first run.
		c.Known, changed = slices.Clone(dirs), true
	}
	var gone []string
	c.Enabled = slices.DeleteFunc(c.Enabled, func(d string) bool {
		if slices.Contains(dirs, d) {
			return false
		}
		gone = append(gone, d)
		return true
	})
	if len(gone) > 0 {
		changed = true
		notes = append(notes, "removed, folder is gone: "+strings.Join(gone, ", "))
	}
	n := len(c.Known)
	c.Known = slices.DeleteFunc(c.Known, func(d string) bool { return !slices.Contains(dirs, d) })
	changed = changed || len(c.Known) != n
	var added []string
	for _, d := range dirs {
		if !slices.Contains(c.Known, d) {
			c.Known = append(c.Known, d)
			if !c.isEnabled(d) {
				c.Enabled = append(c.Enabled, d)
			}
			added = append(added, d)
		}
	}
	if len(added) > 0 {
		changed = true
		notes = append(notes, "new mods enabled: "+strings.Join(added, ", "))
	}
	if changed {
		if err := c.save(); err != nil {
			return nil, nil, err
		}
	}
	return mods, notes, nil
}

// listed is the order status shows mods in, which is what mod numbers refer
// to: enabled ones in load order, then disabled ones.
func listed(cfg *Config, mods []*Mod) []*Mod {
	var out []*Mod
	for _, d := range cfg.Enabled {
		if m := findMod(mods, d); m != nil && m.Dir == d {
			out = append(out, m)
		}
	}
	for _, m := range mods {
		if !cfg.isEnabled(m.Dir) {
			out = append(out, m)
		}
	}
	return out
}

// resolveMod finds the mod an argument names: its folder or ID, its number
// in status, or a part of its folder, title or ID that only it has.
func resolveMod(cfg *Config, mods []*Mod, arg string) (*Mod, error) {
	if m := findMod(mods, arg); m != nil {
		return m, nil
	}
	l := listed(cfg, mods)
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(l) {
			return nil, fmt.Errorf("no mod number %d; status lists %d", n, len(l))
		}
		return l[n-1], nil
	}
	want := strings.ToLower(arg)
	var match []*Mod
	for _, m := range l {
		for _, s := range []string{m.Dir, m.Title(), m.ID()} {
			if strings.Contains(strings.ToLower(s), want) {
				match = append(match, m)
				break
			}
		}
	}
	switch len(match) {
	case 0:
		return nil, fmt.Errorf("no mod matches %q in %s", arg, cfg.abs(cfg.Mods))
	case 1:
		return match[0], nil
	}
	var names []string
	for _, m := range match {
		names = append(names, m.Dir)
	}
	return nil, fmt.Errorf("%q matches several mods: %s", arg, strings.Join(names, ", "))
}
