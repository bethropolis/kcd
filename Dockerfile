# Multi-stage Dockerfile for kcd (KDE Connect Daemon)
# Builds a minimal static image from scratch.

FROM golang:1.26-alpine AS builder

WORKDIR /app

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build static binaries
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /kcd ./cmd/kcd
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /kcdctl ./cmd/kcdctl

# Final production image
FROM scratch

# Copy binaries
COPY --from=builder /kcd /usr/bin/kcd
COPY --from=builder /kcdctl /usr/bin/kcdctl

# Copy SSL certificates for TLS connections
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Use kcd as entrypoint
ENTRYPOINT ["/usr/bin/kcd"]
