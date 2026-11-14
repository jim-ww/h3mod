package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// What's in the game (the last deploy, from state.json) versus what the
// next deploy would put there (the config). Enabled isn't deployed.

// deployedMod is what a deploy recorded about a mod.
type deployedMod struct {
	Version string   `json:"version,omitempty"`
	Options []string `json:"options,omitempty"`
}

// recordDeploy notes the mods, versions, options and intro setting a
// deploy put into the game.
func (s *State) recordDeploy(cfg *Config, mods []*Mod, skipIntro bool) {
	s.Deployed, s.Mods, s.SkipIntro = time.Now(), slices.Clone(cfg.Enabled), skipIntro
	s.ModInfo = map[string]deployedMod{}
	for _, dir := range cfg.Enabled {
		if m := findMod(mods, dir); m != nil {
			sel, _ := cfg.selection(m)
			s.ModInfo[dir] = deployedMod{Version: m.Version(), Options: slices.Sorted(slices.Values(sel))}
		}
	}
}

// modDeployStatus describes a mod's place in the game against the config:
// "" for a disabled mod that isn't deployed.
func modDeployStatus(cfg *Config, s *State, m *Mod) string {
	deployed := slices.Contains(s.Mods, m.Dir)
	switch {
	case !cfg.isEnabled(m.Dir) && deployed:
		return "deployed, removed on next deploy"
	case !cfg.isEnabled(m.Dir):
		return ""
	case !deployed:
		return "not deployed yet"
	}
	info, ok := s.ModInfo[m.Dir]
	if !ok {
		return "deployed" // recorded before versions were
	}
	if info.Version != m.Version() {
		return "updated, not deployed yet"
	}
	sel, _ := cfg.selection(m)
	if !slices.Equal(info.Options, slices.Sorted(slices.Values(sel))) {
		return "options changed, not deployed yet"
	}
	return "deployed"
}

// deployStatus describes the game's state in a line, with what the next
// deploy would change, and whether it would change anything.
func deployStatus(cfg *Config, s *State, mods []*Mod) (line string, pending bool) {
	if s.Deployed.IsZero() {
		line = "nothing deployed"
	} else {
		line = fmt.Sprintf("%d mods deployed %s", len(s.Mods), s.Deployed.Local().Format("2006-01-02 15:04"))
	}
	counts := map[string]int{}
	for _, m := range mods {
		switch st := modDeployStatus(cfg, s, m); st {
		case "not deployed yet":
			counts["to add"]++
		case "deployed, removed on next deploy":
			counts["to remove"]++
		case "updated, not deployed yet", "options changed, not deployed yet":
			counts["changed"]++
		}
	}
	for _, dir := range s.Mods { // deployed mods whose folders are gone
		if findMod(mods, dir) == nil || findMod(mods, dir).Dir != dir {
			counts["to remove"]++
		}
	}
	var changes []string
	for _, k := range []string{"to add", "to remove", "changed"} {
		if counts[k] > 0 {
			changes = append(changes, fmt.Sprintf("%d %s", counts[k], k))
		}
	}
	var kept []string // the deployed mods still enabled, to compare their order
	for _, d := range cfg.Enabled {
		if slices.Contains(s.Mods, d) {
			kept = append(kept, d)
		}
	}
	if !slices.Equal(kept, slices.DeleteFunc(slices.Clone(s.Mods), func(d string) bool { return !cfg.isEnabled(d) })) {
		changes = append(changes, "load order")
	}
	// states from before the setting was recorded have no mod info
	if s.ModInfo != nil && s.SkipIntro != cfg.skipIntro() {
		changes = append(changes, "skip intro")
	}
	if len(changes) > 0 {
		line += "; not deployed yet: " + strings.Join(changes, ", ")
	}
	return line, len(changes) > 0
}
