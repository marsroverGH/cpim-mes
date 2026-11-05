package buildinfo

import "testing"

func TestCurrentIncludesMigrationLatestAndTrimsInjectedValues(t *testing.T) {
	oldVersion, oldCommit, oldBuildTime := Version, Commit, BuildTime
	t.Cleanup(func() {
		Version, Commit, BuildTime = oldVersion, oldCommit, oldBuildTime
	})

	Version = " v0.41.1 "
	Commit = " abcdef123456 "
	BuildTime = " 2026-11-05T12:34:56Z "

	got := Current(41)
	if got.Version != "v0.41.1" || got.Commit != "abcdef123456" || got.BuildTime != "2026-11-05T12:34:56Z" || got.MigrationLatest != 41 {
		t.Fatalf("unexpected build info: %+v", got)
	}
}

func TestCurrentFallsBackWhenLdflagValueIsBlank(t *testing.T) {
	oldVersion, oldCommit, oldBuildTime := Version, Commit, BuildTime
	t.Cleanup(func() {
		Version, Commit, BuildTime = oldVersion, oldCommit, oldBuildTime
	})

	Version, Commit, BuildTime = " ", "", "\t"
	got := Current(41)
	if got.Version != "v0.41.1-dev" || got.Commit != "unknown" || got.BuildTime != "unknown" {
		t.Fatalf("unexpected fallback build info: %+v", got)
	}
}
