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

      # static, so the same binary runs on any x86_64 Linux
      version = pkgs.lib.fileContents ./VERSION;

      h3mod-bin = pkgs.buildGoModule {
        pname = "h3mod";
        inherit version;
        src = self;
        vendorHash = "sha256-sjJdMx4D73uhAuXAcPYcXzTekjJnm+3+EmGoFv+Qlzc=";
        env.CGO_ENABLED = 0;
        ldflags = [ "-s" "-w" "-X main.version=${version}" ];
        doCheck = false;
      };
      # the GUI, for nix users: Fyne needs cgo with OpenGL and X11
      guiLibs = with pkgs; [ libGL libx11 libxcursor libxrandr libxinerama libxi libxxf86vm libxext wayland libxkbcommon ];
      h3mod-gui-bin = pkgs.buildGoModule {
        pname = "h3mod-gui";
        inherit version;
        src = self;
        vendorHash = h3mod-bin.vendorHash;
        tags = [ "gui" ];
        nativeBuildInputs = [ pkgs.pkg-config ];
        buildInputs = guiLibs;
        ldflags = [ "-s" "-w" "-X main.version=${version}" ];
        doCheck = false;
        postInstall = "mv $out/bin/h3mod $out/bin/h3mod-gui";
      };

      # Portable GUI builds, cross-compiled with zig: a Windows exe without a
      # console window, and a Linux binary against glibc 2.31, which loads
      # X11/Wayland and OpenGL from the system at runtime.
      zigGui = target: extra: h3mod-gui-bin.overrideAttrs (old: {
        nativeBuildInputs = old.nativeBuildInputs ++ [ pkgs.zig ];
        env = (old.env or { }) // extra // { CGO_ENABLED = "1"; };
        # after stdenv has set its own compiler
        preBuild = (old.preBuild or "") + ''
          export ZIG_GLOBAL_CACHE_DIR=$TMPDIR/zig-cache ZIG_LOCAL_CACHE_DIR=$TMPDIR/zig-local
          export CC="zig cc -target ${target}" CXX="zig c++ -target ${target}"
        '';
      });
      h3mod-gui-windows = (zigGui "x86_64-windows-gnu" { GOOS = "windows"; GOARCH = "amd64"; }).overrideAttrs (old: {
        # zig ignores -mwindows, which -H windowsgui asks the external linker for
        ldflags = old.ldflags ++ [ "-H=windowsgui" "-extldflags=-Wl,--subsystem,windows" ];
        buildInputs = [ ];
        postInstall = "mv $out/bin/windows_amd64/h3mod.exe $out/bin/h3mod-gui.exe; rmdir $out/bin/windows_amd64";
      });

      h3mod-gui-linux = (zigGui "x86_64-linux-gnu.2.31" { }).overrideAttrs (old: {
        # Headers and link stubs come from nix; at runtime the system's libraries
        # are used, so their own (newer) glibc needs aren't checked here.
        preBuild = old.preBuild + ''
          pc="x11 xext xrender xfixes xcursor xrandr xinerama xi xxf86vm wayland-client wayland-cursor wayland-egl xkbcommon gl"
          export CGO_CFLAGS="$(pkg-config --cflags $pc)"
          export CGO_LDFLAGS="$(pkg-config --libs-only-L $pc) -Wl,--allow-shlib-undefined"
        '';
        buildInputs = old.buildInputs ++ [ pkgs.libglvnd.dev pkgs.libxrender pkgs.libxfixes ];
        # the C parts keep a path to the Go toolchain; harmless off nix
        disallowedReferences = [ ];
        postInstall = "mv $out/bin/h3mod $out/bin/h3mod-gui";
      });

      h3mod-windows = h3mod-bin.overrideAttrs (old: {
        env = old.env // { GOOS = "windows"; GOARCH = "amd64"; };
      });

      # Windows tools: ResourceTool from the same ZHMTools release, the rest
      # as SMF's Windows release ships them
      smf-release = pkgs.fetchurl {
        url = "https://github.com/atampy25/simple-mod-framework/releases/download/2.33.45/Release.zip";
        hash = "sha256-WDwD0JgVThjHkp5/EKJ+IWcn+n2gthCjMHD+rgyG4iA=";
      };
      resourcetool-win = pkgs.fetchurl {
        url = "https://github.com/OrfeasZ/ZHMTools/releases/download/v4.3.0/ResourceTool-win-x64.zip";
        hash = "sha256-Gafd4CBSEBpVxMzLp8NNTQ/60VBgX59v88EeKIy1UtM=";
      };
      windows-tools = pkgs.runCommand "h3mod-windows-tools" { nativeBuildInputs = [ pkgs._7zz ]; } ''
        mkdir -p $out
        7zz e -o$out ${smf-release} Third-Party/quickentity-rs.exe Third-Party/quickentity-3.exe \
          Third-Party/xdelta3.exe Third-Party/xdelta3-LICENSE
        7zz e -o$out ${resourcetool-win} ResourceTool.exe '*.dll'
        cp ${resourcetool-bin}/LICENSE $out/ResourceTool-LICENSE
      '';

      # shell completion scripts, generated by the commands themselves
      completions = name: bin: ''
        installShellCompletion --cmd ${name} \
          --bash <(${bin} completion bash) --zsh <(${bin} completion zsh) --fish <(${bin} completion fish)
      '';
      bundleCompletions = d: shells: pkgs.lib.concatMapStrings (s:
        let ext = if s == "powershell" then "ps1" else s; in ''
          ${h3mod-bin}/bin/h3mod completion ${s} > ${d}/completions/h3mod.${ext}
          ${h3mod-gui-bin}/bin/h3mod-gui completion ${s} > ${d}/completions/h3mod-gui.${ext}
        '') shells;

      # data/ of the bundles, and the files every bundle has
      bundleCommon = d: ''
        mkdir -p ${d}/tools ${d}/data ${d}/completions
        cp ${hash-list} ${d}/data/hash_list.hmla
        cp -r ${legacy-quickentity} ${d}/data/legacy-quickentity
        gzip -9 < ${path-list}/hash_list.txt > ${d}/data/hash_list.txt.gz
        cp ${./README.md} ${d}/README.md
        cp ${./LICENSE} ${d}/LICENSE
      '';
    in
    {
      packages.${system} = {
        inherit quickentity quickentity-static quickentity3 quickentity3-static legacy-quickentity resourcetool;
        inherit h3mod-gui-windows h3mod-gui-linux;
        default = pkgs.runCommand "h3mod-${version}" { nativeBuildInputs = [ pkgs.makeWrapper pkgs.installShellFiles ]; } ''
          makeWrapper ${h3mod-bin}/bin/h3mod $out/bin/h3mod \
            --prefix PATH : ${pkgs.lib.makeBinPath [ quickentity quickentity3 resourcetool pkgs.xdelta ]} \
            --set-default H3MOD_HASH_LIST ${hash-list} \
            --set-default H3MOD_PATH_LIST ${path-list}/hash_list.txt \
            --set-default H3MOD_LEGACY_QN ${legacy-quickentity}
          ${completions "h3mod" "${h3mod-bin}/bin/h3mod"}
        '';

        gui = pkgs.runCommand "h3mod-gui-${version}" { nativeBuildInputs = [ pkgs.makeWrapper pkgs.installShellFiles ]; } ''
          makeWrapper ${h3mod-gui-bin}/bin/h3mod-gui $out/bin/h3mod-gui \
            --prefix PATH : ${pkgs.lib.makeBinPath [ quickentity quickentity3 resourcetool pkgs.xdelta ]} \
            --prefix LD_LIBRARY_PATH : ${pkgs.lib.makeLibraryPath [ pkgs.libGL ]} \
            --set-default H3MOD_HASH_LIST ${hash-list} \
            --set-default H3MOD_PATH_LIST ${path-list}/hash_list.txt \
            --set-default H3MOD_LEGACY_QN ${legacy-quickentity}
          ${completions "h3mod-gui" "${h3mod-gui-bin}/bin/h3mod-gui"}
        '';

        # Portable archives: h3mod finds tools/ and data/ next to itself.
        # On Linux, ResourceTool is the upstream build (glibc 2.34+,
        # libstdc++) and everything else is static.
        bundle = pkgs.runCommand "h3mod-linux-x64-${version}" { } ''
          d=h3mod-linux-x64
          ${bundleCommon "$d"}
          cp ${h3mod-bin}/bin/h3mod ${h3mod-gui-linux}/bin/h3mod-gui $d/
          cp ${quickentity-static}/bin/quickentity-rs ${quickentity3-static}/bin/quickentity-3 ${pkgs.pkgsStatic.xdelta}/bin/xdelta3 \
            ${resourcetool-bin}/ResourceTool ${resourcetool-bin}/*.so $d/tools/
          cp ${resourcetool-bin}/LICENSE $d/tools/ResourceTool-LICENSE
          ${bundleCompletions "$d" [ "bash" "zsh" "fish" ]}
          chmod -R u+w $d
          chmod +x $d/h3mod $d/h3mod-gui $d/tools/{quickentity-rs,quickentity-3,xdelta3,ResourceTool}
          mkdir $out
          tar czf $out/h3mod-${version}-linux-x64.tar.gz $d
        '';

        bundle-windows = pkgs.runCommand "h3mod-windows-x64-${version}" { nativeBuildInputs = [ pkgs.zip ]; } ''
          d=h3mod-windows-x64
          ${bundleCommon "$d"}
          cp ${h3mod-windows}/bin/windows_amd64/h3mod.exe ${h3mod-gui-windows}/bin/h3mod-gui.exe $d/
          cp ${windows-tools}/* $d/tools/
          ${bundleCompletions "$d" [ "powershell" ]}
          chmod -R u+w $d
          mkdir $out
          zip -qr9 $out/h3mod-${version}-windows-x64.zip $d
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
