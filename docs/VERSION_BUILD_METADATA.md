# Step 9 — Version / Build Metadata

CPIM-MES v0.41.1 Production Hardening Step 9 adds an explicit identity for the running release without changing database schema or planning semantics.

## Public endpoint

```http
GET /version
```

The endpoint is unauthenticated and returns only non-secret build identity:

```json
{
  "version": "v0.41.1",
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "buildTime": "2026-11-05T12:34:56Z",
  "migrationLatest": 41
}
```

`Cache-Control: no-store` is returned so a deployment check does not read stale metadata from a browser or intermediary cache.

`migrationLatest` is not a manually maintained HTTP constant. The server takes it from the startup Migration Manager result after the binary has validated/applied embedded migrations and before HTTP serving begins.

## Backend build injection

The backend source defaults are development-safe:

```text
Version   = v0.41.1-dev
Commit    = unknown
BuildTime = unknown
```

Production container builds inject immutable values with Go `-ldflags -X`:

```text
RELEASE_VERSION -> buildinfo.Version
VCS_REF          -> buildinfo.Commit
BUILD_TIME       -> buildinfo.BuildTime
```

The same three values are also attached to OCI image labels:

```text
org.opencontainers.image.version
org.opencontainers.image.revision
org.opencontainers.image.created
```

## Frontend build identity

`frontend/package.json` is aligned to release version `0.41.1`. The production frontend image receives the same build args and compiles them into Vite values:

```text
VITE_APP_VERSION
VITE_APP_COMMIT
VITE_APP_BUILD_TIME
```

The navigation drawer displays the release version. Commit and build time remain available to the compiled frontend build-info module without exposing authentication material or runtime secrets.

## Production Nginx path

Production exposes `/version` through the same public Nginx endpoint as the SPA. Nginx proxies exactly `/version` to `backend:8080/version`; the database and backend ports remain unexposed on the host.

## CI release gate

GitHub Actions injects:

```text
RELEASE_VERSION=v0.41.1
VCS_REF=$GITHUB_SHA
BUILD_TIME=<UTC RFC3339 timestamp>
```

The Playwright Step 9 test calls the production Nginx endpoint and verifies:

- HTTP 200
- `Cache-Control: no-store`
- expected release version
- exact GitHub commit
- parseable non-`unknown` UTC build time
- `migrationLatest = 41`

The static Step 9 guard also requires that Migration 0042 is absent and that the latest migration remains 0041.

## Local development

`go run` and Vite development builds may report `v0.41.1-dev` / `unknown`. This is deliberate: local source execution is not a release artifact. Container/CI release builds are the authoritative path for immutable build identity.
