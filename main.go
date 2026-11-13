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
  status                       list mods in load order, options and problems
  enable <mod>...              enable mods (appended to the load order)
  disable <mod>...             disable mods
  move <mod> <position>        move a mod in the load order (later wins)

options:
  options <mod>                list a mod's options and which are selected
  option <mod> <option> [on|off]
                               select an option ("Group:Name" for choices)
  reset-options <mod>          go back to the mod's defaults

deploying:
  deploy [-skip-unsupported] [-out dir]
  undeploy                     restore vanilla game files
  import-smf [config.json]     take load order and options from SMF

<mod> is a folder name in the mods directory or a mod ID.
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
	switch cmd {
	case "status":
		return status(cfg)
	case "enable", "disable":
		return toggle(cfg, cmd == "enable", rest)
	case "move":
		return move(cfg, rest)
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

func loadMod(cfg *Config, name string) (*Mod, error) {
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return nil, err
	}
	if m := findMod(mods, name); m != nil {
		return m, nil
	}
	return nil, fmt.Errorf("no mod %q in %s", name, cfg.abs(cfg.Mods))
}

func status(cfg *Config) error {
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return err
	}
	fmt.Println("enabled, in load order (later wins):")
	for i, dir := range cfg.Enabled {
		m := findMod(mods, dir)
		if m == nil {
			fmt.Printf("  %2d. %s (missing)\n", i+1, dir)
			continue
		}
		fmt.Printf("  %2d. %s (%s)\n", i+1, m.Title(), dir)
		if sel, _ := cfg.selection(m); len(sel) > 0 {
			fmt.Printf("      options: %s\n", strings.Join(sel, ", "))
		}
	}
	fmt.Println("disabled:")
	for _, m := range mods {
		if !cfg.isEnabled(m.Dir) {
			fmt.Printf("      %s (%s)\n", m.Title(), m.Dir)
		}
	}
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
	for _, n := range names {
		m, err := loadMod(cfg, n)
		switch {
		case err != nil && on:
			return err
		case err != nil:
			// Allow disabling a folder that no longer exists.
			cfg.Enabled = slices.DeleteFunc(cfg.Enabled, func(e string) bool { return strings.EqualFold(e, n) })
		case on && !cfg.isEnabled(m.Dir):
			cfg.Enabled = append(cfg.Enabled, m.Dir)
		case !on:
			cfg.Enabled = slices.DeleteFunc(cfg.Enabled, func(e string) bool { return e == m.Dir })
		}
	}
	return cfg.save()
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

func listOptions(cfg *Config, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: options <mod>")
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
	if len(args) < 2 || len(args) > 3 {
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
