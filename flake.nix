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

    # QuickEntity 3.0 as SMF ships it (quickentity-3.exe, with --lossless) for
    # entity.json 3.0 and patch version 5: the last revision before 3.1
    quickentity-rs-3 = {
      url = "github:atampy25/quickentity-rs/30f394eed38ec6219232a524505ba565996c6326";
      flake = false;
    };

    # SMF's JavaScript QuickEntity 1.136, 2.0 and 2.1 for older entities and
    # patches, at the revision h3mod follows
    smf = {
      url = "github:atampy25/simple-mod-framework/c6d7ae2cad3cfa8d35e22e9b16fda1558b564952";
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

  outputs = { self, nixpkgs, quickentity-rs, quickentity-rs-3, smf, resourcetool-bin, hash-list }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};

      # Make QuickEntity's GUI dependencies optional (the lock file stays
      # valid), so the CLI builds without gtk and statically.
      noGui = ''
        sed -i Cargo.toml \
          -e 's/^\(egui\|eframe\|rfd\) = "\([^"]*\)"/\1 = { version = "\2", optional = true }/' \
          -e 's/^\(egui_extras\|image\) = { \(.*\) }/\1 = { \2, optional = true }/' \
          -e 's|^path = "src/gui.rs"|&\nrequired-features = ["gui"]|'
        printf '\n[features]\ngui = ["egui", "egui_extras", "image", "eframe", "rfd"]\n' >> Cargo.toml
      '';

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
        postPatch = ''
          cp ${./nix/auto_context.rs} auto_context/src/lib.rs
          ${noGui}
        '';
      };

      quickentity = pkgs.rustPlatform.buildRustPackage quickentityArgs;

      # static musl build for the portable bundle
      quickentity-static = pkgs.pkgsStatic.rustPlatform.buildRustPackage quickentityArgs;

      quickentity3Args = {
        pname = "quickentity-3";
        version = "3.0-30f394e";
        src = quickentity-rs-3;
        cargoLock.lockFile = "${quickentity-rs-3}/Cargo.lock";
        cargoBuildFlags = [ "--bin" "quickentity-rs" ];
        doCheck = false;
        postPatch = noGui;
        postInstall = "mv $out/bin/quickentity-rs $out/bin/quickentity-3";
      };
      quickentity3 = pkgs.rustPlatform.buildRustPackage quickentity3Args;
      quickentity3-static = pkgs.pkgsStatic.rustPlatform.buildRustPackage quickentity3Args;

      # SMF's legacy QuickEntity JavaScript with the npm packages it requires,
      # run by h3mod's embedded JavaScript engine
      npm = name: version: hash: pkgs.fetchurl {
        url = "https://registry.npmjs.org/${name}/-/${name}-${version}.tgz";
        inherit hash;
      };
      legacy-quickentity = pkgs.runCommand "smf-legacy-quickentity" { } ''
        mkdir -p $out/node_modules
        cp ${smf}/src/quickentity1136.js ${smf}/src/quickentity20.js ${smf}/src/quickentity.js \
          ${smf}/src/three-onlymath.min.js ${smf}/COPYING ${smf}/COPYING.LESSER $out/
        ${pkgs.lib.concatMapStrings ({ name, tgz }: ''
          mkdir -p $out/node_modules/${name}
          tar xzf ${tgz} -C $out/node_modules/${name} --strip-components=1
        '') [
          { name = "decimal.js"; tgz = npm "decimal.js" "10.3.1" "sha512-V0pfhfr8suzyPGOx3nmq4aHqabehUZn6Ch9kyFpV79TGDTWFmHqUqXdabR7QHqxzrYolF4+tVmJhUG4OURg5dQ=="; }
          { name = "lossless-json"; tgz = npm "lossless-json" "1.0.5" "sha512-RicKUuLwZVNZ6ZdJHgIZnSeA05p8qWc5NW0uR96mpPIjN9WDLUg9+kj1esQU1GkPn9iLZVKatSQK5gyiaFHgJA=="; }
          { name = "rfc6902"; tgz = npm "rfc6902" "5.0.1" "sha512-tYGfLpKIq9X7lrt4o3IkD9w9bpeAtsejfAqWNR98AoxfTsZqCepKa8eDlRiX8QMiCOD9vMx0/YbKLx0G1nPi5w=="; }
          { name = "lodash.isequal"; tgz = npm "lodash.isequal" "4.5.0" "sha1-QVxEePK8wwEgwizhDtMib30+GOA="; }
          { name = "lodash.merge"; tgz = npm "lodash.merge" "4.6.2" "sha512-0KpjqXRVvrYyCsX1swR/XTK0va6VQkQM6MNo7PqW77ByjAhoARA8EfrP1N4+KlKj8YS0ZUCtRT/YUuhyYDujIQ=="; }
        ]}
      '';

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
        vendorHash = "sha256-UjADC7lcXCJqer2VvsGkKD/JOhEn0oK0JJuA5MlaAWg=";
        env.CGO_ENABLED = 0;
        ldflags = [ "-s" "-w" ];
        doCheck = false;
      };
    in
    {
      packages.${system} = {
        inherit quickentity quickentity-static quickentity3 quickentity3-static legacy-quickentity resourcetool;
        default = pkgs.runCommand "h3mod-0.1.0" { nativeBuildInputs = [ pkgs.makeWrapper ]; } ''
          makeWrapper ${h3mod-bin}/bin/h3mod $out/bin/h3mod \
            --prefix PATH : ${pkgs.lib.makeBinPath [ quickentity quickentity3 resourcetool pkgs.xdelta pkgs._7zz ]} \
            --set-default H3MOD_HASH_LIST ${hash-list} \
            --set-default H3MOD_PATH_LIST ${path-list}/hash_list.txt \
            --set-default H3MOD_LEGACY_QN ${legacy-quickentity}
        '';

        # Portable tarball for other Linux systems: h3mod finds tools/ and
        # data/ next to itself. ResourceTool is the upstream build (glibc
        # 2.34+, libstdc++); everything else is static.
        bundle = pkgs.runCommand "h3mod-bundle-0.1.0" { } ''
          d=h3mod-linux-x64
          mkdir -p $d/tools $d/data $out
          cp ${h3mod-bin}/bin/h3mod $d/
          cp ${quickentity-static}/bin/quickentity-rs ${quickentity3-static}/bin/quickentity-3 ${pkgs.pkgsStatic.xdelta}/bin/xdelta3 \
            ${sevenzip-static}/bin/7zz ${resourcetool-bin}/ResourceTool ${resourcetool-bin}/*.so $d/tools/
          cp ${resourcetool-bin}/LICENSE $d/tools/ResourceTool-LICENSE
          cp ${sevenzip-static}/7-Zip-License.txt $d/tools/
          cp ${hash-list} $d/data/hash_list.hmla
          cp -r ${legacy-quickentity} $d/data/legacy-quickentity
          gzip -9 < ${path-list}/hash_list.txt > $d/data/hash_list.txt.gz
          cp ${./README.md} $d/README.md
          cp ${./LICENSE} $d/LICENSE
          chmod -R u+w $d
          chmod +x $d/h3mod $d/tools/{quickentity-rs,quickentity-3,xdelta3,7zz,ResourceTool}
          tar czf $out/$d.tar.gz $d
        '';
      };

      devShells.${system}.default = pkgs.mkShell {
        packages = [ pkgs.go quickentity quickentity3 resourcetool pkgs.xdelta pkgs._7zz ];
        H3MOD_HASH_LIST = "${hash-list}";
        H3MOD_PATH_LIST = "${path-list}/hash_list.txt";
        H3MOD_LEGACY_QN = "${legacy-quickentity}";
        # h3mod needs no cgo, and without it go never calls gcc, whose flags
        # from the shell break on the space in "HITMAN 3"
        CGO_ENABLED = "0";
      };
    };
}
