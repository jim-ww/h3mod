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

      quickentityArgs = {
        pname = "quickentity-rs";
        version = "1.0.0-010765c";
        src = quickentity-rs;
        cargoLock.lockFile = "${quickentity-rs}/Cargo.lock";
        cargoBuildFlags = [ "--bin" "quickentity-rs" ];
        doCheck = false;
        # Upstream builds with nightly: allow its #![feature]s on stable, and
        # replace auto_context, which uses a since-changed nightly API.
        RUSTC_BOOTSTRAP = 1;
        # Make the GUI's dependencies optional (the lock file stays valid), so
        # the CLI builds without gtk and statically.
        postPatch = ''
          cp ${./nix/auto_context.rs} auto_context/src/lib.rs
          sed -i Cargo.toml \
            -e 's/^\(egui\|eframe\|rfd\) = "\([^"]*\)"/\1 = { version = "\2", optional = true }/' \
            -e 's/^\(egui_extras\|image\) = { \(.*\) }/\1 = { \2, optional = true }/' \
            -e 's|^path = "src/gui.rs"|&\nrequired-features = ["gui"]|'
          printf '\n[features]\ngui = ["egui", "egui_extras", "image", "eframe", "rfd"]\n' >> Cargo.toml
        '';
      };

      quickentity = pkgs.rustPlatform.buildRustPackage quickentityArgs;

      # static musl build for the portable bundle
      quickentity-static = pkgs.pkgsStatic.rustPlatform.buildRustPackage quickentityArgs;

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

      # official static 7-Zip build for the bundle
      sevenzip-static = pkgs.runCommand "7zzs-25.01" { } ''
        mkdir -p $out/bin
        tar xJf ${pkgs.fetchurl {
          url = "https://www.7-zip.org/a/7z2501-linux-x64.tar.xz";
          hash = "sha256-TKO3xvL2eGa5JiKBi1gjPccDZ74vNrSY6wveqqRLU/Q=";
        }} 7zzs License.txt
        install -m755 7zzs $out/bin/7zz
        cp License.txt $out/7-Zip-License.txt
      '';

      # static, so the same binary runs on any x86_64 Linux
      h3mod-bin = pkgs.buildGoModule {
        pname = "h3mod";
        version = "0.1.0";
        src = self;
        vendorHash = "sha256-xdOlSdWE/U++ZDhd77WaGiWdz0Nb8KcRBChKU2kLbEA=";
        env.CGO_ENABLED = 0;
        ldflags = [ "-s" "-w" ];
        doCheck = false;
      };
    in
    {
      packages.${system} = {
        inherit quickentity quickentity-static resourcetool;
        default = pkgs.runCommand "h3mod-0.1.0" { nativeBuildInputs = [ pkgs.makeWrapper ]; } ''
          makeWrapper ${h3mod-bin}/bin/h3mod $out/bin/h3mod \
            --prefix PATH : ${pkgs.lib.makeBinPath [ quickentity resourcetool pkgs.xdelta pkgs._7zz ]} \
            --set-default H3MOD_HASH_LIST ${hash-list} \
            --set-default H3MOD_PATH_LIST ${path-list}/hash_list.txt
        '';

        # Portable tarball for other Linux systems: h3mod finds tools/ and
        # data/ next to itself. ResourceTool is the upstream build (glibc
        # 2.34+, libstdc++); everything else is static.
        bundle = pkgs.runCommand "h3mod-bundle-0.1.0" { } ''
          d=h3mod-linux-x64
          mkdir -p $d/tools $d/data $out
          cp ${h3mod-bin}/bin/h3mod $d/
          cp ${quickentity-static}/bin/quickentity-rs ${pkgs.pkgsStatic.xdelta}/bin/xdelta3 \
            ${sevenzip-static}/bin/7zz ${resourcetool-bin}/ResourceTool ${resourcetool-bin}/*.so $d/tools/
          cp ${resourcetool-bin}/LICENSE $d/tools/ResourceTool-LICENSE
          cp ${sevenzip-static}/7-Zip-License.txt $d/tools/
          cp ${hash-list} $d/data/hash_list.hmla
          gzip -9 < ${path-list}/hash_list.txt > $d/data/hash_list.txt.gz
          cp ${./README.md} $d/README.md
          chmod -R u+w $d
          chmod +x $d/h3mod $d/tools/{quickentity-rs,xdelta3,7zz,ResourceTool}
          tar czf $out/$d.tar.gz $d
        '';
      };

      devShells.${system}.default = pkgs.mkShell {
        packages = [ pkgs.go quickentity resourcetool pkgs.xdelta pkgs._7zz ];
        H3MOD_HASH_LIST = "${hash-list}";
        H3MOD_PATH_LIST = "${path-list}/hash_list.txt";
        # h3mod needs no cgo, and without it go never calls gcc, whose flags
        # from the shell break on the space in "HITMAN 3"
        CGO_ENABLED = "0";
      };
    };
}
