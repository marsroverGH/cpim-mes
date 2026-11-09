package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	defaultDevMigrationDatabaseURL = "postgres://cpim:cpim@localhost:5432/cpim?sslmode=disable"
	defaultDevAppDBPassword        = "cpim_app"
	defaultAppDBRole               = "cpim_app"
)

// MigrateConfig configures the one-shot "server migrate" step.
type MigrateConfig struct {
	DatabaseURL string
	AppRole     string
	AppPassword string
	Timeout     time.Duration
	InstalledBy string
}

// LoadMigrate reads and validates the migration step's environment.
func LoadMigrate() (MigrateConfig, error) {
	production := strings.TrimSpace(os.Getenv("APP_ENV")) == EnvironmentProduction
	url, password := "", ""
	if !production {
		url, password = defaultDevMigrationDatabaseURL, defaultDevAppDBPassword
	}
	cfg := MigrateConfig{
		DatabaseURL: envOr("MIGRATION_DATABASE_URL", url),
		AppRole:     envOr("APP_DB_USER", defaultAppDBRole),
		AppPassword: envOr("APP_DB_PASSWORD", password),
		Timeout:     getDuration("MIGRATION_TIMEOUT", 10*time.Minute),
		InstalledBy: getenv("MIGRATION_INSTALLED_BY", "migrate-step"),
	}
	var errs []error
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("MIGRATION_DATABASE_URL must be set"))
	}
	if cfg.AppPassword == "" {
		errs = append(errs, errors.New("APP_DB_PASSWORD must be set"))
	}
	if production && cfg.AppPassword == defaultDevAppDBPassword {
		errs = append(errs, fmt.Errorf("APP_DB_PASSWORD must not use the development default in production"))
	}
	if cfg.Timeout <= 0 {
		errs = append(errs, errors.New("MIGRATION_TIMEOUT must be greater than zero"))
	}
	if err := errors.Join(errs...); err != nil {
		return MigrateConfig{}, err
	}
	return cfg, nil
}

// envOr treats a set-but-empty variable like an unset one, because Compose
// passes empty strings for optional values.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
