# CPIM-MES E2E

Playwright E2E has **no built-in login credentials**.

The stack must already contain an administrator, and the test process must receive:

```text
E2E_ADMIN_USERNAME=<test administrator username>
E2E_ADMIN_PASSWORD=<test administrator password>
```

For a clean Docker Compose database, create the same administrator through the one-time bootstrap path:

```bash
mkdir -p .secrets
# Write a unique 15-72 byte password to .secrets/bootstrap-admin-password.
export BOOTSTRAP_ADMIN_USERNAME=e2e-admin
export E2E_ADMIN_USERNAME="$BOOTSTRAP_ADMIN_USERNAME"
export E2E_ADMIN_PASSWORD="$(cat .secrets/bootstrap-admin-password)"

docker compose -f docker-compose.yml -f docker-compose.prod.yml -f docker-compose.bootstrap.yml up -d --build
cd e2e
npm ci
npm test
```

Delete `.secrets/bootstrap-admin-password` after the initial user exists. CI generates a fresh random secret for each run and masks the value in GitHub Actions logs.

CI runs E2E against the production Nginx path at `http://localhost:8080`, not the Vite dev server.

Tests are intentionally serialized (`--workers=1`) because several scenarios mutate shared planning state. Business API fixtures obtain explicit Bearer JWTs from `/api/auth/token`; browser-flow tests use `/api/auth/login` and verify HttpOnly/Secure cookies plus CSRF enforcement. No test stores a browser JWT in Web Storage. Step 6 also verifies per-username throttling, `429 Retry-After`, and dedicated authentication audit events. Step 7 adds User Administration coverage for admin-only management, last-active-admin protection, immediate role/deactivation effects, reset/change-password flows, and audit secret redaction. The current suite contains **26 Playwright tests**: the v0.41.0 baseline 18 tests plus 7 Production Hardening tests added in Steps 5-10.

## Step 9 — Version / Build Metadata

`version-build-metadata.spec.ts` verifies the public `/version` endpoint through the production Nginx path. CI injects the expected release version and Git commit and requires a parseable non-`unknown` UTC build time plus `migrationLatest: 42`.


## Step 10 / Step 13 production-boundary coverage

Step 10 adds the production observability boundary test: public `/metrics` must return 404 while the backend-private endpoint remains available to the release gate. Step 13 executes all **26 Playwright tests** against the same production Nginx path after an empty-volume `0001 -> 0041` migration, then runs the live backup/restore drill in the same clean-room stack.
