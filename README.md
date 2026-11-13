# h3mod

Deploys HITMAN 3 mods from a folder of unpacked mods. Reads SMF (Simple Mod
Framework) mods and plain rpkg mods as they are.

Base game archives are never written. A deploy writes `chunkNpatch300.rpkg`
archives and `packagedefinition.txt`, each to a temp file first and renamed
into place, with packagedefinition last. An interrupted deploy leaves the
game as it was; the next deploy cleans up. `undeploy` restores vanilla.

## Running

The external tools (ResourceTool, quickentity-rs) and the hash list come from
the flake:

    nix run . -- status            # packaged binary
    nix develop -c go run . status # from source (or direnv with .envrc)

Config is `h3mod.json` in the current directory (`-config` to change):

    {
      "game": "..",               // HITMAN 3 folder
      "mods": "../SMF/Mods",      // folder of unpacked mods
      "enabled": ["Mod A", ...],  // folder names, in load order; later wins
      "options": {...}            // chosen options per mod
    }

## Commands

    status                         mods in load order, options, problems
    enable <mod>... / disable <mod>...
    move <mod> <position>
    options <mod>                  list options and what is selected
    option <mod> <option> [on|off] select ("Group:Name" for choices)
    reset-options <mod>
    import-smf [config.json]       take load order and options from SMF
    deploy [-skip-unsupported] [-out dir]
    undeploy

`<mod>` is a folder name or a mod ID.

Option selections are saved with the mod version and the options the mod had.
When a mod updates, removed options are dropped, new ones get their defaults,
and empty choice groups fall back to theirs; each change is reported.

## Supported

- framework mods: content folders, checkbox and select options
- raw files with `.meta` or `.meta.json`, rpkg mods
- `repository.json`, `entity.json`, `entity.patch.json`, `dlge.json`,
  `material.json`, `texture.tga`
- manifest `dependencies`, and the standard ModJPFiles script

Anything else is reported by `status` and `deploy`.

## Caches

`cache/` holds built entities and textures, keyed by their inputs;
`state.json` records deployed archives so unchanged ones are not rewritten.
Both can be deleted at any time. `clean/` keeps the vanilla
packagedefinition and must be kept.
