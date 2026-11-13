// Command h3mod deploys HITMAN 3 mods from a folder of unpacked mods.
//
// Base game archives are never modified: output goes to chunkNpatch300.rpkg
// plus packagedefinition.txt, both replaced atomically.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
)

const usage = `usage: h3mod [-config h3mod.json] <command>

commands:
  status                  list mods and what would be deployed
  enable <mod>...         enable mods (folder names), appended to load order
  disable <mod>...        disable mods
  deploy [-skip-unsupported] [-out dir]
  undeploy                restore vanilla game files
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
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
	case "deploy":
		dfl := flag.NewFlagSet("deploy", flag.ContinueOnError)
		skip := dfl.Bool("skip-unsupported", false, "deploy without features h3mod doesn't support")
		out := dfl.String("out", "", "write output to this directory instead of the game")
		if err := dfl.Parse(rest); err != nil {
			return err
		}
		return deploy(cfg, *skip, *out)
	case "undeploy":
		return undeploy(cfg)
	}
	fl.Usage()
	return fmt.Errorf("unknown command %q", cmd)
}

func status(cfg *Config) error {
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return err
	}
	byDir := map[string]*Mod{}
	for _, m := range mods {
		byDir[m.Dir] = m
	}
	fmt.Println("enabled, in load order:")
	for i, dir := range cfg.Enabled {
		if m := byDir[dir]; m != nil {
			fmt.Printf("  %2d. %s (%s)\n", i+1, m.Title(), dir)
		} else {
			fmt.Printf("  %2d. %s (missing)\n", i+1, dir)
		}
	}
	fmt.Println("disabled:")
	for _, m := range mods {
		if !cfg.isEnabled(m.Dir) {
			fmt.Printf("      %s (%s)\n", m.Title(), m.Dir)
		}
	}
	p, err := buildPlan(cfg, false)
	if err != nil {
		return err
	}
	if len(p.Unsupported) > 0 {
		fmt.Println("unsupported in enabled mods:")
		printUnsupported(p)
	}
	return nil
}

func toggle(cfg *Config, on bool, dirs []string) error {
	if len(dirs) == 0 {
		return errors.New("no mods given")
	}
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if on {
			if !slices.ContainsFunc(mods, func(m *Mod) bool { return m.Dir == d }) {
				return fmt.Errorf("no mod folder %q", d)
			}
			if !cfg.isEnabled(d) {
				cfg.Enabled = append(cfg.Enabled, d)
			}
		} else {
			cfg.Enabled = slices.DeleteFunc(cfg.Enabled, func(e string) bool { return e == d })
		}
	}
	return cfg.save()
}
