#!/usr/bin/env bash
# kcd install script — installs from source into ~/.local/bin
# Usage: ./scripts/install.sh [--no-service] [--no-nautilus]
#
# Failure-tolerant: individual step failures are warned about and skipped,
# they do NOT abort the whole installation. Only a failed build is fatal.

# ── Colour setup ──────────────────────────────────────────────────────────────
if [[ -t 1 ]] && [[ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]]; then
  BOLD="$(tput bold)"
  RED="$(tput setaf 1)"
  GREEN="$(tput setaf 2)"
  YELLOW="$(tput setaf 3)"
  BLUE="$(tput setaf 4)"
  CYAN="$(tput setaf 6)"
  RESET="$(tput sgr0)"
else
  BOLD="" RED="" GREEN="" YELLOW="" BLUE="" CYAN="" RESET=""
fi

# ── Logging helpers ───────────────────────────────────────────────────────────
info()    { printf "  ${BLUE}→${RESET}  %s\n"       "$*"; }
success() { printf "  ${GREEN}✓${RESET}  %s\n"      "$*"; }
warn()    { printf "  ${YELLOW}⚠${RESET}  %s\n"     "$*" >&2; }
error()   { printf "  ${RED}✗${RESET}  %s\n"        "$*" >&2; }
step()    { printf "\n${BOLD}${CYAN}▶  %s${RESET}\n" "$*"; }
die()     { error "$*"; exit 1; }

# Runs a command, warns on failure but does NOT exit.
try() {
  if ! "$@" 2>/dev/null; then
    warn "Command failed (non-fatal): $*"
    return 1
  fi
  return 0
}

# ── Banner ────────────────────────────────────────────────────────────────────
printf "\n"
printf "${BOLD}${BLUE}┌─────────────────────────────────────────┐${RESET}\n"
printf "${BOLD}${BLUE}│${RESET}   ${BOLD}kcd${RESET} — Headless KDE Connect Daemon     ${BOLD}${BLUE}│${RESET}\n"
printf "${BOLD}${BLUE}└─────────────────────────────────────────┘${RESET}\n"
printf "\n"

# ── Argument parsing ──────────────────────────────────────────────────────────
INSTALL_SERVICE=true
INSTALL_NAUTILUS=true

for arg in "$@"; do
  case "$arg" in
    --no-service)  INSTALL_SERVICE=false ;;
    --no-nautilus) INSTALL_NAUTILUS=false ;;
    --help|-h)
      printf "Usage: %s [--no-service] [--no-nautilus]\n" "$(basename "$0")"
      exit 0 ;;
    *) die "Unknown argument: $arg" ;;
  esac
done

# ── Paths ─────────────────────────────────────────────────────────────────────
BIN_DIR="${HOME}/.local/bin"
SYSTEMD_DIR="${HOME}/.config/systemd/user"
CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/kcd"
NAUTILUS_EXT_DIR="${HOME}/.local/share/nautilus-python/extensions"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ── Prerequisites ─────────────────────────────────────────────────────────────
step "Checking prerequisites"

command -v go >/dev/null 2>&1 \
  || die "Go is not installed. Install Go 1.25+ from https://go.dev/dl/"

GO_VERSION="$(go version | awk '{print $3}' | tr -d 'go')"
REQUIRED_MAJOR=1
REQUIRED_MINOR=22
IFS='.' read -r MAJOR MINOR _ <<< "$GO_VERSION"
if (( MAJOR < REQUIRED_MAJOR || (MAJOR == REQUIRED_MAJOR && MINOR < REQUIRED_MINOR) )); then
  die "Go ${GO_VERSION} is too old. kcd requires Go ${REQUIRED_MAJOR}.${REQUIRED_MINOR}+."
fi
success "Go ${GO_VERSION} found"

# ── Stop existing service ─────────────────────────────────────────────────────
if systemctl --user is-active --quiet kcd.service 2>/dev/null; then
  step "Stopping existing kcd service"
  if try systemctl --user stop kcd.service; then
    success "Service stopped"
  fi
fi

# ── Build ─────────────────────────────────────────────────────────────────────
step "Building static binary"

cd "${REPO_ROOT}"

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo 'dev')"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

info "Version: ${VERSION}  Commit: ${COMMIT}  Date: ${DATE}"
info "CGO_ENABLED=0 go build ./cmd/kcd"

mkdir -p bin
if ! CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o bin/kcd \
      ./cmd/kcd; then
  # Restore backup if one exists
  [[ -f "${BIN_DIR}/kcd.backup" ]] \
    && mv "${BIN_DIR}/kcd.backup" "${BIN_DIR}/kcd" \
    && warn "Restored previous binary from backup."
  die "Build failed."
fi

# Verify static linkage (non-fatal — some distro ldd wrappers behave oddly)
if command -v ldd >/dev/null 2>&1; then
  if ldd bin/kcd 2>&1 | grep -qv "not a dynamic executable"; then
    warn "Binary may not be fully statically linked. Check CGO_ENABLED=0."
  fi
fi
success "Build succeeded ($(du -sh bin/kcd | cut -f1) static binary)"

# ── Install binary ────────────────────────────────────────────────────────────
step "Installing binary"

mkdir -p "${BIN_DIR}"

if [[ -f "${BIN_DIR}/kcd" ]]; then
  if try cp "${BIN_DIR}/kcd" "${BIN_DIR}/kcd.backup"; then
    info "Backed up previous binary → ${BIN_DIR}/kcd.backup"
  fi
fi

if try install -m 755 bin/kcd "${BIN_DIR}/kcd"; then
  success "Installed → ${BIN_DIR}/kcd"
else
  die "Could not install binary to ${BIN_DIR}/kcd."
fi

# Warn if the bin dir isn't in PATH
if ! echo ":${PATH}:" | grep -q ":${BIN_DIR}:"; then
  warn "${BIN_DIR} is not in your PATH."
  printf "  ${YELLOW}Add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):${RESET}\n"
  printf "    export PATH=\"\$HOME/.local/bin:\$PATH\"\n"
fi

# ── Config ────────────────────────────────────────────────────────────────────
step "Installing configuration"

mkdir -p "${CONFIG_DIR}"

if [[ ! -f "${CONFIG_DIR}/kcd.toml" ]]; then
  if try install -m 644 "${REPO_ROOT}/packaging/kcd.example.toml" "${CONFIG_DIR}/kcd.toml"; then
    success "Default config installed → ${CONFIG_DIR}/kcd.toml"
  else
    warn "Could not install default config — create ${CONFIG_DIR}/kcd.toml manually."
  fi
else
  info "Config already exists at ${CONFIG_DIR}/kcd.toml — skipping (won't overwrite)"
fi

# ── systemd user service ──────────────────────────────────────────────────────
if [[ "${INSTALL_SERVICE}" == true ]]; then
  step "Installing systemd user service"

  _systemd_ok=true

  if ! command -v systemctl >/dev/null 2>&1; then
    warn "systemctl not found — skipping service install"
    _systemd_ok=false
  fi

  if [[ "${_systemd_ok}" == true ]]; then
    mkdir -p "${SYSTEMD_DIR}"

    if ! try install -m 644 "${REPO_ROOT}/packaging/kcd-user.service" "${SYSTEMD_DIR}/kcd.service"; then
      warn "Could not install service file — skipping service setup"
      _systemd_ok=false
    fi
  fi

  if [[ "${_systemd_ok}" == true ]]; then
    try systemctl --user daemon-reload \
      || warn "daemon-reload failed — service file may not be recognised yet"

    try systemctl --user enable kcd.service \
      || warn "Could not enable kcd.service — you may need to enable it manually"

    try systemctl --user start kcd.service \
      || warn "Could not start kcd.service — check 'journalctl --user -u kcd -n 30'"

    # Wait briefly and check it actually started
    sleep 2
    if systemctl --user is-active --quiet kcd.service 2>/dev/null; then
      success "kcd.service enabled and running"
    else
      warn "kcd.service is installed but does not appear to be running."
      printf "  Run ${BOLD}journalctl --user -u kcd -n 30${RESET} to see why.\n"
    fi
  fi
fi

# ── Nautilus extension ────────────────────────────────────────────────────────
if [[ "${INSTALL_NAUTILUS}" == true ]] && command -v nautilus >/dev/null 2>&1; then
  step "Installing Nautilus extension"

  mkdir -p "${NAUTILUS_EXT_DIR}"
  if try install -m 644 "${REPO_ROOT}/packaging/nautilus-kcd.py" "${NAUTILUS_EXT_DIR}/nautilus-kcd.py"; then
    success "Extension installed → ${NAUTILUS_EXT_DIR}/nautilus-kcd.py"
  else
    warn "Could not install Nautilus extension"
  fi

  if [[ -n "${DISPLAY:-}" ]] || [[ -n "${WAYLAND_DISPLAY:-}" ]]; then
    try nautilus -q && info "Nautilus restarted to load the extension" \
      || info "Nautilus restart failed — restart it manually to activate the extension"
  else
    info "No display detected — restart Nautilus manually to activate the extension"
  fi
elif [[ "${INSTALL_NAUTILUS}" == true ]]; then
  info "Nautilus not found — skipping extension install"
fi

# ── Waybar refresh ────────────────────────────────────────────────────────────
if command -v waybar >/dev/null 2>&1 && pgrep -x waybar >/dev/null 2>&1; then
  step "Refreshing Waybar"
  if try pkill -SIGUSR2 waybar; then
    success "Waybar refreshed (SIGUSR2 sent)"
  else
    warn "Could not refresh Waybar — reload it manually if needed"
  fi
fi

# ── Shell completions ──────────────────────────────────────────────────────────
step "Installing shell completions"

BASH_COMP_DIR="${HOME}/.local/share/bash-completion/completions"
ZSH_COMP_DIR="${HOME}/.zfunc"
FISH_COMP_DIR="${HOME}/.config/fish/completions"

if [[ -f "${REPO_ROOT}/packaging/kcd.bash-completion" ]]; then
  if command -v bash >/dev/null 2>&1; then
    mkdir -p "${BASH_COMP_DIR}"
    install -m 644 "${REPO_ROOT}/packaging/kcd.bash-completion" \
      "${BASH_COMP_DIR}/kcd"
    success "Bash completion installed → ${BASH_COMP_DIR}/kcd"
    info "Add to ~/.bashrc if not already present:"
    printf "    source ~/.local/share/bash-completion/completions/kcd\n"
  fi

  if command -v zsh >/dev/null 2>&1; then
    mkdir -p "${ZSH_COMP_DIR}"
    install -m 644 "${REPO_ROOT}/packaging/kcd.zsh-completion" \
      "${ZSH_COMP_DIR}/_kcd"
    success "Zsh completion installed → ${ZSH_COMP_DIR}/_kcd"
    info "Add to ~/.zshrc if not already present:"
    printf "    fpath=(~/.zfunc \$fpath) && autoload -Uz compinit && compinit\n"
  fi

  if command -v fish >/dev/null 2>&1; then
    mkdir -p "${FISH_COMP_DIR}"
    install -m 644 "${REPO_ROOT}/packaging/kcd.fish-completion" \
      "${FISH_COMP_DIR}/kcd.fish"
    success "Fish completion installed → ${FISH_COMP_DIR}/kcd.fish"
  fi
else
  info "Completion files not found — skipping"
fi

# ── Firewall reminder ─────────────────────────────────────────────────────────
step "Firewall"

if command -v ufw >/dev/null 2>&1; then
  info "UFW detected. To open KDE Connect ports run:"
  printf "    sudo ufw allow 1716/udp\n"
  printf "    sudo ufw allow 1716/tcp\n"
  printf "    sudo ufw allow 1739:1764/tcp\n"
elif command -v firewall-cmd >/dev/null 2>&1; then
  info "firewalld detected. To open KDE Connect ports:"
  printf "    sudo cp ${REPO_ROOT}/packaging/firewalld-kcd.xml /etc/firewalld/services/kcd.xml\n"
  printf "    sudo firewall-cmd --permanent --add-service=kcd && sudo firewall-cmd --reload\n"
else
  info "No recognised firewall detected — ensure ports 1716/udp, 1716/tcp, 1739-1764/tcp are open"
fi

# ── Done ──────────────────────────────────────────────────────────────────────
printf "\n"
printf "${BOLD}${GREEN}┌─────────────────────────────────────────┐${RESET}\n"
printf "${BOLD}${GREEN}│${RESET}   ${BOLD}Installation complete!${RESET}                ${BOLD}${GREEN}│${RESET}\n"
printf "${BOLD}${GREEN}└─────────────────────────────────────────┘${RESET}\n"
printf "\n"
printf "  Next steps:\n"
printf "    ${BOLD}kcd devices${RESET}          — see discovered phones\n"
printf "    ${BOLD}kcd pair <id>${RESET}         — pair with a device\n"
printf "    ${BOLD}kcd watch${RESET}             — stream live events\n"
printf "\n"
printf "  Manage the daemon:\n"
printf "    ${BOLD}systemctl --user status kcd${RESET}\n"
printf "    ${BOLD}journalctl --user -u kcd -f${RESET}\n"
printf "\n"
