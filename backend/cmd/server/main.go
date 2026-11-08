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
	"github.com/cpim-mes/backend/internal/buildinfo"
	"github.com/cpim-mes/backend/internal/config"
	"github.com/cpim-mes/backend/internal/migration"
	"github.com/cpim-mes/backend/internal/observability"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/cpim-mes/backend/internal/service"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration validation failed; backend will not start: %v", err)
	}
	log.Printf("configuration validated: environment=%s allowed_origins=%d bootstrap_admin_configured=%t", cfg.Environment, len(cfg.AllowedOrigins), cfg.HasBootstrapAdmin())

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

	timezoneCtx, timezoneCancel := context.WithTimeout(context.Background(), 15*time.Second)
	err = service.CheckBusinessTimezone(timezoneCtx, db)
	timezoneCancel()
	if err != nil {
		log.Fatalf("business timezone check failed; backend will not start: %v", err)
	}

	repos := repository.NewRepositories(db)

	// Step 3 removes fixed demo credentials entirely. An empty users table is a
	// startup gate: the first administrator must be supplied explicitly via a
	// password file. Existing databases are never overwritten by bootstrap.
	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 15*time.Second)
	userCount, err := repos.Users.Count(bootstrapCtx)
	if err != nil {
		bootstrapCancel()
		log.Fatalf("initial administrator gate failed while counting users: %v", err)
	}
	if userCount == 0 {
		if !cfg.HasBootstrapAdmin() {
			bootstrapCancel()
			log.Fatal("users table is empty; set BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD_FILE to create the initial administrator")
		}
		password, err := config.ReadBootstrapAdminPassword(cfg.BootstrapAdminPasswordFile)
		if err != nil {
			bootstrapCancel()
			log.Fatalf("initial administrator secret could not be read: %v", err)
		}
		created, bootstrapErr := repos.Users.BootstrapAdmin(bootstrapCtx, cfg.BootstrapAdminUsername, password)
		for i := range password {
			password[i] = 0
		}
		if bootstrapErr != nil {
			bootstrapCancel()
			log.Fatalf("initial administrator bootstrap failed: %v", bootstrapErr)
		}
		if created {
			log.Printf("[auth] initial administrator created: username=%q", cfg.BootstrapAdminUsername)
		} else {
			log.Printf("[auth] initial administrator bootstrap skipped because another instance created a user first")
		}
	} else if cfg.HasBootstrapAdmin() {
		log.Printf("[auth] bootstrap configuration ignored because users already exist")
	}
	bootstrapCancel()

	info := buildinfo.Current(result.LatestVersion)
	log.Printf("build metadata: version=%s commit=%s build_time=%s migration_latest=%d", info.Version, info.Commit, info.BuildTime, info.MigrationLatest)

	metrics := observability.NewRegistry(db)
	tracer, err := observability.NewTracer(observability.TracerConfig{
		Endpoint:      cfg.OTELExporterOTLPEndpoint,
		Headers:       cfg.OTELExporterOTLPHeaders,
		ServiceName:   cfg.OTELServiceName,
		Environment:   cfg.Environment,
		BuildInfo:     info,
		ExportTimeout: cfg.OTELExportTimeout,
		QueueSize:     cfg.OTELQueueSize,
	}, metrics)
	if err != nil {
		log.Fatalf("observability configuration failed; backend will not start: %v", err)
	}
	log.Printf("observability ready: prometheus=/metrics otlp_tracing=%t", tracer.Enabled())

	services := service.NewServices(db, repos, service.ServicesConfig{
		JWTSecret: cfg.JWTSecret,
	})
	router := api.NewRouter(services, api.RouterConfig{
		AllowedOrigins:                    cfg.AllowedOrigins,
		CookieSecure:                      cfg.CookieSecure,
		CookieSameSite:                    cfg.CookieSameSite,
		LoginRateLimitWindow:              cfg.LoginRateLimitWindow,
		LoginRateLimitIPMaxFailures:       cfg.LoginRateLimitIPMaxFailures,
		LoginRateLimitUsernameMaxFailures: cfg.LoginRateLimitUsernameMaxFailures,
		BuildInfo:                         info,
		Metrics:                           metrics,
		Tracer:                            tracer,
	})

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
	if err := tracer.Close(ctx); err != nil {
		log.Printf("observability shutdown warning: %v", err)
	}
}
