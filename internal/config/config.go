package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port       string
	DBPath     string
	JWTSecret  string
	BCryptCost int
}

func Load() *Config {
	return &Config{
		Port:       getEnv("PORT", "3000"),
		DBPath:     getEnv("DB_PATH", "./alexandria.db"),
		JWTSecret:  getEnv("JWT_SECRET", "change-me-in-production"),
		BCryptCost: getEnvInt("BCRYPT_COST", 12),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
