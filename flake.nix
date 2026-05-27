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

          vendorHash = "";  # Go's go.sum handles integrity

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
