# Production Frontend Container

Target: **v0.41.1 Production Hardening — Step 4**

Step 4 removes the Vite development server from the production runtime. The browser receives a pre-built Vue/Vuetify/Vite `dist` tree from Nginx, and Nginx forwards same-origin `/api` requests to the internal Go backend.

## Production topology

```text
Browser / TLS terminator
        |
        | HTTP(S) to published application port
        v
Nginx frontend :8080        <-- only host-published Compose service
        |
        +---- static / assets / SPA fallback
        |
        +---- /api/* --------------------+
                                      Go backend :8080
                                             |
                                      PostgreSQL :5432
```

**DB and Backend are not host-published** in the production Compose topology. They are reachable only over the Compose network. The only published service is the Nginx frontend/reverse proxy.

TLS is expected to terminate at the deployment edge (load balancer, ingress, host reverse proxy, or a later dedicated TLS configuration). Production configuration requires `COOKIE_SECURE=true`; Step 5 now uses that setting for `__Host-` HttpOnly session cookies and signed CSRF cookies.

## Image split

### `frontend/Dockerfile` — production

Multi-stage build:

```text
node:24.20.0-alpine3.24
  -> npm ci
  -> npm run build
  -> /app/dist

nginx:1.30.4-alpine3.24
  -> copy dist only
  -> listen :8080
```

The production runtime does not contain the Vite dev server and does not run `npm run dev`.

### `frontend/Dockerfile.dev` — development only

Development retains Vite with hot reload on port 5173. It proxies `/api` to `http://backend:8080` through `vite.config.ts`.

## Nginx routing

`frontend/nginx.conf` implements:

- `/healthz` — local frontend container health
- `/api/*` — reverse proxy to `http://backend:8080`
- `/assets/*` — immutable long-lived caching for Vite hashed assets
- `/` — SPA fallback using `try_files $uri $uri/ /index.html`
- basic response hardening headers
- `server_tokens off`

The production frontend is built without `VITE_API_BASE`. `frontend/src/api/index.ts` therefore keeps `/api` as the browser base URL, making API traffic same-origin.

## Compose files

### Shared internal services

```text
docker-compose.yml
```

Defines PostgreSQL and Backend but publishes neither service to the host.

### Development

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
```

Development host ports:

```text
PostgreSQL  localhost:55433
Backend     localhost:8080
Frontend    localhost:5173
```

For a clean DB, add the one-time bootstrap override:

```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.dev.yml \
  -f docker-compose.bootstrap.yml \
  up -d --build
```

## Production

Create deployment secrets/config separately from source control. At minimum:

```text
POSTGRES_PASSWORD=<strong database password>
DATABASE_URL=postgres://cpim:<url-encoded-password>@db:5432/cpim?sslmode=disable
JWT_SECRET=<high-entropy application secret>
CPIM_HTTP_PORT=8080
ALLOWED_ORIGINS=
COOKIE_SAMESITE=Strict
```

`docker-compose.prod.yml` forces `APP_ENV=production` and `COOKIE_SECURE=true`. `POSTGRES_PASSWORD`, `DATABASE_URL`, and `JWT_SECRET` are Compose-required values; missing values prevent Compose from constructing the production configuration.

Existing DB with at least one user:

```bash
docker compose \
  --env-file .env.production \
  -f docker-compose.yml \
  -f docker-compose.prod.yml \
  up -d --build
```

Clean DB / first administrator only:

```bash
mkdir -p .secrets
openssl rand -base64 36 | tr -d '\n' > .secrets/bootstrap-admin-password
chmod 600 .secrets/bootstrap-admin-password
export BOOTSTRAP_ADMIN_USERNAME=initial-admin

docker compose \
  --env-file .env.production \
  -f docker-compose.yml \
  -f docker-compose.prod.yml \
  -f docker-compose.bootstrap.yml \
  up -d --build
```

After the administrator exists, remove the bootstrap secret file and restart without `docker-compose.bootstrap.yml`.

## Production exposure invariant

Production must satisfy:

```text
host -> frontend:8080
host -X-> backend:8080
host -X-> db:5432
```

Do not add `ports:` to production `db` or `backend`. If administrative DB access is needed, use an explicitly controlled operational channel (for example a bastion/SSH tunnel) rather than permanently publishing the database port.

## CI coverage

CI now:

1. builds frontend assets with the locked Node/npm runtime;
2. builds the production Compose images;
3. starts `docker-compose.yml + docker-compose.prod.yml + bootstrap`;
4. waits on `http://localhost:8080/healthz`;
5. verifies backend reachability through `http://localhost:8080/api/openapi.json`;
6. runs the existing Playwright suite against `http://localhost:8080`.

Thus E2E exercises the same Nginx/static/reverse-proxy path used by production rather than the Vite development server.

## Validation

```bash
python3 scripts/check_runtime_versions.py
python3 backend/scripts/check_production_config.py
python3 backend/scripts/check_demo_credentials_removed.py
python3 backend/scripts/check_production_frontend_container.py
python3 scripts/check_ci_dependency_policy.py
```

When Docker is available:

```bash
POSTGRES_PASSWORD='ci-db-password' \
DATABASE_URL='postgres://cpim:ci-db-password@db:5432/cpim?sslmode=disable' \
JWT_SECRET='ci-only-secret-not-for-production' \
docker compose -f docker-compose.yml -f docker-compose.prod.yml config
```

## Scope / invariants

Step 4 intentionally does not:

- add migration `0042`;
- modify migrations `0001`-`0041`;
- change MRP, Detailed Scheduling, Pegging, Control Tower, or Recovery Planning semantics;
- weaken the Step 5 HttpOnly Cookie / CSRF authentication boundary;
- add TLS certificate automation;
- expose PostgreSQL or Backend directly in production.
