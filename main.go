// Command h3mod deploys HITMAN 3 mods from a folder of unpacked mods,
// compatible with SMF (Simple Mod Framework) mods.
//
// Base game archives are never modified: output goes to chunkNpatch300.rpkg
// plus packagedefinition.txt, each replaced atomically.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// terminalProgress shows deploy progress on one updating line when stdout
// is a terminal, with log lines printed above it.
func terminalProgress(d *Deployer) {
	if st, err := os.Stdout.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return
	}
	var mu sync.Mutex
	shown := false
	d.progress = func(step string, done, total int) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Printf("\r\033[K%s %d/%d", step, done, total)
		shown = true
	}
	log := d.log
	d.log = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		if shown {
			fmt.Print("\r\033[K")
			shown = false
		}
		log(format, args...)
	}
}

// firstRun offers SMF's mods folder when there is no config yet, asking on
// a terminal, and creates the mods folder otherwise.
func firstRun(cfg *Config) error {
	if !cfg.fresh {
		return nil
	}
	if dir := cfg.smfMods(); dir != "" {
		use := false
		if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			fmt.Printf("Found SMF's mods folder at %s.\nUse it, with SMF's load order and options? [Y/n] ", dir)
			answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			answer = strings.ToLower(strings.TrimSpace(answer))
			use = answer == "" || answer == "y" || answer == "yes"
		} else {
			fmt.Printf("note: SMF's mods folder is at %s; set \"mods\" in %s to use it\n", dir, cfg.path)
		}
		if use {
			notes, err := cfg.useSMFMods(dir)
			for _, n := range notes {
				fmt.Println("note:", n)
			}
			return err
		}
	}
	if err := os.MkdirAll(cfg.abs(cfg.Mods), 0o755); err != nil {
		return err
	}
	return cfg.save()
}

// syncMods updates the config from the mods directory and says what changed.
func syncMods(cfg *Config) error {
	_, notes, err := cfg.syncMods()
	for _, n := range notes {
		fmt.Println("note:", n)
		logLine("note: %s", n)
	}
	return err
}

func loadMod(cfg *Config, name string) (*Mod, error) {
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return nil, err
	}
	return resolveMod(cfg, mods, name)
}

func status(cfg *Config) error {
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return err
	}
	st, err := loadState(cfg.abs("state.json"))
	if err != nil {
		return err
	}
	line, pending := deployStatus(cfg, st, mods)
	if pending {
		line += " (h3mod deploy applies them)"
	}
	fmt.Println(line)
	fmt.Println()
	fmt.Println("enabled for the next deploy, in load order (later wins):")
	list := listed(cfg, mods)
	width := 0
	for _, m := range list {
		width = max(width, len(fmt.Sprintf("%s (%s)", m.Title(), m.Dir)))
	}
	for i, m := range list {
		if i == len(cfg.Enabled) {
			fmt.Println("disabled:")
		}
		name := fmt.Sprintf("%s (%s)", m.Title(), m.Dir)
		if s := modDeployStatus(cfg, st, m); s != "" {
			fmt.Printf("  %2d. %-*s  %s\n", i+1, width, name, s)
		} else {
			fmt.Printf("  %2d. %s\n", i+1, name)
		}
		if sel, _ := cfg.selection(m); len(sel) > 0 && cfg.isEnabled(m.Dir) {
			fmt.Printf("      options: %s\n", strings.Join(sel, ", "))
		}
	}
	fmt.Println("mods can be named by number, folder, ID or a unique part of the name; h3mod help lists commands")
	p, err := buildPlan(cfg, newGameData(cfg), false)
	if err != nil {
		return err
	}
	for _, n := range p.Notes {
		fmt.Println("note:", n)
	}
	if len(p.Skipped) > 0 {
		fmt.Println("unsupported in enabled mods:")
		for _, l := range skippedLines(p) {
			fmt.Println(l)
		}
	}
	return nil
}

func toggle(cfg *Config, on bool, names []string) error {
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return err
	}
	var picked []*Mod
	if len(names) == 1 && names[0] == "all" {
		picked = mods
	}
	// Resolve every name first, so numbers refer to the listing as it is.
	for _, n := range names {
		if picked != nil && n == "all" {
			break
		}
		m, err := resolveMod(cfg, mods, n)
		if err != nil {
			return err
		}
		picked = append(picked, m)
	}
	for _, m := range picked {
		switch {
		case on && !cfg.isEnabled(m.Dir):
			cfg.Enabled = append(cfg.Enabled, m.Dir)
			fmt.Println("enabled", m.Dir)
		case !on && cfg.isEnabled(m.Dir):
			cfg.Enabled = slices.DeleteFunc(cfg.Enabled, func(e string) bool { return e == m.Dir })
			fmt.Println("disabled", m.Dir)
		}
	}
	return cfg.save()
}

// removeCmd deletes mods' folders, asking first unless yes is set.
func removeCmd(cfg *Config, names []string, yes bool) error {
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return err
	}
	var picked []*Mod
	for _, n := range names { // resolve all first, so numbers refer to the listing
		m, err := resolveMod(cfg, mods, n)
		if err != nil {
			return err
		}
		if !slices.Contains(picked, m) {
			picked = append(picked, m)
		}
	}
	if !yes {
		var titles []string
		for _, m := range picked {
			titles = append(titles, m.Title())
		}
		if !confirm(fmt.Sprintf("Delete %s from %s?", strings.Join(titles, ", "), cfg.abs(cfg.Mods))) {
			return errors.New("nothing deleted (--yes deletes without asking)")
		}
	}
	for _, m := range picked {
		if err := os.RemoveAll(m.Path); err != nil {
			return err
		}
		fmt.Println("deleted", m.Dir)
		logLine("deleted %s", m.Path)
	}
	if err := syncMods(cfg); err != nil {
		return err
	}
	fmt.Println("deploy to take them out of the game")
	return nil
}

// confirm asks a yes/no question on the terminal; without one it says no.
func confirm(question string) bool {
	if st, err := os.Stdin.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	fmt.Printf("%s [y/N] ", question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func installCmd(ctx context.Context, cfg *Config, src, rpkgName string, replace bool) error {
	if rpkgName == "" {
		rpkgName = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	dirs, err := installMods(ctx, cfg, src, rpkgName, replace, "", "")
	for _, d := range dirs {
		fmt.Println("installed", d)
	}
	return errors.Join(err, syncMods(cfg))
}

func updatesCmd(ctx context.Context, cfg *Config) error {
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return err
	}
	checked := 0
	for _, m := range mods {
		if m.Manifest == nil || m.Manifest.UpdateCheck == "" {
			continue
		}
		checked++
		u, newer, err := checkUpdate(ctx, m)
		switch {
		case err != nil:
			fmt.Printf("%s: %v\n", m.Dir, err)
		case newer:
			fmt.Printf("%s: %s -> %s\n", m.Dir, m.Manifest.Version, u.Version)
			printIndented(changelog(ctx, m, u))
		default:
			fmt.Printf("%s: %s is the latest\n", m.Dir, m.Manifest.Version)
		}
	}
	fmt.Printf("checked %d of %d mods (others have no update URL)\n", checked, len(mods))
	return nil
}

func printIndented(s string) {
	for line := range strings.SplitSeq(s, "\n") {
		fmt.Println("    " + line)
	}
}

func updateCmd(ctx context.Context, cfg *Config, names []string) error {
	for _, n := range names {
		m, err := loadMod(cfg, n)
		if err != nil {
			return err
		}
		if m.Manifest == nil || m.Manifest.UpdateCheck == "" {
			return fmt.Errorf("%s has no update URL", m.Dir)
		}
		u, newer, err := checkUpdate(ctx, m)
		if err != nil {
			return err
		}
		if !newer {
			fmt.Printf("%s: %s is the latest\n", m.Dir, m.Manifest.Version)
			continue
		}
		if err := installUpdate(ctx, cfg, m, u); err != nil {
			return err
		}
		fmt.Printf("%s: updated %s -> %s\n", m.Dir, m.Manifest.Version, u.Version)
		printIndented(changelog(ctx, m, u))
	}
	return nil
}

func move(cfg *Config, mod, position string) error {
	m, err := loadMod(cfg, mod)
	if err != nil {
		return err
	}
	pos, err := strconv.Atoi(position)
	if err != nil || pos < 1 {
		return fmt.Errorf("invalid position %q", position)
	}
	i := slices.Index(cfg.Enabled, m.Dir)
	if i < 0 {
		return fmt.Errorf("%s is not enabled", m.Dir)
	}
	cfg.Enabled = slices.Delete(cfg.Enabled, i, i+1)
	cfg.Enabled = slices.Insert(cfg.Enabled, min(pos-1, len(cfg.Enabled)), m.Dir)
	return cfg.save()
}

// listModsWithOptions prints mods that have options, shell-quoted for
// pasting into `h3mod option`.
func listModsWithOptions(cfg *Config) error {
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return err
	}
	for _, m := range mods {
		if m.Manifest == nil {
			continue
		}
		if n := len(m.Manifest.optionKeys()); n > 0 {
			state := ""
			if !cfg.isEnabled(m.Dir) {
				state = " (disabled)"
			}
			fmt.Printf("%-45s %2d options%s\n", shellQuote(m.Dir), n, state)
		}
	}
	return nil
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func listOptions(cfg *Config, args []string) error {
	if len(args) == 0 {
		return listModsWithOptions(cfg)
	}
	m, err := loadMod(cfg, args[0])
	if err != nil {
		return err
	}
	if m.Manifest == nil || len(m.Manifest.Options) == 0 {
		fmt.Printf("%s has no options\n", m.Title())
		return nil
	}
	sel, notes := cfg.selection(m)
	fmt.Printf("%s %s\n", m.Title(), m.Version())
	group := "\x00"
	for _, o := range m.Manifest.Options {
		if o.Type == "conditional" {
			continue
		}
		g := ""
		if o.Type == "select" {
			g = o.Group
		}
		if g != group {
			if g == "" {
				fmt.Println("  toggles:")
			} else {
				fmt.Printf("  choose one of %q:\n", g)
			}
			group = g
		}
		mark := "[ ]"
		if slices.Contains(sel, o.Key()) {
			mark = "[x]"
		}
		def := ""
		if o.EnabledByDefault {
			def = " (default)"
		}
		fmt.Printf("    %s %s%s\n", mark, o.Key(), def)
		if o.Tooltip != "" {
			fmt.Printf("        %s\n", o.Tooltip)
		}
	}
	for _, n := range notes {
		fmt.Println("note:", n)
	}
	return nil
}

func setOption(cfg *Config, args []string) error {
	if len(args) == 0 {
		return listModsWithOptions(cfg)
	}
	if len(args) == 1 {
		return listOptions(cfg, args)
	}
	m, err := loadMod(cfg, args[0])
	if err != nil {
		return err
	}
	on := true
	if len(args) == 3 {
		switch args[2] {
		case "on":
		case "off":
			on = false
		default:
			return fmt.Errorf("expected on or off, got %q", args[2])
		}
	}
	if err := cfg.setOption(m, args[1], on); err != nil {
		return err
	}
	return cfg.save()
}

func resetOptions(cfg *Config, mod string) error {
	m, err := loadMod(cfg, mod)
	if err != nil {
		return err
	}
	cfg.resetOptions(m)
	return cfg.save()
}

func importSMFCmd(cfg *Config, args []string) error {
	path := filepath.Join(cfg.abs(cfg.Game), "SMF", "config.json")
	if len(args) > 0 {
		path = args[0]
	}
	notes, err := importSMF(cfg, path)
	if err != nil {
		return err
	}
	for _, n := range notes {
		fmt.Println("note:", n)
	}
	fmt.Printf("imported %d enabled mods from %s\n", len(cfg.Enabled), path)
	return cfg.save()
}
