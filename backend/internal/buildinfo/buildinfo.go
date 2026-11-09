package buildinfo

import "strings"

// These values are intentionally variables so production builds can inject
// immutable release metadata with -ldflags -X without adding runtime config.
// Source defaults remain useful for local `go run` / unit tests.
var (
	Version   = "v0.44.0-dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Info is the public, non-secret identity of the running binary.
// MigrationLatest is supplied by the startup Migration Manager so /version
// reports the schema version this binary actually validated before serving.
type Info struct {
	Version         string `json:"version"`
	Commit          string `json:"commit"`
	BuildTime       string `json:"buildTime"`
	MigrationLatest int    `json:"migrationLatest"`
}

func Current(migrationLatest int) Info {
	return Info{
		Version:         valueOr(Version, "v0.44.0-dev"),
		Commit:          valueOr(Commit, "unknown"),
		BuildTime:       valueOr(BuildTime, "unknown"),
		MigrationLatest: migrationLatest,
	}
}

func valueOr(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
