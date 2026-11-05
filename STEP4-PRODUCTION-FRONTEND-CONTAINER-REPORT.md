# CPIM-MES v0.41.1 Step 4 — Production Frontend Container 実装レポート

- 実装日: 2026-08-29
- 基準: v0.41.1 Step 3 Demo Credentials Removal 完成版
- 対象: v0.41.1 Production Hardening — Step 4
- Migration: 0001〜0041 / 41本（変更なし）
- Migration 0042: 作成なし

## 1. 目的

ProductionでVite development serverを実行する構成を廃止し、Vue/Vuetify/Viteをbuildした静的成果物をNginxで配信する。Browserからの `/api` は同一originのNginxを経由して内部Go Backendへreverse proxyする。

ProductionではPostgreSQLとBackendをhostへ公開せず、Frontend/Nginxだけを公開する。

## 2. 実装内容

### 2.1 Production Frontend Dockerfile

`frontend/Dockerfile` をmulti-stage化。

```text
node:24.20.0-alpine3.24 AS build
  -> npm@11.19.0
  -> npm ci
  -> npm run build

nginx:1.30.4-alpine3.24 AS runtime
  -> /app/dist only
  -> listen :8080
```

Production runtimeから `npm run dev` / Vite serverを除去した。

### 2.2 Development Dockerfile

旧Vite runtimeを `frontend/Dockerfile.dev` へ分離。

```text
Node 24.20.0
npm 11.19.0
npm ci
npm run dev
port 5173
```

### 2.3 Nginx reverse proxy

新規 `frontend/nginx.conf`。

- `/healthz` -> Nginx container health
- `/api/*` -> `http://backend:8080`
- `/assets/*` -> hashed asset long cache + immutable
- `/` -> SPA fallback `try_files $uri $uri/ /index.html`
- `server_tokens off`
- `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`
- proxy forwarding headers

Production buildでは `VITE_API_BASE` を注入せず、Frontendの既存 `/api` relative baseを維持する。

### 2.4 Docker Compose分離

`docker-compose.yml`:

- shared PostgreSQL + Backend
- DB host portなし
- Backend host portなし
- Backend healthcheck追加
- Frontendは定義しない

`docker-compose.dev.yml` 新規:

```text
DB       55433:5432
Backend  8080:8080
Frontend 5173:5173
Frontend Dockerfile.dev
```

`docker-compose.prod.yml` 新規:

```text
DB       host publishなし
Backend  host publishなし
Frontend ${CPIM_HTTP_PORT:-8080}:8080
```

Production overrideは以下を強制/要求する。

```text
APP_ENV=production
POSTGRES_PASSWORD required
DATABASE_URL required
JWT_SECRET required
COOKIE_SECURE=true
ALLOWED_ORIGINS default empty
COOKIE_SAMESITE default Strict
```

`docker-compose.locktest.yml` は従来のclean-room test portを維持しつつ、Step 4のbase分離へ対応した。

### 2.5 CI/E2E

GitHub ActionsをProduction topology検証へ変更。

Docker build:

```text
docker-compose.yml + docker-compose.prod.yml
```

E2E startup:

```text
docker-compose.yml
+ docker-compose.prod.yml
+ docker-compose.bootstrap.yml
```

E2E browser endpoint:

```text
http://localhost:8080
```

Health/Proxy gate:

```text
GET /healthz
GET /api/openapi.json
```

これによりPlaywrightはVite dev serverではなく、Production Nginx + reverse proxy pathを通る。

### 2.6 Static Guard

新規:

```text
backend/scripts/check_production_frontend_container.py
```

51 checksで以下をfail-closed検査する。

- Production Docker multi-stage
- Vite dev server不存在
- Nginx reverse proxy
- SPA fallback
- same-origin `/api`
- Production DB/Backend host port不存在
- Production frontendのみhost publish
- Production required secrets/config
- CI production image build
- CI E2E production proxy path
- Migration 0041 invariant

`Makefile` / CIへ追加した。

## 3. Runtime baseline

Step 4時点:

```text
Go          1.26.10
Node.js     24.20.0 LTS
npm         11.19.0
PostgreSQL  16.15
Nginx       1.30.4 stable
Alpine      3.24
```

Nginx 1.30.4 stable imageは2026-08-29時点のDocker Official Image stable tagとして確認した。

## 4. Validation

### PASS

```text
Production Frontend Container Guard: 51/51 PASS
Production Config Guard:            41/41 PASS
Demo Credentials Guard:             41/41 PASS
0039 Static Guard:                  106 checks PASS
0040 Static Guard:                  95/95 PASS
0041 Static Guard:                  98/98 PASS
Runtime version policy:             PASS
Dependency lock check:              PASS
CI/dependency policy:               PASS
All backend/scripts/check_*.py:      PASS
```

YAML parse:

```text
docker-compose.yml                 PASS
docker-compose.dev.yml             PASS
docker-compose.prod.yml            PASS
docker-compose.bootstrap.yml       PASS
docker-compose.locktest.yml        PASS
.github/workflows/ci.yml           PASS
```

Nginx configuration syntax:

```text
syntax is ok
test is successful
```

検査環境のNginx 1.26.3で、Compose service名 `backend` の解決だけ127.0.0.1へ置換したsyntax-only validationを実施した。

### Business/domain tree invariant

Step 3基準とSHA-256 tree comparison:

```text
backend/internal/service      UNCHANGED
backend/internal/domain       UNCHANGED
backend/internal/repository   UNCHANGED
backend/migrations            UNCHANGED
frontend/src                  UNCHANGED
e2e/tests                     UNCHANGED
```

Migration:

```text
count  = 41
latest = 0041
0042   = absent
```

## 5. この実行環境で実行できなかったGate

この作業環境のruntimeは:

```text
Node v22.16.0 / npm 10.9.2
Go 1.23.2
Docker daemon/toolingなし
```

ProjectはStep 1でNode 24.x/npm 11.x/Go 1.26.xをfail-closed要求するため、この環境で `npm ci` は期待どおりEBADENGINEで拒否された。

したがって以下は開発MacまたはGitHub Actionsで最終実行する。

```text
Go 1.26.10: go mod verify / go vet / go test -race
Node 24.20.0 + npm 11.19.0: npm ci / typecheck / production build
Docker: production image build
Docker: production Compose config/start
Playwright: existing Full E2E
```

CI定義はこれらをProduction topologyで実行するよう更新済み。

## 6. Production起動例

`.env.production` をsource control外に用意する。

```text
POSTGRES_USER=cpim
POSTGRES_PASSWORD=<strong secret>
POSTGRES_DB=cpim
DATABASE_URL=postgres://cpim:<url-encoded-secret>@db:5432/cpim?sslmode=disable
JWT_SECRET=<high entropy secret>
CPIM_HTTP_PORT=8080
ALLOWED_ORIGINS=
COOKIE_SAMESITE=Strict
```

既存usersあり:

```bash
docker compose \
  --env-file .env.production \
  -f docker-compose.yml \
  -f docker-compose.prod.yml \
  up -d --build
```

clean DB初回:

```bash
docker compose \
  --env-file .env.production \
  -f docker-compose.yml \
  -f docker-compose.prod.yml \
  -f docker-compose.bootstrap.yml \
  up -d --build
```

## 7. Scope boundary

Step 4では以下を変更していない。

- Migration / DB schema
- MRP
- ATP / CTP
- Detailed Scheduling
- Full Pegging
- Dynamic Rescheduling
- Production Control Tower
- Scenario-Based Recovery Planning
- JWT localStorage transport（Step 5対象）
- User Administration（Step 7対象）
- FAILED Recovery Run persistence（Step 8対象）

0041 Simulation/Publish side-effect-free invariantも変更なし。

## 8. 次Step

次は **Step 5 — Cookie Auth / CORS / CSRF**。

Browser認証をlocalStorage JWTからHttpOnly/Secure/SameSite cookieへ移し、same-origin Production topologyを前提にCSRF defenseを追加する。
