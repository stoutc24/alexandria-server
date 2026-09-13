package config

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
)

// Config is the whole configuration surface. It is deliberately small:
// this server is a single-household appliance, not a service with
// tenants. One deployment, one library, one shared token.
type Config struct {
	Port     string
	DBPath   string
	APIToken string

	// ServerName labels this server in LAN discovery pick-lists. It
	// defaults to the machine's hostname, which is WRONG in Docker:
	// there the hostname is the container id, so the server appears to
	// be a different machine after every recreate (reported from a live
	// deployment). Set ALEXANDRIA_SERVER_NAME to pin it.
	ServerName string

	// Trailers. The three tools are looked up on PATH unless pinned
	// here (YT_DLP_PATH, FFMPEG_PATH, DENO_PATH); none is required —
	// without them the server runs and simply does not offer trailers.
	// The MP4 cache defaults to a "trailers" directory beside the
	// database, so in Docker it lands on the /data volume, and is capped
	// in megabytes with the least recently played evicted first.
	YtDlpPath       string
	FfmpegPath      string
	DenoPath        string
	TrailerCacheDir string
	TrailerCacheMB  int64
}

// defaultTrailerCacheMB mirrors trailers.DefaultCacheMB. 4 GB is a few
// hundred trailers, which a NAS will not notice.
const defaultTrailerCacheMB = 4096

// defaultToken is the placeholder shipped in .env.example and the
// Dockerfile. Booting with it would leave an open server, so it counts
// as unset.
const defaultToken = "change-me"

func Load() *Config {
	cfg := &Config{
		Port:       getEnv("PORT", "4000"),
		DBPath:     getEnv("DB_PATH", "./alexandria.db"),
		APIToken:   os.Getenv("ALEXANDRIA_API_TOKEN"),
		ServerName: os.Getenv("ALEXANDRIA_SERVER_NAME"),

		YtDlpPath:      os.Getenv("YT_DLP_PATH"),
		FfmpegPath:     os.Getenv("FFMPEG_PATH"),
		DenoPath:       os.Getenv("DENO_PATH"),
		TrailerCacheMB: getEnvInt64("TRAILER_CACHE_MB", defaultTrailerCacheMB),
	}
	cfg.TrailerCacheDir = getEnv("TRAILER_CACHE_DIR",
		filepath.Join(filepath.Dir(cfg.DBPath), "trailers"))

	// Refuse to start rather than come up unauthenticated. This server
	// holds a map of everything the household owns; an accidentally
	// exposed one with no token is worse than one that failed to boot.
	if cfg.APIToken == "" || cfg.APIToken == defaultToken {
		log.Fatal("ALEXANDRIA_API_TOKEN is not set.\n" +
			"Generate one with:  openssl rand -hex 32\n" +
			"then set it in .env (or pass it to the container).")
	}
	if len(cfg.APIToken) < 16 {
		log.Fatal("ALEXANDRIA_API_TOKEN is too short; use at least 16 " +
			"characters (openssl rand -hex 32).")
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt64 reads a positive integer. Garbage is logged and replaced
// by the default rather than refused: a typo in a cache size should not
// keep the whole server from starting.
func getEnvInt64(key string, defaultValue int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		log.Printf("%s=%q is not a positive integer; using %d", key, value, defaultValue)
		return defaultValue
	}
	return n
}
