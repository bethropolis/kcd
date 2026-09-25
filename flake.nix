{
  description = "Lightweight, headless implementation of the KDE Connect protocol in Go";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "kcd";
          version = self.shortRev or "dirty";

          src = ./.;

          # Update when go.sum changes: nix build 2>&1 | grep 'got:' | awk '{print $2}'
          vendorHash = "sha256-/rT2aUVw0AG5oSMq/nTaybsvMUd+bPLMPJR2J1dltic=";

          subPackages = [ "cmd/kcd" ];

          env.CGO_ENABLED = "0";

          ldflags = [
            "-s" "-w"
            "-X main.version=${self.shortRev or "dirty"}"
          ];

          meta = with pkgs.lib; {
            description = "Headless KDE Connect daemon written in Go";
            homepage = "https://github.com/bethropolis/kcd";
            license = licenses.mit;
            maintainers = [ ];
            platforms = platforms.linux;
          };
        };

        apps.default = flake-utils.lib.mkApp {
          drv = self.packages.${system}.default;
        };
      }
    );
}
