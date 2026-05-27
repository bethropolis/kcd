# https://just.systems

binary_daemon := "kcd"
bin_dir := "bin"

default: all

# Run build and test
@all: build test

# Build the daemon
@build:
    mkdir -p {{ bin_dir }}
    CGO_ENABLED=0 go build -ldflags="-s -w" -o {{ bin_dir }}/{{ binary_daemon }} ./cmd/kcd
    echo "Build complete: {{ bin_dir }}/{{ binary_daemon }}"

# Run tests
test:
    CGO_ENABLED=0 go test -p 1 ./...

# Clean build artifacts
@clean:
    rm -f {{ bin_dir }}/{{ binary_daemon }}
    rm -rf packaging/
    echo "Cleanup complete"

# Install the binary and systemd service
@install: build
    mkdir -p ~/.local/bin
    install -m 755 {{ bin_dir }}/{{ binary_daemon }} ~/.local/bin/{{ binary_daemon }}
    mkdir -p ~/.config/systemd/user
    install -m 644 packaging/kcd-user.service ~/.config/systemd/user/kcd.service
    echo "Installed {{ binary_daemon }} to ~/.local/bin"
    echo "Installed systemd service to ~/.config/systemd/user/kcd.service"
    echo "Enable with: systemctl --user enable --now kcd"

# Run a dry-run release via goreleaser
release-dry-run:
    goreleaser release --snapshot --clean

# Install the Waybar integration script
@waybar:
    mkdir -p ~/.config/waybar/scripts
    cp desktop-integration/waybar/scripts/kcd-waybar.sh ~/.config/waybar/scripts/kcd-waybar.sh
    chmod +x ~/.config/waybar/scripts/kcd-waybar.sh
    echo "Installed kcd-waybar.sh to ~/.config/waybar/scripts/kcd-waybar.sh"
    echo "Add to Waybar config (config.jsonc):"
    echo '  "custom/kcd": {'
    echo '      "exec": "~/.config/waybar/scripts/kcd-waybar.sh",'
    echo '      "return-type": "json",'
    echo '      "restart-interval": 0'
    echo '  }'

# Run integration tests
integration:
    go test -tags integration -race -count=1 ./internal/integration/...
