# Multi-stage Dockerfile for the Ingestion Mesh
# Stage 1: Build the Go binaries
# Stage 2: Minimal runtime image

# =============================================================================
# BUILD STAGE
# =============================================================================
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Create non-root user for build
RUN adduser -D -g '' appuser

WORKDIR /build

# Copy go module files first (for layer caching)
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build both binaries with optimal settings
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o bin/server ./cmd/server

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o bin/worker ./cmd/worker

# =============================================================================
# RUNTIME STAGE
# =============================================================================
FROM alpine:3.19

# Security: Install CA certs and create non-root user
RUN apk add --no-cache ca-certificates wget && \
    adduser -D -g '' -s /bin/false appuser

# Set timezone
ENV TZ=UTC

WORKDIR /app

# Copy binaries from builder
COPY --from=builder /build/bin/server /app/server
COPY --from=builder /build/bin/worker /app/worker

# Copy migration files for runtime reference
COPY --from=builder /build/migrations /app/migrations

# Change ownership to non-root user
RUN chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Expose the server port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --spider -q http://localhost:8080/health || exit 1

# Default to running the server
CMD ["/app/server"]
