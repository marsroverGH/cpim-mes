package main

import (
	"context"
	"log"

	"github.com/cpim-mes/backend/internal/config"
	"github.com/cpim-mes/backend/internal/migration"
	"github.com/cpim-mes/backend/internal/repository"
)

// runMigrate is the one-shot "server migrate" step. It alone holds the
// privileged database URL; the HTTP server connects as the application role.
func runMigrate() {
	cfg, err := config.LoadMigrate()
	if err != nil {
		log.Fatalf("migration configuration invalid: %v", err)
	}
	db, err := repository.NewDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	result, err := migration.New(db, cfg.InstalledBy, log.Default()).Migrate(ctx)
	if err != nil {
		log.Fatalf("database migration failed: %v", err)
	}
	if len(result.Baselined) > 0 || len(result.AppliedNow) > 0 {
		log.Printf("database migration complete: latest=%04d baselined=%v applied=%v", result.LatestVersion, result.Baselined, result.AppliedNow)
	}
	if err := migration.EnsureAppRole(ctx, db, cfg.AppRole, cfg.AppPassword); err != nil {
		log.Fatalf("application role setup failed: %v", err)
	}
	log.Printf("application role %q is ready", cfg.AppRole)
}
