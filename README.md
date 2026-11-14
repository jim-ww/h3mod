# h3mod

Deploys HITMAN 3 mods from a folder of unpacked mods. Reads SMF (Simple Mod
Framework) mods and plain rpkg mods as they are.

Base game archives are never written. A deploy writes `chunkNpatch300.rpkg`
archives, `packagedefinition.txt` and `thumbs.dat`, each to a temp file
first and renamed into place, archives first. An interrupted deploy leaves the
game as it was; the next deploy cleans up. `undeploy` restores vanilla.

## Running

### On other Linux systems

`nix build .#bundle` makes `result/h3mod-linux-x64.tar.gz`: the h3mod
binary with every tool and hash list it needs in `tools/` and `data/`. It
runs on any x86_64 Linux with glibc 2.34+ (ResourceTool is the upstream
build); on NixOS use the flake instead. Unpack it anywhere and, from a
folder holding `h3mod.json`:

    /path/to/h3mod-linux-x64/h3mod status

A plain `go build` binary has none of these tools and only works where they
are in PATH.

### With nix

The external tools (ResourceTool, quickentity-rs 3.1 and 3.0, xdelta3, 7-Zip),
SMF's legacy QuickEntity scripts and the hash lists
come from the flake:

    nix run . -- status            # packaged binary
    nix develop -c go run . status # from source (or direnv with .envrc)

Config is `h3mod.json` in the current directory (`-config` to change).
Without one, h3mod uses the defaults below: run it from a folder inside the
HITMAN 3 folder, next to `mods/`. Relative paths are relative to the config.

    {
      "game": "..",               // HITMAN 3 folder
      "mods": "mods",             // folder of unpacked mods, e.g. "../SMF/Mods"
      "enabled": ["Mod A", ...],  // folder names, in load order; later wins
      "options": {...},           // chosen options per mod
      "skipIntro": false,         // the intro is skipped unless this is false
      "deploySummary": "...",     // see Peacock below; "off" to disable
      "known": [...]              // mod folders seen so far (managed by h3mod)
    }

## Commands

    status                         mods in load order, options, problems (also with no command)
    enable <mod>... | all / disable <mod>... | all
    move <mod> <position>
    install [-name n] [-replace] <archive|rpkg>
    updates                        check mods' update URLs, with release notes
    update <mod>...                download and install new versions
    options <mod>                  list options and what is selected
    option <mod> <option> [on|off] select ("Group:Name" for choices)
    reset-options <mod>
    import-smf [config.json]       take load order and options from SMF
    deploy [-skip-unsupported] [-skip-intro=false] [-out dir]
    undeploy

`<mod>` is a number from `status`, a folder name, a mod ID, or any part of
the name only one mod has; `enable all`/`disable all` work too. New folders in
the mods directory are enabled automatically (at the end of the load order);
deleted ones drop out of it. On a first run every mod is enabled.

`install` takes what SMF's Mod Manager does: archives of framework mod
folders, or `.rpkg` files (loose or archived) that become an rpkg mod named
by `-name`. Archives are unpacked with 7-Zip into a hidden folder in the mods
directory, checked (manifests, content folders, no links) and moved into
place. `update` installs into the mod's existing folder, keeping its place
in the load order and its options.

Before each deploy the load order is sorted by the mods' `loadBefore` and
`loadAfter` rules, keeping the current order where the rules allow, and saved.

Option selections are saved with the mod version and the options the mod had.
When a mod updates, removed options are dropped, new ones get their defaults,
and empty choice groups fall back to theirs; each change is reported.

## Supported

- framework mods: content folders, checkbox and select options, conditional
  options, requirements, incompatibilities, supported platforms and load
  order rules
- raw files with `.meta` or `.meta.json`, rpkg mods
- `entity.json` and `entity.patch.json` of any QuickEntity version: older
  ones (before 3.1 / patch version 6) are upgraded into the cache like SMF
  does, with SMF's JavaScript QuickEntity 1.136–2.1 (run by an embedded
  JavaScript engine) and QuickEntity 3.0; mod files are never changed
- `repository.json`, `material.json`,
  `texture.tga`, `.delta`, `sfx.wem`
- `unlockables.json`, `contract.json`, `JSON.patch.json`, blobs folders
- `dlge.json`, `locr.json`, `ditl.json`, `clng.json`, `rtlv.json`, manifest
  localisation and localised lines
- manifest `dependencies`, `packagedefinition`, `thumbs`, and the standard
  ModJPFiles script, including its files for `Retail/mods` (h3mod tracks
  the files it places there and removes only those)
- skipping the intro, on by default: `"skipIntro": false` in the config or
  `deploy -skip-intro=false` keeps it

- contracts added to the Destinations menu (`SMF.destinations`)
- Peacock integration: modded unlockables and contracts, and `peacockPlugins`

Anything else (mod scripts other than ModJPFiles) is reported by `status` and
`deploy`.

## Peacock

Like SMF, a deploy writes a deploy summary that Peacock reads to serve modded
unlockables and contracts and to run mods' plugins. By default it goes where
Peacock on Linux looks: `$XDG_DATA_HOME/app.simple-mod-framework/deploySummary.json`.
If Peacock runs under Wine, either set `"deploySummary"` to
`<prefix>/drive_c/users/<user>/AppData/Local/Simple Mod Framework/deploySummary.json`,
or point Peacock's `frameworkDeploySummaryPath` option at the file. h3mod
only replaces or removes summaries it wrote itself; `undeploy` removes it.

## Caches

`cache/` holds built entities, textures, deltas and RTLVs, keyed by their
inputs;
`state.json` records deployed archives so unchanged ones are not rewritten.
Both can be deleted at any time. `clean/` keeps the vanilla
packagedefinition and thumbs.dat and must be kept.
