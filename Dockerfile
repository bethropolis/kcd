# ─── Builder ───────────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.date=${DATE}" \
      -o /out/kcd \
      ./cmd/kcd

RUN mkdir -p /out/empty && \
    for d in config state data run/kcd; do \
        mkdir -p "/out/$d"; \
    done && \
    chown -R 65534:65534 /out/config /out/state /out/data /out/run

RUN apk add --no-cache file && \
    file /out/kcd | grep -q "statically linked" || \
    (echo "ERROR: binary is not statically linked" && exit 1)

# ─── Runtime ───────────────────────────────────────────────────────────────────
FROM alpine:3.20

LABEL org.opencontainers.image.title="kcd" \
      org.opencontainers.image.description="Headless KDE Connect daemon" \
      org.opencontainers.image.url="https://github.com/bethropolis/kcd" \
      org.opencontainers.image.source="https://github.com/bethropolis/kcd" \
      org.opencontainers.image.licenses="MIT"

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/kcd /usr/bin/kcd

COPY --from=builder --chown=65534:65534 /out/config /config
COPY --from=builder --chown=65534:65534 /out/state /state
COPY --from=builder --chown=65534:65534 /out/data /data
COPY --from=builder --chown=65534:65534 /out/run /run

COPY entrypoint.sh /entrypoint.sh

VOLUME ["/config", "/state", "/data"]

ENV XDG_CONFIG_HOME=/config \
    XDG_STATE_HOME=/state \
    XDG_RUNTIME_DIR=/run \
    XDG_CACHE_HOME=/tmp

EXPOSE 1716/tcp 1716/udp 1739-1764/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/bin/kcd", "devices"]

ENTRYPOINT ["/entrypoint.sh"]
CMD ["daemon"]
