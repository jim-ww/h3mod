{
  description = "h3mod: HITMAN 3 mod deployer";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    # QuickEntity 3.1 CLI as SMF calls it (patch apply --permissive --format-fix):
    # the last revision before the CLI moved to hitman-commons
    quickentity-rs = {
      url = "github:atampy25/quickentity-rs/010765c77660ca1660a044b8b236d9ff537b380f";
      flake = false;
    };

    # ResourceTool: game binary <-> JSON for TEMP/TBLU (prebuilt Linux release)
    resourcetool-bin = {
      url = "https://github.com/OrfeasZ/ZHMTools/releases/download/v4.3.0/ResourceTool-linux-x64.zip";
      flake = false;
    };

    # sound tag and switch names for dlge.json, as TonyTools ships them
    hash-list = {
      url = "https://github.com/glacier-modding/Hitman-l10n-Hashes/releases/download/v102/hash_list.hmla";
      flake = false;
    };
  };

  outputs = { self, nixpkgs, quickentity-rs, resourcetool-bin, hash-list }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};

      quickentity = pkgs.rustPlatform.buildRustPackage {
        pname = "quickentity-rs";
        version = "1.0.0-010765c";
        src = quickentity-rs;
        cargoLock.lockFile = "${quickentity-rs}/Cargo.lock";
        cargoBuildFlags = [ "--bin" "quickentity-rs" ];
        doCheck = false;
        # Upstream builds with nightly: allow its #![feature]s on stable, and
        # replace auto_context, which uses a since-changed nightly API.
        RUSTC_BOOTSTRAP = 1;
        postPatch = ''
          cp ${./nix/auto_context.rs} auto_context/src/lib.rs
        '';
        # the GUI binary isn't built, but its dependencies still need these
        nativeBuildInputs = [ pkgs.pkg-config ];
        buildInputs = [ pkgs.gtk3 ];
      };

      resourcetool = pkgs.stdenv.mkDerivation {
        pname = "resourcetool";
        version = "4.3.0";
        src = resourcetool-bin;
        nativeBuildInputs = [ pkgs.autoPatchelfHook ];
        buildInputs = [ pkgs.stdenv.cc.cc.lib ];
        installPhase = ''
          runHook preInstall
          mkdir -p $out/bin
          find . -type f \( -name ResourceTool -o -name '*.so*' \) -exec cp {} $out/bin/ \;
          chmod +x $out/bin/ResourceTool
          runHook postInstall
        '';
      };

      # Resource paths by hash, version 381 like SMF ships. rpkg-tool names
      # entity references by path from this list, and mods' patches expect it.
      path-list = pkgs.runCommand "hitman-hash-list-381" { nativeBuildInputs = [ pkgs.p7zip ]; } ''
        7z e -o$out ${pkgs.fetchurl {
          url = "https://github.com/glacier-modding/Hitman-Hashes/releases/download/v381/latest-hashes.7z";
          hash = "sha256-95C268G6KjhHfHFRFqEsnGIuETn/6fZ6lDQHCDjgdrY=";
        }} hash_list.txt
      '';
    in
    {
      packages.${system} = {
        inherit quickentity resourcetool;
        default = pkgs.buildGoModule {
          pname = "h3mod";
          version = "0.1.0";
          src = self;
          vendorHash = "sha256-GPYWBOWaJorIX1E7X9zoWvHnfCghzqUb/UGKRD4mJzQ=";
          doCheck = false;
          nativeBuildInputs = [ pkgs.makeWrapper ];
          postInstall = ''
            wrapProgram $out/bin/h3mod \
              --prefix PATH : ${pkgs.lib.makeBinPath [ quickentity resourcetool pkgs.xdelta ]} \
              --set-default H3MOD_HASH_LIST ${hash-list} \
              --set-default H3MOD_PATH_LIST ${path-list}/hash_list.txt
          '';
        };
      };

      devShells.${system}.default = pkgs.mkShell {
        packages = [ pkgs.go quickentity resourcetool pkgs.xdelta ];
        H3MOD_HASH_LIST = "${hash-list}";
        H3MOD_PATH_LIST = "${path-list}/hash_list.txt";
      };
    };
}
