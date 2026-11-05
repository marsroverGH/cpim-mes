package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	EnvironmentDevelopment = "development"
	EnvironmentTest        = "test"
	EnvironmentProduction  = "production"

	defaultDevDatabaseURL    = "postgres://cpim:cpim@localhost:5432/cpim?sslmode=disable"
	defaultDevJWTSecret      = "dev-secret-change-me-in-production"
	defaultDevAllowedOrigins = "http://localhost:5173,http://127.0.0.1:5173"

	BootstrapPasswordMinBytes = 15
	BootstrapPasswordMaxBytes = 72 // bcrypt input limit
	bootstrapSecretMaxBytes   = 4096

	DefaultLoginRateLimitWindow              = 15 * time.Minute
	DefaultLoginRateLimitIPMaxFailures       = 30
	DefaultLoginRateLimitUsernameMaxFailures = 5
)

type Config struct {
	Environment                       string
	HTTPAddr                          string
	DatabaseURL                       string
	JWTSecret                         string
	AllowedOrigins                    []string
	BootstrapAdminUsername            string
	BootstrapAdminPasswordFile        string
	CookieSecure                      bool
	CookieSameSite                    string
	LoginRateLimitWindow              time.Duration
	LoginRateLimitIPMaxFailures       int
	LoginRateLimitUsernameMaxFailures int
	OTELExporterOTLPEndpoint          string
	OTELExporterOTLPHeaders           string
	OTELServiceName                   string
	OTELExportTimeout                 time.Duration
	OTELQueueSize                     int
	MigrationTimeout                  time.Duration
	MigrationInstalledBy              string
}

// Load reads environment configuration and validates it before any database or
// HTTP work starts. Production intentionally has no fallback DATABASE_URL or
// JWT_SECRET and rejects fail-open security settings. Step 3 permanently
// removes fixed demo credentials; initial administration uses a password file.
func Load() (Config, error) {
	if err := rejectRemovedCredentialEnvironment(); err != nil {
		return Config{}, err
	}

	environment := strings.ToLower(strings.TrimSpace(getenv("APP_ENV", EnvironmentDevelopment)))
	if environment == "" {
		environment = EnvironmentDevelopment
	}

	production := environment == EnvironmentProduction

	databaseDefault := defaultDevDatabaseURL
	jwtDefault := defaultDevJWTSecret
	allowedOriginsDefault := defaultDevAllowedOrigins
	cookieSecureDefault := false
	cookieSameSiteDefault := "Lax"
	if production {
		databaseDefault = ""
		jwtDefault = ""
		allowedOriginsDefault = ""
		cookieSecureDefault = true
		cookieSameSiteDefault = "Strict"
	}

	cookieSecure, err := getBool("COOKIE_SECURE", cookieSecureDefault)
	if err != nil {
		return Config{}, err
	}
	loginWindow, err := getPositiveDuration("LOGIN_RATE_LIMIT_WINDOW", DefaultLoginRateLimitWindow)
	if err != nil {
		return Config{}, err
	}
	loginIPMax, err := getPositiveInt("LOGIN_RATE_LIMIT_IP_MAX_FAILURES", DefaultLoginRateLimitIPMaxFailures)
	if err != nil {
		return Config{}, err
	}
	loginUsernameMax, err := getPositiveInt("LOGIN_RATE_LIMIT_USERNAME_MAX_FAILURES", DefaultLoginRateLimitUsernameMaxFailures)
	if err != nil {
		return Config{}, err
	}
	otelTimeout, err := getPositiveDuration("OTEL_EXPORT_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	otelQueueSize, err := getPositiveInt("OTEL_BSP_MAX_QUEUE_SIZE", 256)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Environment:                       environment,
		HTTPAddr:                          getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:                       strings.TrimSpace(getenv("DATABASE_URL", databaseDefault)),
		JWTSecret:                         strings.TrimSpace(getenv("JWT_SECRET", jwtDefault)),
		AllowedOrigins:                    splitCSV(getenv("ALLOWED_ORIGINS", allowedOriginsDefault)),
		BootstrapAdminUsername:            strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_USERNAME")),
		BootstrapAdminPasswordFile:        strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_PASSWORD_FILE")),
		CookieSecure:                      cookieSecure,
		CookieSameSite:                    canonicalSameSite(getenv("COOKIE_SAMESITE", cookieSameSiteDefault)),
		LoginRateLimitWindow:              loginWindow,
		LoginRateLimitIPMaxFailures:       loginIPMax,
		LoginRateLimitUsernameMaxFailures: loginUsernameMax,
		OTELExporterOTLPEndpoint:          strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		OTELExporterOTLPHeaders:           strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")),
		OTELServiceName:                   strings.TrimSpace(getenv("OTEL_SERVICE_NAME", "cpim-mes-backend")),
		OTELExportTimeout:                 otelTimeout,
		OTELQueueSize:                     otelQueueSize,
		MigrationTimeout:                  getDuration("MIGRATION_TIMEOUT", 10*time.Minute),
		MigrationInstalledBy:              getenv("MIGRATION_INSTALLED_BY", "backend-startup"),
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if cfg.HasBootstrapAdmin() {
		// Validate readability/strength before any database network activity. The
		// password is intentionally not retained in Config or emitted to logs.
		secret, err := ReadBootstrapAdminPassword(cfg.BootstrapAdminPasswordFile)
		if err != nil {
			return Config{}, err
		}
		zeroBytes(secret)
	}
	return cfg, nil
}

func (c Config) IsProduction() bool {
	return c.Environment == EnvironmentProduction
}

func (c Config) HasBootstrapAdmin() bool {
	return c.BootstrapAdminUsername != "" && c.BootstrapAdminPasswordFile != ""
}

func (c Config) Validate() error {
	var errs []error

	switch c.Environment {
	case EnvironmentDevelopment, EnvironmentTest, EnvironmentProduction:
	default:
		errs = append(errs, fmt.Errorf("APP_ENV must be one of %q, %q, or %q", EnvironmentDevelopment, EnvironmentTest, EnvironmentProduction))
	}

	if strings.TrimSpace(c.HTTPAddr) == "" {
		errs = append(errs, errors.New("HTTP_ADDR must not be empty"))
	}
	if c.MigrationTimeout <= 0 {
		errs = append(errs, errors.New("MIGRATION_TIMEOUT must be greater than zero"))
	}
	if strings.TrimSpace(c.MigrationInstalledBy) == "" {
		errs = append(errs, errors.New("MIGRATION_INSTALLED_BY must not be empty"))
	}

	for _, origin := range c.AllowedOrigins {
		if err := validateOrigin(origin); err != nil {
			errs = append(errs, fmt.Errorf("ALLOWED_ORIGINS: %w", err))
		}
	}

	switch c.CookieSameSite {
	case "Strict", "Lax", "None":
	default:
		errs = append(errs, errors.New("COOKIE_SAMESITE must be Strict, Lax, or None"))
	}
	if c.CookieSameSite == "None" && !c.CookieSecure {
		errs = append(errs, errors.New("COOKIE_SECURE must be true when COOKIE_SAMESITE=None"))
	}
	if c.LoginRateLimitWindow <= 0 {
		errs = append(errs, errors.New("LOGIN_RATE_LIMIT_WINDOW must be greater than zero"))
	}
	if c.LoginRateLimitIPMaxFailures <= 0 {
		errs = append(errs, errors.New("LOGIN_RATE_LIMIT_IP_MAX_FAILURES must be greater than zero"))
	}
	if c.LoginRateLimitUsernameMaxFailures <= 0 {
		errs = append(errs, errors.New("LOGIN_RATE_LIMIT_USERNAME_MAX_FAILURES must be greater than zero"))
	}
	if c.OTELExportTimeout <= 0 {
		errs = append(errs, errors.New("OTEL_EXPORT_TIMEOUT must be greater than zero"))
	}
	if c.OTELQueueSize <= 0 {
		errs = append(errs, errors.New("OTEL_BSP_MAX_QUEUE_SIZE must be greater than zero"))
	}
	if strings.TrimSpace(c.OTELServiceName) == "" {
		errs = append(errs, errors.New("OTEL_SERVICE_NAME must not be empty"))
	}
	if endpoint := strings.TrimSpace(c.OTELExporterOTLPEndpoint); endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, errors.New("OTEL_EXPORTER_OTLP_ENDPOINT must be an absolute http(s) URL"))
		} else if u.User != nil {
			errs = append(errs, errors.New("OTEL_EXPORTER_OTLP_ENDPOINT must not contain embedded credentials"))
		}
	}
	if strings.ContainsAny(c.OTELExporterOTLPHeaders, "\r\n") {
		errs = append(errs, errors.New("OTEL_EXPORTER_OTLP_HEADERS must not contain CR/LF"))
	}

	bootstrapUsernameSet := c.BootstrapAdminUsername != ""
	bootstrapPasswordFileSet := c.BootstrapAdminPasswordFile != ""
	if bootstrapUsernameSet != bootstrapPasswordFileSet {
		errs = append(errs, errors.New("BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD_FILE must be set together"))
	}
	if bootstrapUsernameSet {
		if err := validateBootstrapUsername(c.BootstrapAdminUsername); err != nil {
			errs = append(errs, err)
		}
	}

	if c.IsProduction() {
		if strings.TrimSpace(c.DatabaseURL) == "" {
			errs = append(errs, errors.New("DATABASE_URL must be explicitly set in production"))
		}
		if strings.TrimSpace(c.JWTSecret) == "" {
			errs = append(errs, errors.New("JWT_SECRET must be explicitly set in production"))
		}
		if c.JWTSecret == defaultDevJWTSecret {
			errs = append(errs, errors.New("JWT_SECRET must not use the development default in production"))
		}
		if !c.CookieSecure {
			errs = append(errs, errors.New("COOKIE_SECURE must be true in production"))
		}
		for _, origin := range c.AllowedOrigins {
			if strings.Contains(origin, "*") {
				errs = append(errs, errors.New("ALLOWED_ORIGINS must not contain wildcard origins in production"))
				break
			}
		}
	}

	return errors.Join(errs...)
}

// ReadBootstrapAdminPassword reads a one-line secret from a file. A single
// trailing LF/CRLF (common for Docker/Kubernetes secret files) is removed;
// other whitespace is part of the password. The returned byte slice is owned
// by the caller and should be zeroed as soon as bcrypt hashing is complete.
func ReadBootstrapAdminPassword(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("BOOTSTRAP_ADMIN_PASSWORD_FILE must not be empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD_FILE cannot be read: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("BOOTSTRAP_ADMIN_PASSWORD_FILE must reference a regular file")
	}
	if info.Size() > bootstrapSecretMaxBytes {
		return nil, fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD_FILE exceeds %d bytes", bootstrapSecretMaxBytes)
	}
	secret, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD_FILE cannot be read: %w", err)
	}
	secret = trimOneLineEnding(secret)
	if err := validateBootstrapPassword(secret); err != nil {
		zeroBytes(secret)
		return nil, err
	}
	return secret, nil
}

func validateBootstrapPassword(password []byte) error {
	if len(password) < BootstrapPasswordMinBytes {
		return fmt.Errorf("bootstrap admin password must be at least %d bytes", BootstrapPasswordMinBytes)
	}
	if len(password) > BootstrapPasswordMaxBytes {
		return fmt.Errorf("bootstrap admin password must be at most %d bytes for bcrypt", BootstrapPasswordMaxBytes)
	}
	for _, b := range password {
		if b == 0 || b == '\n' || b == '\r' {
			return errors.New("bootstrap admin password must be a single line without NUL bytes")
		}
	}
	return nil
}

func validateBootstrapUsername(username string) error {
	if username == "" {
		return errors.New("BOOTSTRAP_ADMIN_USERNAME must not be empty")
	}
	if len([]byte(username)) > 128 {
		return errors.New("BOOTSTRAP_ADMIN_USERNAME must be at most 128 bytes")
	}
	for _, r := range username {
		if unicode.IsControl(r) {
			return errors.New("BOOTSTRAP_ADMIN_USERNAME must not contain control characters")
		}
	}
	return nil
}

func rejectRemovedCredentialEnvironment() error {
	var errs []error
	if v, ok := os.LookupEnv("SEED_DEMO_USERS"); ok && strings.TrimSpace(v) != "" {
		errs = append(errs, errors.New("SEED_DEMO_USERS is removed in v0.41.1 Step 3; fixed demo users are no longer supported"))
	}
	if v, ok := os.LookupEnv("BOOTSTRAP_ADMIN_PASSWORD"); ok && strings.TrimSpace(v) != "" {
		errs = append(errs, errors.New("BOOTSTRAP_ADMIN_PASSWORD is not supported; use BOOTSTRAP_ADMIN_PASSWORD_FILE so the password is not carried in process environment"))
	}
	return errors.Join(errs...)
}

func getenv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}

func getBool(k string, def bool) (bool, error) {
	v, ok := os.LookupEnv(k)
	if !ok || strings.TrimSpace(v) == "" {
		return def, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", k, err)
	}
	return parsed, nil
}

func getPositiveInt(k string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", k)
	}
	return n, nil
}

func getPositiveDuration(k string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", k)
	}
	return d, nil
}

func getDuration(k string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, raw := range strings.Split(v, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func canonicalSameSite(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "strict":
		return "Strict"
	case "lax":
		return "Lax"
	case "none":
		return "None"
	default:
		return strings.TrimSpace(v)
	}
}

func validateOrigin(origin string) error {
	if strings.Contains(origin, "*") {
		return fmt.Errorf("origin %q must not contain wildcard; credentialed CORS requires explicit origins", origin)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("invalid origin %q: %w", origin, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("origin %q must use http or https", origin)
	}
	if u.Host == "" {
		return fmt.Errorf("origin %q must include a host", origin)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("origin %q must not include userinfo, path, query, or fragment", origin)
	}
	return nil
}

func trimOneLineEnding(secret []byte) []byte {
	if len(secret) > 0 && secret[len(secret)-1] == '\n' {
		secret = secret[:len(secret)-1]
		if len(secret) > 0 && secret[len(secret)-1] == '\r' {
			secret = secret[:len(secret)-1]
		}
	}
	return secret
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
