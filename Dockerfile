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
# Runtime stage
# =============================================================================
#
# Debian, not Alpine, and the reason is deno. YouTube trailers need three
# tools at runtime — yt-dlp to extract, ffmpeg to merge the separate
# video and audio streams YouTube now serves, and deno as the JavaScript
# runtime that extraction requires — and deno's Linux build is
# glibc-linked, so it will not start on musl. The price is size: expect
# the image to grow by a few hundred MB over the Alpine one (ffmpeg's
# codec libraries are most of it, then the ~100 MB deno binary and the
# ~30 MB yt-dlp bundle). Without any one of the three the server still
# runs; it just does not advertise "trailers.youtube" on /health.
FROM debian:bookworm-slim

# Labels for container metadata
LABEL org.opencontainers.image.title="Alexandria Server"
LABEL org.opencontainers.image.description="Companion server for Alexandria: merged library index, dedupe, per-profile stats, trailers"
LABEL org.opencontainers.image.vendor="Stout Solutions"

# Install runtime dependencies. curl fetches yt-dlp below and replaces
# wget in the health check — bookworm-slim ships neither.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        tzdata \
        curl \
        ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# deno, from the official image. denoland/deno:bin is a multi-arch
# manifest (amd64 + arm64), so buildx picks the right binary per platform.
COPY --from=denoland/deno:bin /deno /usr/local/bin/deno

# Create non-root user for security. Same uid/gid as the Alpine image
# had, so an existing /data volume keeps its ownership across the base
# image change. -m gives it a home: yt-dlp and deno keep small caches
# under $HOME and warn (or fail) when there is nowhere to put them.
RUN groupadd -g 1000 alexandria && \
    useradd -u 1000 -g alexandria -s /bin/sh -m alexandria

WORKDIR /app

# yt-dlp: the standalone release binary for this image's architecture.
# It lives under /app rather than /usr/local/bin because `yt-dlp -U`,
# which the server runs daily, rewrites the binary in place and needs to
# own both the file AND its directory — and /app is the alexandria
# user's (chown below). A self-updated binary lasts until the container
# is recreated; the next image build bakes in the then-current release.
RUN mkdir -p /app/bin && \
    case "$(dpkg --print-architecture)" in \
        amd64) asset=yt-dlp_linux ;; \
        arm64) asset=yt-dlp_linux_aarch64 ;; \
        *) echo "no yt-dlp release binary for $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /app/bin/yt-dlp \
        "https://github.com/yt-dlp/yt-dlp/releases/latest/download/${asset}" && \
    chmod 0755 /app/bin/yt-dlp

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

# Keep the Go heap bounded on a small box. Without it the collector
# sizes itself against the HOST's memory, which on a NAS running other
# things means this holds far more than it needs. 96 MiB is generous for
# a workload whose resident set is around 20 MB. (This bounds the Go
# process only; a trailer download runs yt-dlp, deno and ffmpeg as
# children, which count against the container's limit, not this.)
ENV GOMEMLIMIT=96MiB
# One background collector is right for a mostly-idle service; the
# default scales with CPU count and just adds scheduler churn here.
ENV GOGC=100
ENV TZ=UTC

# Trailers: where yt-dlp is, and where the MP4 cache goes — on the data
# volume, so it survives a recreate. TRAILER_CACHE_MB caps the cache
# (default 4096; least recently played evicted first). ffmpeg and deno
# are found on PATH.
ENV YT_DLP_PATH=/app/bin/yt-dlp
ENV PATH="/app/bin:${PATH}"
ENV TRAILER_CACHE_DIR=/data/trailers

# Expose port
EXPOSE 4000

# Volume for persistent data
VOLUME ["/data"]

# Health check (curl, not wget: see the apt step)
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -fsS -o /dev/null http://localhost:4000/health || exit 1

# Run the server
ENTRYPOINT ["./alexandria-server"]
