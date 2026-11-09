# =============================================================================
# Alexandria Server - Multi-stage Docker Build
# =============================================================================

# Build stage
#
# There is no frontend stage: the admin page is a single HTML file
# embedded with go:embed (internal/adminui), so the image needs no Node
# toolchain and no npm dependencies.
#
# Both base images are a supported release pinned by digest (a tag can be
# re-pointed; a digest cannot). Dependabot proposes updates. go.mod's `go`
# line is the MINIMUM version: building with a newer Go is fine, and an
# out-of-support Go (1.22 was) ships without the standard library's
# security fixes.
FROM golang:1.27-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS builder

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

# License notices for everything compiled into the binary: the Go standard
# library and each module `go version -m` lists. MIT and BSD both require
# the notice to travel with binary copies, and generating it here means it
# can never fall behind a dependency update. A module without a license
# file fails the build rather than shipping unannounced.
RUN set -e; out=THIRD_PARTY_NOTICES.txt; \
    { echo "Alexandria Server includes the third-party software below, each under its own license."; echo; \
      echo "==== Go standard library and runtime"; cat "$(go env GOROOT)/LICENSE"; echo; } > "$out"; \
    go version -m alexandria-server | awk '$1 == "dep" { print $2, $3 }' | while read -r mod ver; do \
      dir="$(go env GOMODCACHE)/$(echo "$mod" | sed 's/[A-Z]/!&/g' | tr 'A-Z' 'a-z')@$ver"; \
      lic="$(ls "$dir" | grep -i -E '^(licen[cs]e|copying)' | head -n 1)"; \
      [ -n "$lic" ] || { echo "no license file for $mod $ver" >&2; exit 1; }; \
      { echo "==== $mod $ver"; cat "$dir/$lic"; echo; } >> "$out"; \
    done

# =============================================================================
# Runtime stage - minimal image
# =============================================================================
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

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

# Copy binary from builder, with Alexandria's license and the notices
# for the software inside it
COPY --from=builder /build/alexandria-server /build/LICENSE /build/THIRD_PARTY_NOTICES.txt ./

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

# Keep the Go heap bounded on a small box. Without it the collector
# sizes itself against the HOST's memory, which on a NAS running other
# things means this holds far more than it needs. 96 MiB is generous for
# a workload whose resident set is around 20 MB.
ENV GOMEMLIMIT=96MiB
# One background collector is right for a mostly-idle service; the
# default scales with CPU count and just adds scheduler churn here.
ENV GOGC=100
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
