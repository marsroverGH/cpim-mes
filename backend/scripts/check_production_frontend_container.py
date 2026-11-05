#!/usr/bin/env python3
"""Static guard for v0.41.1 Step 4 — Production Frontend Container.

Uses only the Python standard library so the guard can run in a clean CI image.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
checks: list[tuple[str, bool, str]] = []


def check(name: str, ok: bool, detail: str = "") -> None:
    checks.append((name, ok, detail))


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def service_block(text: str, name: str) -> str:
    # Compose files in this repository use two-space service indentation.
    m = re.search(rf"(?ms)^  {re.escape(name)}:\n(.*?)(?=^  [A-Za-z0-9_.-]+:\n|\Z)", text)
    return m.group(1) if m else ""

prod_docker = read("frontend/Dockerfile")
dev_docker = read("frontend/Dockerfile.dev")
nginx = read("frontend/nginx.conf")
base_text = read("docker-compose.yml")
dev_text = read("docker-compose.dev.yml")
prod_text = read("docker-compose.prod.yml")
ci = read(".github/workflows/ci.yml")
api = read("frontend/src/api/index.ts")
doc = read("docs/PRODUCTION_FRONTEND_CONTAINER.md") if (ROOT / "docs/PRODUCTION_FRONTEND_CONTAINER.md").exists() else ""

base_db = service_block(base_text, "db")
base_backend = service_block(base_text, "backend")
dev_db = service_block(dev_text, "db")
dev_backend = service_block(dev_text, "backend")
dev_frontend = service_block(dev_text, "frontend")
prod_db = service_block(prod_text, "db")
prod_backend = service_block(prod_text, "backend")
prod_frontend = service_block(prod_text, "frontend")

# Production image must build static assets and run only Nginx.
check("production Dockerfile has Node build stage", "FROM node:24.20.0-alpine3.24 AS build" in prod_docker)
check("production Dockerfile installs from lockfile", "COPY package.json package-lock.json ./" in prod_docker and "RUN npm ci" in prod_docker)
check("production Dockerfile runs frontend build", "RUN npm run build" in prod_docker)
check("production Dockerfile has Nginx runtime stage", "FROM nginx:1.30.6-alpine3.24 AS runtime" in prod_docker)
check("production image copies only dist into web root", "COPY --from=build /app/dist /usr/share/nginx/html" in prod_docker)
check("production image does not launch Vite", 'CMD ["npm", "run", "dev"]' not in prod_docker and "vite --host" not in prod_docker)
check("production image exposes application HTTP port", "EXPOSE 8080" in prod_docker)
check("production image has local healthcheck", "http://127.0.0.1:8080/healthz" in prod_docker)

# Development image intentionally retains Vite.
check("development Dockerfile exists", (ROOT / "frontend/Dockerfile.dev").exists())
check("development Dockerfile uses locked npm install", "RUN npm ci" in dev_docker)
check("development Dockerfile launches Vite dev server", 'CMD ["npm", "run", "dev"]' in dev_docker)
check("development Dockerfile exposes 5173", "EXPOSE 5173" in dev_docker)

# Nginx same-origin reverse proxy + SPA semantics.
check("Nginx listens on 8080", "listen 8080;" in nginx)
check("Nginx has frontend health endpoint", "location = /healthz" in nginx and 'return 200 "ok\\n";' in nginx)
check("Nginx proxies API to internal backend", "location ^~ /api/" in nginx and "proxy_pass http://backend:8080;" in nginx)
check("Nginx preserves or supplies forwarded scheme", "proxy_set_header X-Forwarded-Proto $cpim_forwarded_proto;" in nginx and "$http_x_forwarded_proto" in nginx)
check("Nginx SPA fallback enabled", "try_files $uri $uri/ /index.html;" in nginx)
check("Nginx immutable cache for Vite assets", 'Cache-Control "public, immutable"' in nginx)
check("Nginx suppresses version tokens", "server_tokens off;" in nginx)

# Frontend browser API remains same-origin; no production build-time backend URL.
check("frontend API defaults to relative /api", "|| '/api'" in api)
check("production Dockerfile has no VITE_API_BASE", "VITE_API_BASE" not in prod_docker)
check("production Nginx has no localhost backend target", "localhost:8080" not in nginx)

# Compose topology: base is internal, dev publishes developer ports, prod only publishes proxy.
check("base database has no host ports", "\n    ports:" not in base_db)
check("base backend has no host ports", "\n    ports:" not in base_backend)
check("base does not run frontend implicitly", not service_block(base_text, "frontend"))
check("base backend has healthcheck", "healthcheck:" in base_backend)
check("development DB publishes 55433", '- "55433:5432"' in dev_db)
check("development backend publishes 8080", '- "8080:8080"' in dev_backend)
check("development frontend publishes 5173", '- "5173:5173"' in dev_frontend)
check("development frontend uses Dockerfile.dev", "dockerfile: Dockerfile.dev" in dev_frontend)
check("production DB has no host ports", "ports:" not in prod_db)
check("production backend has no host ports", "ports:" not in prod_backend)
check("production frontend is the only published service", '- "${CPIM_HTTP_PORT:-8080}:8080"' in prod_frontend)
check("production frontend uses static Dockerfile", "dockerfile: Dockerfile" in prod_frontend and "Dockerfile.dev" not in prod_frontend)
check("production backend is forced to APP_ENV=production", "APP_ENV: production" in prod_backend)
check("production requires DATABASE_URL", "${DATABASE_URL:?" in prod_backend)
check("production requires JWT_SECRET", "${JWT_SECRET:?" in prod_backend)
check("production requires PostgreSQL password", "${POSTGRES_PASSWORD:?" in prod_db)
check("production secure cookie is forced", 'COOKIE_SECURE: "true"' in prod_backend)
check("production same-origin CORS default is empty", "ALLOWED_ORIGINS: ${ALLOWED_ORIGINS:-}" in prod_backend)

# CI must build and exercise the production topology, not the Vite dev server.
check("CI builds production Compose topology", "docker-compose.prod.yml build --pull" in ci)
check("CI starts production Compose topology", "docker-compose.prod.yml -f docker-compose.bootstrap.yml up -d --build" in ci)
check("CI E2E uses production proxy URL", "E2E_BASE_URL: http://localhost:8080" in ci)
check("CI waits for proxied backend API", "http://localhost:8080/api/openapi.json" in ci)
check("CI no longer waits on Vite 5173", "curl -fsS http://localhost:5173" not in ci)
check("Step 4 guard is part of CI", "check_production_frontend_container.py" in ci)

# Documentation / schema invariants.
check("production frontend documentation exists", "Production Frontend Container" in doc)
check("documentation states DB and Backend are not host-published", "DB and Backend are not host-published" in doc)
migrations = sorted((ROOT / "backend/migrations").glob("*.sql"))
versions = [int(m.name[:4]) for m in migrations if re.match(r"^\d{4}_", m.name)]
check("migration count remains 41", len(versions) == 41, f"count={len(versions)}")
check("latest migration remains 0041", bool(versions) and max(versions) == 41, f"latest={max(versions) if versions else 'none'}")
check("migration 0042 absent", not any(v >= 42 for v in versions))

failed = [item for item in checks if not item[1]]
for name, ok, detail in checks:
    suffix = f" ({detail})" if detail else ""
    print(f"{'PASS' if ok else 'FAIL'}: {name}{suffix}")
print(f"\nProduction frontend container static guard: {len(checks)-len(failed)}/{len(checks)} PASS")
if failed:
    sys.exit(1)
