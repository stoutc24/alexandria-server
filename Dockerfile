# =============================================================================
# Alexandria Server - Multi-stage Docker Build
# =============================================================================

# Build stage
#
# There is no frontend stage: the admin page is a single HTML file
# embedded with go:embed (internal/adminui), so the image needs no Node
# toolchain and no npm dependencies.
FROM golang:1.22-alpine AS builder

WORKDIR /build

# Install git for go mod download (some deps may need it)
RUN apk add --no-cache git

# Copy all source first (needed for go mod tidy to resolve dependencies)
COPY . .

# go.sum is committed, so download exactly what it pins rather than
# letting tidy resolve fresh versions at image-build time.
RUN go mod download

# Build the binary with optimizations (architecture detected by Docker buildx)
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.Version=docker" \
    -trimpath \
    -o alexandria-server \
    ./cmd/server

# =============================================================================
# Runtime stage - minimal image
# =============================================================================
FROM alpine:3.19

# Labels for container metadata
LABEL org.opencontainers.image.title="Alexandria Server"
LABEL org.opencontainers.image.description="Companion server for Alexandria: merged library index, dedupe, per-profile stats"
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
ENV PORT=4000
ENV DB_PATH=/data/alexandria.db
# Deliberately the placeholder: config.Load() rejects it, so a container
# started without a real token fails fast instead of coming up open.
ENV ALEXANDRIA_API_TOKEN=change-me
ENV TZ=UTC

# Expose port
EXPOSE 4000

# Volume for persistent data
VOLUME ["/data"]

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:4000/health || exit 1

# Run the server
ENTRYPOINT ["./alexandria-server"]
