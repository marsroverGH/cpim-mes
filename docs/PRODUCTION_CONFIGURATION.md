# Production Configuration / Fail-Closed Startup Gate

Target: **v0.41.1 Production Hardening — Step 2, updated through Step 6**

The configuration gate runs before the backend opens a database connection. Step 3 extends the gate by removing fixed demo credentials and validating an optional initial-admin password file before DB network activity.

## Environment modes

`APP_ENV` accepts exactly:

- `development` (default)
- `test`
- `production`

Any other value is a startup error.

## Production-required behavior

With `APP_ENV=production`:

| Setting | Production behavior |
| --- | --- |
| `DATABASE_URL` | no fallback; empty/unset fails startup |
| `JWT_SECRET` | no fallback; empty/unset fails startup; development default is rejected |
| `ALLOWED_ORIGINS` | defaults to empty (no CORS); any value containing `*` is rejected |
| `COOKIE_SECURE` | defaults to `true`; `false` is rejected |
| `COOKIE_SAMESITE` | defaults to `Strict`; accepted values are `Strict`, `Lax`, `None` |
| `BOOTSTRAP_ADMIN_USERNAME` | optional unless `users` is empty; must be paired with password-file setting |
| `BOOTSTRAP_ADMIN_PASSWORD_FILE` | optional unless `users` is empty; readable 15-72 byte one-line secret required |
| `LOGIN_RATE_LIMIT_WINDOW` | positive duration; default `15m` |
| `LOGIN_RATE_LIMIT_IP_MAX_FAILURES` | positive integer; default `30` |
| `LOGIN_RATE_LIMIT_USERNAME_MAX_FAILURES` | positive integer; default `5` |

`COOKIE_SAMESITE=None` requires `COOKIE_SECURE=true` in all environments.

Step 3 removes `SEED_DEMO_USERS`. If the legacy variable is still present with a non-empty value, startup fails with an upgrade message. Inline `BOOTSTRAP_ADMIN_PASSWORD` is also rejected; use a secret file.

Step 5 consumes these settings for HttpOnly browser session cookies and signed CSRF protection. Browser JWT storage in Web Storage has been removed; CLI/API Bearer tokens remain available through `/api/auth/token`. Step 6 adds fail-closed validation for the login rate-limit window and independent IP/username failure thresholds.

## CORS policy

`ALLOWED_ORIGINS` is a comma-separated list. Duplicate entries are removed while preserving order.

Examples:

```text
ALLOWED_ORIGINS=https://mes.example.com
ALLOWED_ORIGINS=https://mes.example.com,https://admin.example.com
```

An empty production value means the CORS middleware is not installed, so browsers receive no cross-origin permission from the backend. Wildcard origins are rejected in every environment because credentialed CORS requires explicit origins.

Development retains CORS/cookie convenience defaults but no longer receives any user credentials:

```text
APP_ENV=development
ALLOWED_ORIGINS=http://localhost:5173,http://127.0.0.1:5173
COOKIE_SECURE=false
COOKIE_SAMESITE=Lax
```

## Authentication bootstrap boundary

There is no automatic demo-user seed in any environment.

After Migration Manager completion, startup counts `users`:

```text
users > 0  -> continue; bootstrap never changes existing users
users = 0  -> require explicit initial-admin bootstrap or FAIL startup
```

See [`BOOTSTRAP_ADMIN.md`](./BOOTSTRAP_ADMIN.md) for secret-file handling and Docker Compose bootstrap instructions.

## Startup order

```text
read environment
  -> validate configuration (fail closed)
  -> validate bootstrap secret file when configured
  -> open PostgreSQL
  -> migration gate
  -> initial-admin gate/bootstrap
  -> services/router
  -> HTTP listen
```

A basic configuration or bootstrap-secret-file failure therefore occurs before database network activity. The users-table emptiness check necessarily occurs after migrations because a clean DB does not have the table until migration `0003` is applied.

## Compose topology after Step 4

The base `docker-compose.yml` now defines only shared internal PostgreSQL and Backend services. It intentionally publishes neither service to the host.

Development adds `docker-compose.dev.yml`:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
```

Production adds `docker-compose.prod.yml`, which forces `APP_ENV=production`, forces `COOKIE_SECURE=true`, requires `POSTGRES_PASSWORD`, `DATABASE_URL`, and `JWT_SECRET`, and publishes only the Nginx frontend/reverse proxy:

```bash
docker compose --env-file .env.production -f docker-compose.yml -f docker-compose.prod.yml up -d --build
```

For a clean DB, add `-f docker-compose.bootstrap.yml` for the first startup only. See [`PRODUCTION_FRONTEND_CONTAINER.md`](./PRODUCTION_FRONTEND_CONTAINER.md).

Changing only `APP_ENV=production` on the base file remains intentionally invalid: the shared development defaults are rejected by the backend production validation gate. Production deployments must use the production override.

## Validation

Unit coverage is in:

```text
backend/internal/config/config_test.go
```

Static policy coverage is in:

```text
backend/scripts/check_production_config.py
backend/scripts/check_demo_credentials_removed.py
```

Run:

```bash
cd backend
go test -mod=readonly ./internal/config
cd ..
python3 backend/scripts/check_production_config.py
python3 backend/scripts/check_demo_credentials_removed.py
```

## Scope / invariants

Step 3 intentionally does not:

- add migration `0042`
- modify migrations `0001`-`0041`
- change MRP, scheduling, pegging, Control Tower, or Recovery Planning semantics
- add general user-administration/password-reset APIs (Step 7)
- change the production reverse-proxy topology beyond Step 4
