// Command h3mod deploys HITMAN 3 mods from a folder of unpacked mods,
// compatible with SMF (Simple Mod Framework) mods.
//
// Base game archives are never modified: output goes to chunkNpatch300.rpkg
// plus packagedefinition.txt, each replaced atomically.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

const usage = `usage: h3mod [-config h3mod.json] <command> [args]

mods:
  status                       list enabled and disabled mods, options, problems
  enable <mod>... | all        enable mods (appended to the load order)
  disable <mod>... | all       disable mods
  move <mod> <position>        move a mod in the load order (later wins)
  install [-name n] [-replace] <archive|rpkg>
                               add mods from a zip/7z/rar or .rpkg to the mods dir
  updates                      check mods' update URLs for new versions
  update <mod>...              download and install new versions

options:
  options [mod]                mods with options, or one mod's options
  option <mod> <option> [on|off]
                               select an option ("Group:Name" for choices)
  reset-options <mod>          go back to the mod's defaults

deploying:
  deploy [-skip-unsupported] [-out dir]
  undeploy                     restore vanilla game files
  import-smf [config.json]     take load order and options from SMF

<mod> is a number from status, a folder name, a mod ID, or a unique part of
a mod's name. New folders in the mods directory are enabled automatically;
deleted ones drop out of the load order.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("h3mod", flag.ContinueOnError)
	fl.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	cfgPath := fl.String("config", "h3mod.json", "config file")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() == 0 {
		fl.Usage()
		return errors.New("no command")
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	cmd, rest := fl.Arg(0), fl.Args()[1:]
	if cmd == "help" {
		fmt.Print(usage)
		return nil
	}
	if cmd == "install" {
		if err := os.MkdirAll(cfg.abs(cfg.Mods), 0o755); err != nil {
			return err
		}
	}
	if err := syncMods(cfg); err != nil {
		return err
	}
	switch cmd {
	case "status":
		return status(cfg)
	case "enable", "disable":
		return toggle(cfg, cmd == "enable", rest)
	case "move":
		return move(cfg, rest)
	case "install":
		return installCmd(ctx, cfg, rest)
	case "updates":
		return updatesCmd(ctx, cfg)
	case "update":
		return updateCmd(ctx, cfg, rest)
	case "options":
		return listOptions(cfg, rest)
	case "option":
		return setOption(cfg, rest)
	case "reset-options":
		return resetOptions(cfg, rest)
	case "import-smf":
		return importSMFCmd(cfg, rest)
	case "deploy":
		dfl := flag.NewFlagSet("deploy", flag.ContinueOnError)
		skip := dfl.Bool("skip-unsupported", false, "deploy without features h3mod doesn't support")
		out := dfl.String("out", "", "write output to this directory instead of the game")
		if err := dfl.Parse(rest); err != nil {
			return err
		}
		return newDeployer(cfg).Deploy(ctx, *skip, *out)
	case "undeploy":
		return newDeployer(cfg).Undeploy()
	}
	fl.Usage()
	return fmt.Errorf("unknown command %q", cmd)
}

// syncMods updates the config from the mods directory and says what changed.
func syncMods(cfg *Config) error {
	_, notes, err := cfg.syncMods()
	for _, n := range notes {
		fmt.Println("note:", n)
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
	fmt.Println("enabled, in load order (later wins):")
	for i, m := range listed(cfg, mods) {
		if i == len(cfg.Enabled) {
			fmt.Println("disabled:")
		}
		fmt.Printf("  %2d. %s (%s)\n", i+1, m.Title(), m.Dir)
		if sel, _ := cfg.selection(m); len(sel) > 0 && cfg.isEnabled(m.Dir) {
			fmt.Printf("      options: %s\n", strings.Join(sel, ", "))
		}
	}
	fmt.Println("mods can be named by number, folder, ID or a unique part of the name")
	p, err := buildPlan(cfg, newGameData(cfg), false)
	if err != nil {
		return err
	}
	for _, n := range p.Notes {
		fmt.Println("note:", n)
	}
	if len(p.Skipped) > 0 {
		fmt.Println("unsupported in enabled mods:")
		printSkipped(p)
	}
	return nil
}

func toggle(cfg *Config, on bool, names []string) error {
	if len(names) == 0 {
		return errors.New("no mods given")
	}
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

func installCmd(ctx context.Context, cfg *Config, args []string) error {
	fl := flag.NewFlagSet("install", flag.ContinueOnError)
	name := fl.String("name", "", "folder name for an rpkg mod (default: the file's name)")
	replace := fl.Bool("replace", false, "replace mods that are already installed")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() != 1 {
		return errors.New("usage: install [-name n] [-replace] <archive|rpkg>")
	}
	src := fl.Arg(0)
	if *name == "" {
		*name = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	dirs, err := installMods(ctx, cfg, src, *name, *replace, "", "")
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
			for line := range strings.SplitSeq(strings.TrimSpace(u.Changelog), "\n") {
				fmt.Println("    " + line)
			}
		}
	}
	fmt.Printf("checked %d of %d mods (others have no update URL)\n", checked, len(mods))
	return nil
}

func updateCmd(ctx context.Context, cfg *Config, names []string) error {
	if len(names) == 0 {
		return errors.New("usage: update <mod>...")
	}
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
		work := cfg.abs(filepath.Join("cache", "work"))
		if err := os.MkdirAll(work, 0o755); err != nil {
			return err
		}
		file, err := download(ctx, u.URL, work)
		if err != nil {
			return err
		}
		_, err = installMods(ctx, cfg, file, "", true, m.Dir, m.ID())
		os.Remove(file)
		if err != nil {
			return fmt.Errorf("%s: %w", m.Dir, err)
		}
		fmt.Printf("%s: updated %s -> %s\n", m.Dir, m.Manifest.Version, u.Version)
	}
	return nil
}

func move(cfg *Config, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: move <mod> <position>")
	}
	m, err := loadMod(cfg, args[0])
	if err != nil {
		return err
	}
	pos, err := strconv.Atoi(args[1])
	if err != nil || pos < 1 {
		return fmt.Errorf("invalid position %q", args[1])
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
	if len(args) != 1 {
		return errors.New("usage: options [mod]")
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
	if len(args) > 3 {
		return errors.New("usage: option <mod> <option> [on|off]")
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

func resetOptions(cfg *Config, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: reset-options <mod>")
	}
	m, err := loadMod(cfg, args[0])
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
