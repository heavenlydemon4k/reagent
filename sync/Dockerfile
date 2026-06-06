# Multi-stage build for production
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install dependencies
RUN apk add --no-cache git ca-certificates

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build both binaries
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/worker ./cmd/worker

# Production image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binaries from builder
COPY --from=builder /app/bin/server /app/bin/server
COPY --from=builder /app/bin/worker /app/bin/worker

# Copy migrations
COPY --from=builder /app/migrations /app/migrations

# Non-root user
RUN adduser -D -u 1000 sync
USER sync

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --spider -q http://localhost:8080/health || exit 1

# Default to server
CMD ["/app/bin/server"]
