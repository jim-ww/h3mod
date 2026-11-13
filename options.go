package main

import (
	"fmt"
	"slices"
)

// ModOptions is the saved option selection of one mod. Version and Known
// record the mod as it was when the selection was made, so an update of the
// mod can be reconciled instead of silently breaking the selection.
type ModOptions struct {
	Version  string   `json:"version,omitempty"`
	Selected []string `json:"selected"`
	Known    []string `json:"known,omitempty"`
}

// selection returns the option keys to deploy for a mod, plus notes on how a
// saved selection was adapted to the current version of the mod.
func (c *Config) selection(m *Mod) (sel []string, notes []string) {
	man := m.Manifest
	if man == nil || len(man.Options) == 0 {
		return nil, nil
	}
	saved := c.Options[m.Dir]
	if saved == nil {
		return man.defaultSelection(), nil
	}
	keys := man.optionKeys()
	known := saved.Known
	if len(known) == 0 {
		known = keys
	}
	note := func(format string, args ...any) {
		notes = append(notes, fmt.Sprintf(format, args...))
	}
	for _, k := range saved.Selected {
		if slices.Contains(keys, k) {
			sel = append(sel, k)
		} else {
			note("option %q no longer exists", k)
		}
	}
	for _, k := range keys {
		if o := man.option(k); !slices.Contains(known, k) && o.EnabledByDefault && !slices.Contains(sel, k) {
			sel = append(sel, k)
			note("new option %q enabled by default", k)
		}
	}
	return normalizeSelects(man, sel, &notes), notes
}

// normalizeSelects keeps at most one option per select group, falling back to
// the group's default when none is selected.
func normalizeSelects(man *Manifest, sel []string, notes *[]string) []string {
	seen := map[string]bool{}
	out := sel[:0:0]
	for _, k := range sel {
		o := man.option(k)
		if o.Type == "select" {
			if seen[o.Group] {
				continue
			}
			seen[o.Group] = true
		}
		out = append(out, k)
	}
	for _, o := range man.Options {
		if o.Type == "select" && o.EnabledByDefault && !seen[o.Group] {
			seen[o.Group] = true
			out = append(out, o.Key())
			if notes != nil {
				*notes = append(*notes, fmt.Sprintf("no choice for %q, using default %q", o.Group, o.Name))
			}
		}
	}
	return out
}

// setOption turns an option on or off and saves the selection against the
// current mod version. Turning a select option on deselects its group.
func (c *Config) setOption(m *Mod, key string, on bool) error {
	man := m.Manifest
	if man == nil {
		return fmt.Errorf("%s has no options", m.Dir)
	}
	o := man.option(key)
	if o == nil || o.Type == "conditional" {
		return fmt.Errorf("%s has no option %q (see `h3mod options %s`)", m.Dir, key, m.Dir)
	}
	if o.Type == "select" && !on {
		return fmt.Errorf("%q is a choice in group %q: select another option instead", o.Name, o.Group)
	}
	sel, _ := c.selection(m)
	sel = slices.DeleteFunc(sel, func(k string) bool {
		if k == key {
			return true
		}
		other := man.option(k)
		return o.Type == "select" && other.Type == "select" && other.Group == o.Group
	})
	if on {
		sel = append(sel, key)
	}
	c.saveSelection(m, sel)
	return nil
}

func (c *Config) resetOptions(m *Mod) {
	delete(c.Options, m.Dir)
}

func (c *Config) saveSelection(m *Mod, sel []string) {
	if c.Options == nil {
		c.Options = map[string]*ModOptions{}
	}
	c.Options[m.Dir] = &ModOptions{
		Version:  m.Version(),
		Selected: normalizeSelects(m.Manifest, sel, nil),
		Known:    m.Manifest.optionKeys(),
	}
}
