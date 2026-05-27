# Running kcd in a Container

All commands below use `docker` but work identically with `podman` (just
replace `docker` with `podman`).

Multi-architecture images (linux/amd64, linux/arm64) are published to
[ghcr.io/bethropolis/kcd](https://github.com/bethropolis/kcd/pkgs/container/kcd).

```bash
docker pull ghcr.io/bethropolis/kcd:latest
```

## Quick Start (host networking)

Device discovery (UDP broadcast + mDNS) requires the container to share the host
network stack:

```bash
docker run -d \
  --name kcd \
  --network host \
  --restart unless-stopped \
  -v kcd-config:/config \
  -v kcd-state:/state \
  -v ${PWD}/downloads:/data \
  ghcr.io/bethropolis/kcd:latest
```

## docker-compose / podman-compose

The [`docker-compose.yml`](../docker-compose.yml) at the project root provides
two networking profiles (compatible with both `docker compose` and
`podman-compose`):

| Profile | Command | Use case |
|---|---|---|
| `default` | `docker compose up -d` | Host networking — device discovery works |
| `bridge` | `docker compose --profile bridge up -d` | Swarm / K8s — no LAN broadcast, use `kcd connect <ip>` |

The compose file also includes a `kcd-cli` service (profile: `cli`) for running
subcommands against the daemon's Unix socket:

```bash
docker compose run --rm kcd-cli devices
```

## Volumes

| Path | Contents | Required |
|---|---|---|
| `/config` | `kcd.toml`, `cert.pem`, `key.pem` | Yes |
| `/state` | `devices.json` (persisted pairs) | Yes |
| `/data` | Received files | Optional |
| `/run` | IPC Unix socket | Yes (tmpfs recommended) |

The entrypoint auto-fixes ownership on all volumes at startup (remaps host-root
owned paths to UID 65534). A default `kcd.toml` is created at `/config/kcd/` if
none exists.

## Building Locally

```bash
docker build \
  --build-arg VERSION=dev \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -t kcd:local .
```

## Notes

- Bridge networking loses UDP broadcast discovery. Paired phones reconnect
  automatically via remembered IP, or use `kcd connect <phone-ip>`.
- `notify-send` and other desktop-integration tools are **not** available inside
  the container (no D-Bus session bus). Plugins that depend on them are no-ops.
- The healthcheck runs `kcd devices` against the daemon's IPC socket — it only
  passes once the daemon is fully initialised.
