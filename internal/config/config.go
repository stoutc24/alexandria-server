package config

import (
	"log"
	"os"
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
}

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
	}

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
