package config

import (
	"strings"
	"testing"
)

func TestLoadMigrateDevelopmentDefaults(t *testing.T) {
	for _, k := range []string{"APP_ENV", "MIGRATION_DATABASE_URL", "APP_DB_USER", "APP_DB_PASSWORD", "MIGRATION_TIMEOUT"} {
		t.Setenv(k, "")
	}
	cfg, err := LoadMigrate()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppRole != "cpim_app" || cfg.DatabaseURL != defaultDevMigrationDatabaseURL || cfg.AppPassword == "" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadMigrateProductionFailsClosed(t *testing.T) {
	for _, c := range []struct{ url, password, want string }{
		{"", "strong-password", "MIGRATION_DATABASE_URL"},
		{"postgres://x", "", "APP_DB_PASSWORD"},
		{"postgres://x", "cpim_app", "development default"},
	} {
		t.Setenv("APP_ENV", "production")
		t.Setenv("MIGRATION_DATABASE_URL", c.url)
		t.Setenv("APP_DB_PASSWORD", c.password)
		t.Setenv("APP_DB_USER", "")
		t.Setenv("MIGRATION_TIMEOUT", "")
		if _, err := LoadMigrate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%+v: err = %v", c, err)
		}
	}
}
