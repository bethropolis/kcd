# ─── Builder ───────────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

# Build-time version info (injected by GoReleaser / docker buildx)
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

WORKDIR /build

# Fetch dependencies in a separate layer so they are cached between code changes
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Build a fully static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.date=${DATE}" \
      -o /out/kcd \
      ./cmd/kcd

# Smoke-test: binary must be statically linked
RUN file /out/kcd | grep -q "statically linked" || \
    (echo "ERROR: binary is not statically linked" && exit 1)

# ─── Runtime ───────────────────────────────────────────────────────────────────
# `scratch` gives us a zero-footprint image.
# The binary is fully static, so no libc or shell is needed.
FROM scratch

LABEL org.opencontainers.image.title="kcd" \
      org.opencontainers.image.description="Headless KDE Connect daemon" \
      org.opencontainers.image.url="https://github.com/bethropolis/kcd" \
      org.opencontainers.image.source="https://github.com/bethropolis/kcd" \
      org.opencontainers.image.licenses="MIT"

# TLS root certificates (needed for outbound TLS when communicating with devices)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# The binary
COPY --from=builder /out/kcd /usr/bin/kcd

# Volume mount points — must be provided at runtime
# /config  → $XDG_CONFIG_HOME/kcd  (kcd.toml, cert.pem, key.pem)
# /state   → $XDG_STATE_HOME/kcd   (devices.json — persisted pairs)
# /data    → download_dir           (received files)
VOLUME ["/config", "/state", "/data"]

# kcd reads these to find its paths without a real home directory
ENV XDG_CONFIG_HOME=/config \
    XDG_STATE_HOME=/state \
    XDG_RUNTIME_DIR=/run \
    XDG_CACHE_HOME=/tmp

# KDE Connect control port (TCP + UDP)
EXPOSE 1716/tcp
EXPOSE 1716/udp

# File-transfer side-channels
EXPOSE 1739-1764/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/bin/kcd", "devices"]

ENTRYPOINT ["/usr/bin/kcd"]
CMD ["daemon"]
