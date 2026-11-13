{
  description = "h3mod: HITMAN 3 mod deployer";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    # QuickEntity 3.1, the version SMF uses for entity.patch.json (patchVersion 6)
    quickentity-rs = {
      url = "github:atampy25/quickentity-rs/3.1";
      flake = false;
    };

    # ResourceTool: game binary <-> JSON for TEMP/TBLU (prebuilt Linux release)
    resourcetool-bin = {
      url = "https://github.com/OrfeasZ/ZHMTools/releases/download/v4.3.0/ResourceTool-linux-x64.zip";
      flake = false;
    };
  };

  outputs = { self, nixpkgs, quickentity-rs, resourcetool-bin }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};

      quickentity = pkgs.rustPlatform.buildRustPackage {
        pname = "quickentity-rs";
        version = "3.1";
        src = quickentity-rs;
        cargoLock.lockFile = "${quickentity-rs}/Cargo.lock";
        cargoBuildFlags = [ "--bin" "quickentity-rs" ];
        doCheck = false;
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
    in
    {
      packages.${system} = {
        inherit quickentity resourcetool;
        default = pkgs.buildGoModule {
          pname = "h3mod";
          version = "0.1.0";
          src = self;
          vendorHash = null;
          doCheck = false;
          nativeBuildInputs = [ pkgs.makeWrapper ];
          postInstall = ''
            wrapProgram $out/bin/h3mod --prefix PATH : ${pkgs.lib.makeBinPath [ quickentity resourcetool ]}
          '';
        };
      };

      devShells.${system}.default = pkgs.mkShell {
        packages = [ pkgs.go quickentity resourcetool ];
      };
    };
}
