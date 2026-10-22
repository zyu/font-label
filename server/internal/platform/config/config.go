package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration, sourced from environment variables.
type Config struct {
	HTTPAddr      string
	DatabaseURL   string
	JWTSecret     string
	AccessTTL     time.Duration
	MediaSecret   string
	MediaTTL      time.Duration
	StorageRoot   string
	MaxFileMB     int64
	MaxHold       int
	LockTTL       time.Duration
	AdminUser     string
	AdminPassword string
	WorkerInAPI   bool
	LogLevel      string
}

func get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// Load reads configuration from the environment with development-friendly defaults.
func Load() Config {
	return Config{
		HTTPAddr:      get("HTTP_ADDR", ":18090"),
		DatabaseURL:   get("DATABASE_URL", "postgres://postgres:fontlabel@127.0.0.1:15433/fontlabel?sslmode=disable"),
		JWTSecret:     get("JWT_SECRET", "dev-only-jwt-secret-change-me-0123456789"),
		AccessTTL:     time.Duration(getInt("ACCESS_TTL_HOURS", 12)) * time.Hour,
		MediaSecret:   get("MEDIA_SIGN_SECRET", "dev-only-media-secret-change-me-9876543210"),
		MediaTTL:      time.Duration(getInt("MEDIA_SIGN_TTL_MIN", 60)) * time.Minute,
		StorageRoot:   get("STORAGE_ROOT", "./storage"),
		MaxFileMB:     int64(getInt("MAX_FILE_MB", 20)),
		MaxHold:       getInt("MAX_HOLD_PER_USER", 100),
		LockTTL:       time.Duration(getInt("LOCK_TTL_HOURS", 24)) * time.Hour,
		AdminUser:     get("ADMIN_INIT_USERNAME", "admin"),
		AdminPassword: get("ADMIN_INIT_PASSWORD", "Admin@12345"),
		WorkerInAPI:   get("WORKER_IN_API", "true") == "true",
		LogLevel:      get("LOG_LEVEL", "info"),
	}
}
