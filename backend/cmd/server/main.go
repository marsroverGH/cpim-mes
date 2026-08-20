package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cpim-mes/backend/internal/api"
	"github.com/cpim-mes/backend/internal/config"
	"github.com/cpim-mes/backend/internal/migration"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/cpim-mes/backend/internal/service"
)

func main() {
	cfg := config.Load()

	db, err := repository.NewDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer db.Close()

	// Schema migration is a startup gate: the API must never run against an
	// older, partially upgraded, checksum-mismatched, or newer-than-binary DB.
	migrationCtx, migrationCancel := context.WithTimeout(context.Background(), cfg.MigrationTimeout)
	result, err := migration.New(db, cfg.MigrationInstalledBy, log.Default()).Migrate(migrationCtx)
	migrationCancel()
	if err != nil {
		log.Fatalf("database migration failed; backend will not start: %v", err)
	}
	if len(result.Baselined) > 0 || len(result.AppliedNow) > 0 {
		log.Printf("database migration complete: latest=%04d baselined=%v applied=%v", result.LatestVersion, result.Baselined, result.AppliedNow)
	}

	repos := repository.NewRepositories(db)

	// 起動時に初期ユーザーをシード (users テーブルが空のときのみ)
	seedCtx, seedCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := repos.Users.SeedDefaultUsers(seedCtx); err != nil {
		log.Printf("[warn] seed users failed: %v", err)
	}
	seedCancel()

	services := service.NewServices(db, repos, service.ServicesConfig{
		JWTSecret: cfg.JWTSecret,
	})
	router := api.NewRouter(services)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
