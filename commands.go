package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

// version is set at build time from VERSION.
var version = "dev"

// defaultConfig is the config used without --config, see configPath.
var defaultConfig = configPath()

// cliMain runs a command line; h3mod-gui uses it too when given a command.
func cliMain(name string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCmd(name).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		logLine("error: %v", err)
		os.Exit(1)
	}
	logLine("done")
}

// hasCommand reports whether args name a command rather than only flags,
// which tells h3mod-gui to act like the CLI.
func hasCommand(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := strings.TrimLeft(args[i], "-")
		switch {
		case !strings.HasPrefix(args[i], "-"):
			return true
		case a == "config" || a == "c" || a == "game" || a == "mods":
			i++ // the flag's value
		}
	}
	return false
}

// cli holds what commands share: the config, loaded before each command.
type cli struct {
	cfgPath string
	cfg     *Config
}

// Commands without the config setup (it may ask about SMF and saves), and
// commands whose runs aren't worth logging.
var (
	noSetup  = []string{"help", "version", "completion", "bash", "zsh", "fish", "powershell", "__complete", "__completeNoDesc"}
	readOnly = []string{"status", "options", "updates"}
)

// setup loads the config and brings it up to date with the mods folder.
func (c *cli) setup(cmd *cobra.Command, args []string) error {
	if slices.Contains(noSetup, cmd.Name()) {
		return nil
	}
	cfg, err := loadConfig(c.cfgPath)
	if err != nil {
		return err
	}
	c.cfg = cfg
	if !slices.Contains(readOnly, cmd.Name()) && cmd.HasParent() {
		openRunLog(cfg, fmt.Sprintf("%s %s: %s", cmd.Root().Name(), version, strings.Join(os.Args[1:], " ")))
	}
	if cmd.Name() == "install" {
		if err := os.MkdirAll(cfg.abs(cfg.Mods), 0o755); err != nil {
			return err
		}
	}
	if err := firstRun(cfg); err != nil {
		return err
	}
	return syncMods(cfg)
}

func newRootCmd(name string) *cobra.Command {
	c := &cli{}
	root := &cobra.Command{
		Use:   name,
		Short: "Mod manager for HITMAN 3, compatible with SMF mods",
		Long: `Mod manager for HITMAN 3, compatible with SMF (Simple Mod Framework) mods.

Without a command it shows status. h3mod-gui takes the same commands and
opens its window without one.

<mod> is a number from status, a folder name, a mod ID, or a unique part of a
mod's name. New folders in the mods folder are enabled automatically; deleted
ones drop out of the load order.`,
		Version:           version,
		Args:              cobra.NoArgs,
		SilenceUsage:      true,
		SilenceErrors:     true,
		PersistentPreRunE: c.setup,
		RunE:              func(*cobra.Command, []string) error { return status(c.cfg) },
	}
	root.PersistentFlags().StringVarP(&c.cfgPath, "config", "c", defaultConfig, "config file")
	root.RegisterFlagCompletionFunc("config", fileExt("json"))
	root.AddGroup(&cobra.Group{ID: "mods", Title: "Mods:"}, &cobra.Group{ID: "options", Title: "Options:"}, &cobra.Group{ID: "deploy", Title: "Deploying:"})
	add := func(group string, cmd *cobra.Command) { cmd.GroupID = group; root.AddCommand(cmd) }

	add("mods", &cobra.Command{
		Use:   "status",
		Short: "List enabled and disabled mods, options and problems",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return status(c.cfg) },
	})
	add("mods", &cobra.Command{
		Use:               "enable <mod>... | all",
		Short:             "Enable mods (appended to the load order)",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: c.completeMods(func(cfg *Config, m *Mod) bool { return !cfg.isEnabled(m.Dir) }, "all"),
		RunE:              func(_ *cobra.Command, args []string) error { return toggle(c.cfg, true, args) },
	})
	add("mods", &cobra.Command{
		Use:               "disable <mod>... | all",
		Short:             "Disable mods",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: c.completeMods(func(cfg *Config, m *Mod) bool { return cfg.isEnabled(m.Dir) }, "all"),
		RunE:              func(_ *cobra.Command, args []string) error { return toggle(c.cfg, false, args) },
	})
	add("mods", &cobra.Command{
		Use:   "move <mod> <position>",
		Short: "Move a mod in the load order (later wins)",
		Args:  cobra.ExactArgs(2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, s string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return c.completeMods(func(cfg *Config, m *Mod) bool { return cfg.isEnabled(m.Dir) })(cmd, args, s)
			}
			cfg, err := loadConfig(c.cfgPath)
			if len(args) > 1 || err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var out []cobra.Completion
			for i, d := range cfg.Enabled {
				out = append(out, cobra.CompletionWithDesc(strconv.Itoa(i+1), "now "+d))
			}
			return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
		},
		RunE: func(_ *cobra.Command, args []string) error { return move(c.cfg, args[0], args[1]) },
	})
	var rpkgName string
	var replace bool
	install := &cobra.Command{
		Use:               "install <archive|rpkg>",
		Short:             "Add mods from a zip/7z/rar or .rpkg to the mods folder",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: fileExt("zip", "7z", "rar", "rpkg"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return installCmd(cmd.Context(), c.cfg, args[0], rpkgName, replace)
		},
	}
	install.Flags().StringVarP(&rpkgName, "name", "n", "", "folder name for an rpkg mod (default: the file's name)")
	install.Flags().BoolVar(&replace, "replace", false, "replace mods that are already installed")
	add("mods", install)
	var yes bool
	remove := &cobra.Command{
		Use:               "remove <mod>...",
		Aliases:           []string{"uninstall", "rm"},
		Short:             "Delete mods from the mods folder (deploy afterwards to take them out of the game)",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: c.completeMods(func(*Config, *Mod) bool { return true }),
		RunE:              func(_ *cobra.Command, args []string) error { return removeCmd(c.cfg, args, yes) },
	}
	remove.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	add("mods", remove)
	add("mods", &cobra.Command{
		Use:   "updates",
		Short: "Check mods' update URLs for new versions, with release notes",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return updatesCmd(cmd.Context(), c.cfg) },
	})
	add("mods", &cobra.Command{
		Use:   "update <mod>...",
		Short: "Download and install new versions",
		Args:  cobra.MinimumNArgs(1),
		ValidArgsFunction: c.completeMods(func(_ *Config, m *Mod) bool {
			return m.Manifest != nil && m.Manifest.UpdateCheck != ""
		}),
		RunE: func(cmd *cobra.Command, args []string) error { return updateCmd(cmd.Context(), c.cfg, args) },
	})
	var smfDir string
	var useSMFMods bool
	importSMF := &cobra.Command{
		Use:               "import-smf [config.json]",
		Short:             "Take load order, options and the intro setting from SMF (again)",
		Long:              "Take load order, options and the intro setting from SMF's config.json, from SMF's folder (--smf, saved; default: SMF in the game folder) or the given file. When h3mod has no deploy recorded and the game has SMF's deploy, SMF's setup is recorded as deployed.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: fileExt("json"),
		RunE: func(_ *cobra.Command, args []string) error {
			return importSMFCmd(c.cfg, args, smfDir, useSMFMods)
		},
	}
	importSMF.Flags().StringVar(&smfDir, "smf", "", "SMF's folder; saved to the config")
	importSMF.Flags().BoolVar(&useSMFMods, "use-smf-mods", false, "also use SMF's mods folder")
	importSMF.MarkFlagDirname("smf")
	add("mods", importSMF)

	add("options", &cobra.Command{
		Use:               "options [mod]",
		Short:             "List mods with options, or one mod's options",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: c.completeMods(hasOptions),
		RunE:              func(_ *cobra.Command, args []string) error { return listOptions(c.cfg, args) },
	})
	add("options", &cobra.Command{
		Use:   "option <mod> <option> [on|off]",
		Short: `Select an option ("Group:Name" for choices)`,
		Args:  cobra.RangeArgs(0, 3),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, s string) ([]cobra.Completion, cobra.ShellCompDirective) {
			switch len(args) {
			case 0:
				return c.completeMods(hasOptions)(cmd, args, s)
			case 1:
				return c.completeOptions(args[0])
			case 2:
				return []cobra.Completion{"on", "off"}, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(_ *cobra.Command, args []string) error { return setOption(c.cfg, args) },
	})
	add("options", &cobra.Command{
		Use:               "reset-options <mod>",
		Short:             "Go back to the mod's default options",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: c.completeMods(hasOptions),
		RunE:              func(_ *cobra.Command, args []string) error { return resetOptions(c.cfg, args[0]) },
	})

	var skipUnsupported, skipIntro bool
	var out string
	deploy := &cobra.Command{
		Use:   "deploy",
		Short: "Build the enabled mods and install them into the game",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d := newDeployer(c.cfg)
			if cmd.Flags().Changed("skip-intro") {
				d.skipIntro = skipIntro
			}
			terminalProgress(d)
			return d.Deploy(cmd.Context(), skipUnsupported, out)
		},
	}
	deploy.Flags().BoolVar(&skipUnsupported, "skip-unsupported", false, "deploy without features h3mod doesn't support")
	deploy.Flags().BoolVar(&skipIntro, "skip-intro", true, `start at the main menu (default: the config's "skipIntro", on if unset)`)
	deploy.Flags().StringVarP(&out, "out", "o", "", "write output to this folder instead of the game")
	deploy.MarkFlagDirname("out")
	add("deploy", deploy)
	add("deploy", &cobra.Command{
		Use:   "undeploy",
		Short: "Restore vanilla game files",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return newDeployer(c.cfg).Undeploy() },
	})

	root.AddGroup(&cobra.Group{ID: "sdk", Title: "ZHMModSDK:"})
	sdk := &cobra.Command{
		Use:     "sdk",
		Short:   "Show ZHMModSDK and its mods",
		GroupID: "sdk",
		Args:    cobra.NoArgs,
		RunE:    func(*cobra.Command, []string) error { return sdkStatusCmd(c.cfg) },
	}
	completeSDKMods := func(_ *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		cfg, err := loadConfig(c.cfgPath)
		if err != nil || len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		if len(args) == 1 {
			return []cobra.Completion{"on", "off"}, cobra.ShellCompDirectiveNoFileComp
		}
		st, err := readSDKStatus(cfg)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var out []cobra.Completion
		for _, m := range st.Mods {
			if hasPrefixFold(m.Name, toComplete) {
				out = append(out, m.Name)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	sdk.AddCommand(
		&cobra.Command{
			Use:   "install",
			Short: "Install or update ZHMModSDK from its latest release",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				v, err := installSDK(cmd.Context(), c.cfg, func(f string, a ...any) { fmt.Printf(f+"\n", a...) })
				if err == nil {
					fmt.Println("installed ZHMModSDK", v)
				}
				return err
			},
		},
		&cobra.Command{
			Use:   "uninstall",
			Short: "Remove ZHMModSDK as h3mod installed it, restoring what it replaced",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				return uninstallSDK(c.cfg, func(f string, a ...any) { fmt.Printf(f+"\n", a...) })
			},
		},
		&cobra.Command{
			Use:   "enable",
			Short: "Turn ZHMModSDK's loader on",
			Args:  cobra.NoArgs,
			RunE:  func(*cobra.Command, []string) error { return setSDKLoader(c.cfg, true) },
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Turn ZHMModSDK's loader off, keeping it installed",
			Args:  cobra.NoArgs,
			RunE:  func(*cobra.Command, []string) error { return setSDKLoader(c.cfg, false) },
		},
		&cobra.Command{
			Use:               "mod <name> [on|off]",
			Short:             "Turn a ZHMModSDK mod on or off",
			Args:              cobra.RangeArgs(1, 2),
			ValidArgsFunction: completeSDKMods,
			RunE: func(_ *cobra.Command, args []string) error {
				on := len(args) == 1 || args[1] == "on"
				if len(args) == 2 && args[1] != "on" && args[1] != "off" {
					return fmt.Errorf("expected on or off, got %q", args[1])
				}
				return setSDKMod(c.cfg, args[0], on)
			},
		},
	)
	root.AddCommand(sdk)

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, _ []string) { fmt.Println(cmd.Root().Name(), version) },
	})
	return root
}

func hasOptions(_ *Config, m *Mod) bool {
	return m.Manifest != nil && len(m.Manifest.optionKeys()) > 0
}

// completeMods completes mod folder names, described by title, for mods
// keep accepts, plus extra words. It only reads the config, never saves.
func (c *cli) completeMods(keep func(*Config, *Mod) bool, extra ...string) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		cfg, err := loadConfig(c.cfgPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		mods, err := discoverMods(cfg.abs(cfg.Mods))
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var out []cobra.Completion
		for _, m := range listed(cfg, mods) {
			if keep(cfg, m) && !slices.Contains(args, m.Dir) && hasPrefixFold(m.Dir, toComplete) {
				out = append(out, cobra.CompletionWithDesc(m.Dir, m.Title()))
			}
		}
		for _, e := range extra {
			if hasPrefixFold(e, toComplete) {
				out = append(out, e)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
	}
}

// completeOptions completes a mod's option keys.
func (c *cli) completeOptions(mod string) ([]cobra.Completion, cobra.ShellCompDirective) {
	cfg, err := loadConfig(c.cfgPath)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	mods, err := discoverMods(cfg.abs(cfg.Mods))
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	m, err := resolveMod(cfg, mods, mod)
	if err != nil || m.Manifest == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, o := range m.Manifest.Options {
		if o.Type != "conditional" {
			out = append(out, cobra.CompletionWithDesc(o.Key(), o.Tooltip))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func fileExt(exts ...string) cobra.CompletionFunc {
	return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return exts, cobra.ShellCompDirectiveFilterFileExt
	}
}
