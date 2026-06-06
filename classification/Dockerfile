# syntax=docker/dockerfile:1
# Multi-stage production build — non-root, minimal attack surface

# ─── Stage 1: Builder ─────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache ca-certificates git

WORKDIR /src

# Download dependencies first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build both binaries statically
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags=-static" \
    -o /bin/server ./cmd/server

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags=-static" \
    -o /bin/worker ./cmd/worker

# ─── Stage 2: Server ──────────────────────────────────────────────────────────
FROM alpine:3.19 AS server

RUN apk add --no-cache ca-certificates wget

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /bin/server /classification-server

RUN adduser -D -g '' -s /bin/false appuser
USER appuser
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --spider -q http://localhost:8080/health || exit 1

ENTRYPOINT ["/classification-server"]

# ─── Stage 3: Worker ──────────────────────────────────────────────────────────
FROM gcr.io/distroless/static:nonroot AS worker

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /bin/worker /classification-worker

USER nonroot:nonroot

ENTRYPOINT ["/classification-worker"]

# ─── Default target ───────────────────────────────────────────────────────────
FROM server
