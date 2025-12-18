package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port          string
	DBPath        string
	JWTSecret     string
	BCryptCost    int
	EncryptionKey string // For encrypting stored credentials
}

func Load() *Config {
	jwtSecret := getEnv("JWT_SECRET", "change-me-in-production")

	return &Config{
		Port:          getEnv("PORT", "3000"),
		DBPath:        getEnv("DB_PATH", "./alexandria.db"),
		JWTSecret:     jwtSecret,
		BCryptCost:    getEnvInt("BCRYPT_COST", 12),
		EncryptionKey: getEnv("ENCRYPTION_KEY", jwtSecret), // Fallback to JWT secret
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
