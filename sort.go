package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// loadRule names a mod by ID, optionally only when its version is in a
// semver range: "id" or ["id", "range"].
type loadRule struct {
	ID    string
	Range string
}

func (r *loadRule) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &r.ID); err == nil {
		return nil
	}
	var pair []string
	if err := json.Unmarshal(b, &pair); err != nil || len(pair) != 2 {
		return fmt.Errorf("load rule %s: want \"id\" or [\"id\", \"range\"]", b)
	}
	r.ID, r.Range = pair[0], pair[1]
	return nil
}

func (r loadRule) matches(m *Mod) bool {
	if !strings.EqualFold(r.ID, m.ID()) {
		return false
	}
	if r.Range == "" {
		return true
	}
	c, err := semver.NewConstraint(r.Range)
	if err != nil {
		return false
	}
	v, err := semver.NewVersion(m.Manifest.Version)
	return err == nil && c.Check(v)
}

// sortLoadOrder reorders enabled mods so loadBefore and loadAfter rules hold,
// moving as little as possible, like SMF does before each deploy.
func sortLoadOrder(cfg *Config, mods []*Mod, vars map[string]any) ([]string, error) {
	var enabled []*Mod
	for _, dir := range cfg.Enabled {
		if m := findMod(mods, dir); m != nil && m.Dir == dir {
			enabled = append(enabled, m)
		}
	}
	// before[i] lists the mods that must come after mod i.
	before := make([][]int, len(enabled))
	indegree := make([]int, len(enabled))
	edge := func(a, b int) {
		if a != b && !slices.Contains(before[a], b) {
			before[a] = append(before[a], b)
			indegree[b]++
		}
	}
	for i, m := range enabled {
		if m.Manifest == nil {
			continue
		}
		sel, _ := cfg.selection(m)
		for _, c := range m.Manifest.content(sel, vars) {
			for j, o := range enabled {
				if o.Manifest == nil {
					continue
				}
				for _, r := range c.LoadBefore {
					if r.matches(o) {
						edge(i, j)
					}
				}
				for _, r := range c.LoadAfter {
					if r.matches(o) {
						edge(j, i)
					}
				}
			}
		}
	}
	// Kahn's algorithm, always taking the earliest ready mod.
	done := make([]bool, len(enabled))
	order := make([]string, 0, len(enabled))
	for len(order) < len(enabled) {
		next := -1
		for i := range enabled {
			if !done[i] && indegree[i] == 0 {
				next = i
				break
			}
		}
		if next < 0 {
			var cycle []string
			for i, m := range enabled {
				if !done[i] {
					cycle = append(cycle, m.Dir)
				}
			}
			return nil, fmt.Errorf("load order rules form a cycle among %s", strings.Join(cycle, ", "))
		}
		done[next] = true
		order = append(order, enabled[next].Dir)
		for _, b := range before[next] {
			indegree[b]--
		}
	}
	// Folders that are enabled but missing keep their place at the end.
	for _, dir := range cfg.Enabled {
		if !slices.Contains(order, dir) {
			order = append(order, dir)
		}
	}
	return order, nil
}
