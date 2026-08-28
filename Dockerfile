# =============================================================================
# Alexandria Server - Multi-stage Docker Build
# =============================================================================

# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /build

# Install git for go mod download (some deps may need it)
RUN apk add --no-cache git

# Copy all source first (needed for go mod tidy to resolve dependencies)
COPY . .

# Generate go.sum and download dependencies
RUN go mod tidy && go mod download

# Build the binary with optimizations (architecture detected by Docker buildx)
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.Version=docker" \
    -o alexandria-server \
    ./cmd/server

# =============================================================================
# Runtime stage - minimal image
# =============================================================================
FROM alpine:3.19

# Labels for container metadata
LABEL org.opencontainers.image.title="Alexandria Server"
LABEL org.opencontainers.image.description="Companion server for Alexandria: merged library index, dedupe, and LLM coordination"
LABEL org.opencontainers.image.source="https://github.com/stoutc24/alexandria-server"
LABEL org.opencontainers.image.vendor="Stout Solutions"

# Install runtime dependencies
RUN apk --no-cache add \
    ca-certificates \
    tzdata \
    && rm -rf /var/cache/apk/*

# Create non-root user for security
RUN addgroup -g 1000 alexandria && \
    adduser -u 1000 -G alexandria -s /bin/sh -D alexandria

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/alexandria-server .

# Create data directory and set permissions
RUN mkdir -p /data && chown -R alexandria:alexandria /app /data

# Switch to non-root user
USER alexandria

# Environment variables with sensible defaults
ENV PORT=3000
ENV DB_PATH=/data/alexandria.db
# Deliberately the placeholder value: config.Load() rejects it, so a
# container started without a real token fails fast instead of coming up
# unauthenticated.
ENV ALEXANDRIA_API_TOKEN=change-me
ENV TZ=UTC

# Expose port
EXPOSE 3000

# Volume for persistent data
VOLUME ["/data"]

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:3000/health || exit 1

# Run the server
ENTRYPOINT ["./alexandria-server"]
