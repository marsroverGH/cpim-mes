package config

import (
	"os"
	"time"
)

type Config struct {
	HTTPAddr             string
	DatabaseURL          string
	JWTSecret            string
	MigrationTimeout     time.Duration
	MigrationInstalledBy string
}

func Load() Config {
	return Config{
		HTTPAddr:             getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:          getenv("DATABASE_URL", "postgres://cpim:cpim@localhost:5432/cpim?sslmode=disable"),
		JWTSecret:            getenv("JWT_SECRET", "dev-secret-change-me-in-production"),
		MigrationTimeout:     getDuration("MIGRATION_TIMEOUT", 10*time.Minute),
		MigrationInstalledBy: getenv("MIGRATION_INSTALLED_BY", "backend-startup"),
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getDuration(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}
