# h3mod

A blazing fast mod manager for HITMAN 3, written in Go. An alternative to SMF
(Simple Mod Framework) that installs and deploys the same mods, SMF framework
mods and plain rpkg mods, as they are: `h3mod` for the terminal, `h3mod-gui`
for everyone else.

- **Fast.** Deploys only rebuild what changed; entities and textures are
  built in parallel and cached.
- **SMF compatible.** Content folders, options (checkbox, select,
  conditional), load-order rules, every SMF content type, old QuickEntity
  entities and patches, Peacock integration. `import-smf` takes over SMF's
  load order and options.
- **Mod manager.** Install from zip/7z/rar or `.rpkg`, check for updates with
  release notes, enable, disable and reorder by number or part of a name. New
  mod folders are enabled automatically; deleted ones drop out.
- **Safe.** Base game archives are never written. Every file is replaced
  atomically, so an interrupted deploy leaves the game as it was, and
  `undeploy` restores vanilla.
- **Simple GUI.** Drop mod archives on the window, tick mods, pick options,
  press Deploy. It finds the game on its own.
- Linux and Windows, one download with all tools included.

## Install

### Prebuilt (Linux, Windows)

Download `h3mod-<version>-linux-x64.tar.gz` or `h3mod-<version>-windows-x64.zip`
from [releases](https://github.com/jim-ww/h3mod/releases) and unpack it into
the HITMAN 3 folder. The archive has `h3mod`, `h3mod-gui` and the tools and
hash lists they need (`tools/`, `data/`). Linux needs glibc 2.34+; on NixOS
use nix.

### Nix (preferred)

    nix run github:jim-ww/h3mod -- status
    nix run github:jim-ww/h3mod#gui
    nix profile install github:jim-ww/h3mod github:jim-ww/h3mod#gui

### Go

    go install github.com/jim-ww/h3mod@latest
    go install -tags gui github.com/jim-ww/h3mod@latest   # the GUI, also named h3mod

The GUI needs cgo with OpenGL and X11/Wayland headers (see
[Fyne](https://docs.fyne.io/started/)).

The binary alone can deploy mods that only use plain Go features. Entities,
`dlge.json`, `.delta` files and installing archives need these, in `tools/`
and `data/` next to the binary, in PATH, or in the given variables:

| Needed for | Tools and data |
|---|---|
| `entity.json`, `entity.patch.json`, `rtlv.json` | ResourceTool, quickentity-rs (3.1, rev 010765c) |
| entity patches | `hash_list.txt` (Hitman-Hashes v381, `H3MOD_PATH_LIST`) |
| `dlge.json` | `hash_list.hmla` (Hitman-l10n-Hashes v102, `H3MOD_HASH_LIST`) |
| QuickEntity 3.0 files | `quickentity-3` (quickentity-rs rev 30f394e) |
| QuickEntity before 3.0 | SMF's QuickEntity scripts and npm packages (`H3MOD_LEGACY_QN`) |
| `.delta` files | xdelta3 |
| `install` from archives | 7-Zip (`7zz` or `7z`) |

The easiest way to get them is `tools/` and `data/` from a release archive.

## Usage

### GUI

Start `h3mod-gui` (`h3mod-gui.exe` on Windows). Install mods by dropping their
archives on the window or with **Install mod…**, tick the ones you want,
select a mod to pick its options, then press **Deploy**. Drag mods up or down
to change the load order. Each mod shows whether it's deployed, and the line
above **Deploy** says what the next deploy would change. **Restore vanilla** removes all mods from the game.
Mods are kept in `mods/` next to the program.

    h3mod-gui [--config h3mod.json] [--game dir] [--mods dir]

`--game` and `--mods` set the folders and save them to the config. Given a
command, `h3mod-gui` works like `h3mod` (`h3mod-gui deploy`, `h3mod-gui help`),
with the same config as its window. On Windows the output then appears after
the prompt, since the shell doesn't wait for GUI programs; `start /wait
h3mod-gui deploy` waits.

### Terminal

From the folder the release was unpacked to (inside the HITMAN 3 folder):

    ./h3mod install ~/Downloads/SomeMod.zip   # into mods/, enabled
    ./h3mod                                   # status: mods, options, problems
    ./h3mod options "Some Mod"                # list its options
    ./h3mod option "Some Mod" "Color:Red"     # pick one
    ./h3mod move "Some Mod" 1                 # load it first (later wins)
    ./h3mod deploy                            # apply to the game
    ./h3mod undeploy                          # back to vanilla

On Windows run `.\h3mod.exe` the same way.

On the first run, both ask whether to use SMF's mods folder (with its load
order and options) when the game folder has one. Later, set `"mods"` in
`h3mod.json` and run `h3mod import-smf`, or use **Mods: Change…** in the GUI.

Errors name the mod and the file in it. Runs that change something are logged
to `h3mod.log` next to the config.

## Commands

    status                         what's deployed, mods in load order, options, problems (also with no command)
    enable <mod>... | all / disable <mod>... | all
    move <mod> <position>
    install [--name n] [--replace] <archive|rpkg>
    remove <mod>... [--yes]        delete mods from the mods folder (GUI: Delete mod…)
    updates                        check mods' update URLs, with release notes, and offer to install
    update <mod>...                download and install new versions
    options <mod>                  list options and what is selected
    option <mod> <option> [on|off] select ("Group:Name" for choices)
    reset-options <mod>
    import-smf [config.json]       take load order and options from SMF
    deploy [--skip-unsupported] [--skip-intro=false] [-o dir]
    undeploy
    help [command], version, completion <shell>

`--config`/`-c` picks the config; `h3mod <command> --help` explains a command.

`<mod>` is a number from `status`, a folder name, a mod ID, or any part of
the name only one mod has. On a first run every mod is enabled.

Enabling, disabling, reordering and options only change what the next deploy
does. `status` shows both: when the last deploy was and, per mod, whether it's
deployed, not deployed yet, changed since, or removed on the next deploy.

`install` takes what SMF's Mod Manager does: archives of framework mod
folders, or `.rpkg` files that become an rpkg mod named by `-name`. Archives
are checked (manifests, content folders, no links) before they're moved into
the mods folder. `update` installs into the mod's existing folder, keeping its
place in the load order and its options.

Before each deploy the load order is sorted by the mods' `loadBefore` and
`loadAfter` rules, keeping the current order where the rules allow. Option
selections are saved with the mod version: when a mod updates, removed options
are dropped, new ones get their defaults, and each change is reported.

## Completion

Tab completes commands, mod names, option names, load order positions and
files. The nix packages install it for bash, zsh and fish. From a release
archive, load the script from `completions/`:

    source completions/h3mod.bash                 # bash, e.g. in ~/.bashrc
    cp completions/h3mod.zsh ~/.zfunc/_h3mod      # zsh, with ~/.zfunc in fpath
    cp completions/h3mod.fish ~/.config/fish/completions/
    . .\completions\h3mod.ps1                     # PowerShell, e.g. in $PROFILE

Completion runs `h3mod`, so its folder needs to be in PATH. `h3mod-gui` has
the same scripts under its own name.

## Config

`h3mod.json` in the current directory (`--config` to change); the GUI uses the
one next to it, so both share it when run from the release folder. Without
one, these defaults apply; relative paths are relative to the config.

    {
      "game": "..",               // HITMAN 3 folder
      "mods": "mods",             // folder of unpacked mods, e.g. "../SMF/Mods"
      "enabled": ["Mod A", ...],  // folder names, in load order; later wins
      "options": {...},           // chosen options per mod
      "skipIntro": false,         // the intro is skipped unless this is false
      "checkUpdates": true,       // check mods for updates in status and on GUI start (off by default)
      "deploySummary": "...",     // see Peacock below; "off" to disable
      "known": [...]              // mod folders seen so far (managed by h3mod)
    }

## Supported

- framework mods: content folders, checkbox, select and conditional options,
  requirements, incompatibilities, supported platforms, load-order rules
- raw files with `.meta` or `.meta.json`, rpkg mods
- `entity.json` and `entity.patch.json` of any QuickEntity version; older ones
  are upgraded into the cache like SMF does, mod files are never changed
- `repository.json`, `material.json`, `texture.tga`, `.delta`, `sfx.wem`
- `unlockables.json`, `contract.json`, `JSON.patch.json`, blobs folders,
  contracts in the Destinations menu
- `dlge.json`, `locr.json`, `ditl.json`, `clng.json`, `rtlv.json`, manifest
  localisation and localised lines
- manifest `dependencies`, `packagedefinition`, `thumbs`, and the standard
  ModJPFiles script, including its `Retail/mods` files (h3mod removes only the
  files it placed)
- Peacock: modded unlockables and contracts, `peacockPlugins`

Other mod scripts are reported by `status` and `deploy`.

## Peacock

Like SMF, a deploy writes a summary that Peacock reads to serve modded
unlockables and contracts and to run mods' plugins: in
`%LOCALAPPDATA%\Simple Mod Framework\` on Windows and
`$XDG_DATA_HOME/app.simple-mod-framework/` on Linux. If Peacock runs under
Wine, set `"deploySummary"` to
`<prefix>/drive_c/users/<user>/AppData/Local/Simple Mod Framework/deploySummary.json`
or point Peacock's `frameworkDeploySummaryPath` at the file. h3mod only
replaces or removes summaries it wrote itself.

## Files

`cache/` holds built entities, textures, deltas and RTLVs; `state.json` the
deployed archives, so unchanged ones aren't rewritten; `h3mod.log` the last
runs (moved to `h3mod.log.1` at 1 MB). Both can be deleted at
any time. `clean/` keeps the vanilla packagedefinition and thumbs.dat and
must be kept.

## Building

    make build       # ./h3mod for this system
    make gui         # h3mod-gui, via nix
    make test        # in the nix dev shell

`nix develop` (or direnv) gives a shell with all tools for `go run .`.

## Support the project

If h3mod saves you time, consider a small donation.

**Monero (XMR)**

`83YGRqP8uHed6NeegZQeX9ccCxbzoRHHEEi7pTwk4aqdJZEVXXA6NWtetnsEM2v33zFBBt3Rp6DNhU9qhJEGPspU14yN8t7`

## License

GPL-3.0. Free to use, study, share, and modify — provided you keep the same freedoms for others.
