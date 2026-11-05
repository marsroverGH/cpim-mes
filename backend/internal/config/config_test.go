package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"APP_ENV", "HTTP_ADDR", "DATABASE_URL", "JWT_SECRET", "ALLOWED_ORIGINS",
		"SEED_DEMO_USERS", "BOOTSTRAP_ADMIN_USERNAME", "BOOTSTRAP_ADMIN_PASSWORD",
		"BOOTSTRAP_ADMIN_PASSWORD_FILE", "COOKIE_SECURE", "COOKIE_SAMESITE",
		"LOGIN_RATE_LIMIT_WINDOW", "LOGIN_RATE_LIMIT_IP_MAX_FAILURES",
		"LOGIN_RATE_LIMIT_USERNAME_MAX_FAILURES", "MIGRATION_TIMEOUT", "MIGRATION_INSTALLED_BY",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_SERVICE_NAME",
		"OTEL_EXPORT_TIMEOUT", "OTEL_BSP_MAX_QUEUE_SIZE",
	}
	for _, key := range keys {
		value, existed := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		key, value, existed := key, value, existed
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}

func setProductionMinimum(t *testing.T) {
	t.Helper()
	clearConfigEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://cpim:secret@db:5432/cpim?sslmode=require")
	t.Setenv("JWT_SECRET", "production-secret-with-high-entropy-placeholder")
	t.Setenv("COOKIE_SECURE", "true")
}

func writeBootstrapSecret(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bootstrap-admin-password")
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return path
}

func TestLoadDevelopmentDefaultsHaveNoFixedUsers(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Environment != EnvironmentDevelopment {
		t.Fatalf("Environment = %q", cfg.Environment)
	}
	if cfg.DatabaseURL != defaultDevDatabaseURL {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.JWTSecret != defaultDevJWTSecret {
		t.Fatalf("JWTSecret = %q", cfg.JWTSecret)
	}
	wantOrigins := []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	if len(cfg.AllowedOrigins) != len(wantOrigins) {
		t.Fatalf("AllowedOrigins = %#v", cfg.AllowedOrigins)
	}
	for i := range wantOrigins {
		if cfg.AllowedOrigins[i] != wantOrigins[i] {
			t.Fatalf("AllowedOrigins[%d] = %q; want %q", i, cfg.AllowedOrigins[i], wantOrigins[i])
		}
	}
	if cfg.HasBootstrapAdmin() {
		t.Fatal("development must not contain implicit bootstrap credentials")
	}
	if cfg.CookieSecure {
		t.Fatal("CookieSecure = true; development default should be false")
	}
	if cfg.CookieSameSite != "Lax" {
		t.Fatalf("CookieSameSite = %q", cfg.CookieSameSite)
	}
}

func TestLoadLoginRateLimitDefaults(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LoginRateLimitWindow != DefaultLoginRateLimitWindow {
		t.Fatalf("LoginRateLimitWindow = %v", cfg.LoginRateLimitWindow)
	}
	if cfg.LoginRateLimitIPMaxFailures != DefaultLoginRateLimitIPMaxFailures {
		t.Fatalf("LoginRateLimitIPMaxFailures = %d", cfg.LoginRateLimitIPMaxFailures)
	}
	if cfg.LoginRateLimitUsernameMaxFailures != DefaultLoginRateLimitUsernameMaxFailures {
		t.Fatalf("LoginRateLimitUsernameMaxFailures = %d", cfg.LoginRateLimitUsernameMaxFailures)
	}
}

func TestLoadAcceptsExplicitLoginRateLimit(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("LOGIN_RATE_LIMIT_WINDOW", "7m")
	t.Setenv("LOGIN_RATE_LIMIT_IP_MAX_FAILURES", "19")
	t.Setenv("LOGIN_RATE_LIMIT_USERNAME_MAX_FAILURES", "4")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LoginRateLimitWindow.String() != "7m0s" || cfg.LoginRateLimitIPMaxFailures != 19 || cfg.LoginRateLimitUsernameMaxFailures != 4 {
		t.Fatalf("login limiter config = %#v", cfg)
	}
}

func TestLoadRejectsInvalidLoginRateLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value string
	}{
		{"zero-ip", "LOGIN_RATE_LIMIT_IP_MAX_FAILURES", "0"},
		{"negative-user", "LOGIN_RATE_LIMIT_USERNAME_MAX_FAILURES", "-1"},
		{"bad-user", "LOGIN_RATE_LIMIT_USERNAME_MAX_FAILURES", "many"},
		{"zero-window", "LOGIN_RATE_LIMIT_WINDOW", "0s"},
		{"bad-window", "LOGIN_RATE_LIMIT_WINDOW", "soon"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("Load() error = %v; want %s failure", err, tc.key)
			}
		})
	}
}

func TestLoadProductionRequiresDatabaseURL(t *testing.T) {
	setProductionMinimum(t)
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("Load() error = %v; want DATABASE_URL failure", err)
	}
}

func TestLoadProductionRequiresJWTSecret(t *testing.T) {
	setProductionMinimum(t)
	t.Setenv("JWT_SECRET", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("Load() error = %v; want JWT_SECRET failure", err)
	}
}

func TestLoadProductionRejectsDevelopmentJWTSecret(t *testing.T) {
	setProductionMinimum(t)
	t.Setenv("JWT_SECRET", defaultDevJWTSecret)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "development default") {
		t.Fatalf("Load() error = %v; want development-secret failure", err)
	}
}

func TestLoadRejectsRemovedDemoSeedEnvironment(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("SEED_DEMO_USERS", "false")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("Load() error = %v; want removed demo seed failure", err)
	}
}

func TestLoadRejectsInlineBootstrapPassword(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "do-not-put-bootstrap-secrets-in-env")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "BOOTSTRAP_ADMIN_PASSWORD_FILE") {
		t.Fatalf("Load() error = %v; want password-file guidance", err)
	}
}

func TestLoadBootstrapPairRequired(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("BOOTSTRAP_ADMIN_USERNAME", "initial-admin")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "must be set together") {
		t.Fatalf("Load() error = %v; want bootstrap pair failure", err)
	}

	clearConfigEnv(t)
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD_FILE", writeBootstrapSecret(t, "strong-bootstrap-password\n"))
	_, err = Load()
	if err == nil || !strings.Contains(err.Error(), "must be set together") {
		t.Fatalf("Load() error = %v; want bootstrap pair failure", err)
	}
}

func TestLoadValidatesBootstrapPasswordFileBeforeDatabase(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("BOOTSTRAP_ADMIN_USERNAME", "initial-admin")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD_FILE", writeBootstrapSecret(t, "short\n"))
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("Load() error = %v; want password length failure", err)
	}
}

func TestLoadAcceptsStrongBootstrapSecretFile(t *testing.T) {
	clearConfigEnv(t)
	secretPath := writeBootstrapSecret(t, "strong-bootstrap-password-from-secret-file\n")
	t.Setenv("BOOTSTRAP_ADMIN_USERNAME", "initial-admin")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD_FILE", secretPath)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.HasBootstrapAdmin() || cfg.BootstrapAdminUsername != "initial-admin" || cfg.BootstrapAdminPasswordFile != secretPath {
		t.Fatalf("bootstrap config = %#v", cfg)
	}
	password, err := ReadBootstrapAdminPassword(secretPath)
	if err != nil {
		t.Fatalf("ReadBootstrapAdminPassword() error = %v", err)
	}
	if string(password) != "strong-bootstrap-password-from-secret-file" {
		t.Fatalf("password file line ending was not normalized")
	}
	zeroBytes(password)
}

func TestReadBootstrapAdminPasswordRejectsEmbeddedNewlineAndBcryptOverflow(t *testing.T) {
	for name, value := range map[string]string{
		"embedded-newline": "strong-bootstrap\npassword",
		"over-72-bytes":    strings.Repeat("x", BootstrapPasswordMaxBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeBootstrapSecret(t, value)
			if _, err := ReadBootstrapAdminPassword(path); err == nil {
				t.Fatalf("ReadBootstrapAdminPassword(%s) unexpectedly succeeded", name)
			}
		})
	}
}

func TestLoadRejectsWildcardOriginWithCredentialedCORS(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("ALLOWED_ORIGINS", "*")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("Load() error = %v; want wildcard origin failure", err)
	}
}

func TestLoadProductionRejectsWildcardOrigin(t *testing.T) {
	setProductionMinimum(t)
	for _, origins := range []string{"*", "https://app.example.com,https://*.example.com"} {
		t.Run(origins, func(t *testing.T) {
			t.Setenv("ALLOWED_ORIGINS", origins)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "wildcard") {
				t.Fatalf("Load() error = %v; want wildcard origin failure", err)
			}
		})
	}
}

func TestLoadProductionAllowsExplicitOriginList(t *testing.T) {
	setProductionMinimum(t)
	t.Setenv("ALLOWED_ORIGINS", "https://mes.example.com, https://admin.example.com,https://mes.example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []string{"https://mes.example.com", "https://admin.example.com"}
	if len(cfg.AllowedOrigins) != len(want) {
		t.Fatalf("AllowedOrigins = %#v", cfg.AllowedOrigins)
	}
	for i := range want {
		if cfg.AllowedOrigins[i] != want[i] {
			t.Fatalf("AllowedOrigins[%d] = %q; want %q", i, cfg.AllowedOrigins[i], want[i])
		}
	}
}

func TestLoadProductionDefaultsAreFailClosed(t *testing.T) {
	setProductionMinimum(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HasBootstrapAdmin() {
		t.Fatal("production must not contain implicit bootstrap credentials")
	}
	if !cfg.CookieSecure {
		t.Fatal("production CookieSecure = false")
	}
	if cfg.CookieSameSite != "Strict" {
		t.Fatalf("production CookieSameSite = %q", cfg.CookieSameSite)
	}
	if len(cfg.AllowedOrigins) != 0 {
		t.Fatalf("production AllowedOrigins = %#v; want same-origin/no CORS by default", cfg.AllowedOrigins)
	}
}

func TestLoadRejectsProductionCookieSecureFalse(t *testing.T) {
	setProductionMinimum(t)
	t.Setenv("COOKIE_SECURE", "false")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("Load() error = %v; want COOKIE_SECURE failure", err)
	}
}

func TestLoadRejectsInvalidEnvironmentAndBoolean(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("APP_ENV", "prod")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("invalid APP_ENV error = %v", err)
	}

	clearConfigEnv(t)
	t.Setenv("COOKIE_SECURE", "sometimes")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("invalid boolean error = %v", err)
	}
}

func TestLoadRejectsMalformedOrigin(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("ALLOWED_ORIGINS", "ftp://example.com")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("Load() error = %v; want origin scheme failure", err)
	}
}

func TestLoadSameSiteNoneRequiresSecure(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("COOKIE_SAMESITE", "None")
	t.Setenv("COOKIE_SECURE", "false")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("Load() error = %v; want SameSite=None secure failure", err)
	}
}

func TestLoadObservabilityDefaults(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.OTELExporterOTLPEndpoint != "" {
		t.Fatalf("OTELExporterOTLPEndpoint = %q; want disabled", cfg.OTELExporterOTLPEndpoint)
	}
	if cfg.OTELServiceName != "cpim-mes-backend" {
		t.Fatalf("OTELServiceName = %q", cfg.OTELServiceName)
	}
	if cfg.OTELExportTimeout != 5*time.Second {
		t.Fatalf("OTELExportTimeout = %v", cfg.OTELExportTimeout)
	}
	if cfg.OTELQueueSize != 256 {
		t.Fatalf("OTELQueueSize = %d", cfg.OTELQueueSize)
	}
}

func TestLoadAcceptsOptionalOTLPHTTPJSONConfiguration(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://collector.example.com:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Bearer-test")
	t.Setenv("OTEL_SERVICE_NAME", "cpim-mes-ci")
	t.Setenv("OTEL_EXPORT_TIMEOUT", "3s")
	t.Setenv("OTEL_BSP_MAX_QUEUE_SIZE", "64")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.OTELExporterOTLPEndpoint != "https://collector.example.com:4318" || cfg.OTELServiceName != "cpim-mes-ci" || cfg.OTELExportTimeout != 3*time.Second || cfg.OTELQueueSize != 64 {
		t.Fatalf("OTLP config = %#v", cfg)
	}
}

func TestLoadRejectsInvalidOTLPConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value string
	}{
		{"relative-endpoint", "OTEL_EXPORTER_OTLP_ENDPOINT", "collector:4318"},
		{"credential-endpoint", "OTEL_EXPORTER_OTLP_ENDPOINT", "https://user:pass@collector.example.com:4318"},
		{"zero-timeout", "OTEL_EXPORT_TIMEOUT", "0s"},
		{"zero-queue", "OTEL_BSP_MAX_QUEUE_SIZE", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() unexpectedly accepted %s=%q", tc.key, tc.value)
			}
		})
	}
}
