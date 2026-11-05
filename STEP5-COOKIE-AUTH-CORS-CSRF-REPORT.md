# CPIM-MES v0.41.1 Step 5 — Cookie Auth / CORS / CSRF 実装レポート

- 基準: v0.41.1 Step 4 Production Frontend Container 完成版
- 対象: v0.41.1 Production Hardening Step 5
- Migration: 0001-0041 / 41本
- Migration 0042: 作成なし
- 業務Planning semantics: 変更なし

## 1. 実装目的

Browser認証をJavaScriptから読めるlocalStorage JWTから切り離し、ProductionではHttpOnly/Secure/SameSite Cookieを使用する。Cookie認証によって生じるCSRFリスクに対して、SameSiteだけに依存しない明示的なCSRF検証を導入する。CLI/API向けBearer JWTは互換経路として維持する。

## 2. Browser認証

`POST /api/auth/login` はusername/passwordを検証後、JWTをJSONに返さずCookieへ格納する。

Production cookie:

```text
__Host-cpim-session
  HttpOnly
  Secure
  Path=/
  SameSite=<COOKIE_SAMESITE; default Strict>
  Max-Age=8h

__Host-cpim-csrf
  JavaScript-readable (CSRF token only)
  Secure
  Path=/
  SameSite=<same policy>
  Max-Age=8h
```

DevelopmentではHTTPSなしでも動作できるよう、prefixなしの `cpim-session` / `cpim-csrf` を使う。

`GET /api/auth/me` でSPAの認証状態を復元し、`POST /api/auth/logout` で両Cookieを失効させる。

## 3. CLI/API Bearer互換

新しい明示endpoint:

```text
POST /api/auth/token
```

のみがJWTをresponse bodyへ返す。既存API呼び出しは引き続き次を利用できる。

```text
Authorization: Bearer <jwt>
```

Bearer認証はBrowser Cookie認証ではないため、CSRF headerを要求しない。

## 4. CSRF防御

Cookie認証されたunsafe methodに以下を要求する。

```text
POST / PUT / PATCH / DELETE / その他non-safe method
```

検証内容:

1. HttpOnly session cookieによる有効JWT認証
2. `X-CSRF-Token` header
3. CSRF cookieとのconstant-time一致
4. JWT sessionへHMAC-SHA256でbindされたsigned double-submit token検証
5. `Sec-Fetch-Site: cross-site` をreject
6. `Origin` が存在する場合、same-originまたは明示 `ALLOWED_ORIGINS` のみ許可

CSRF tokenは32-byte CSPRNG nonceを使い、JWT + nonceにHMACを付与する。したがってSameSite Cookieは追加防御であり、唯一のCSRF防御ではない。

Browser login自体もOriginが存在する場合はtrusted originを要求する。

## 5. CORS hardening

Credentialed CORSへ変更:

```text
AllowCredentials=true
AllowedHeaders:
  Authorization
  Content-Type
  X-CSRF-Token
```

Wildcardは全environmentで拒否する。

Development default:

```text
ALLOWED_ORIGINS=http://localhost:5173,http://127.0.0.1:5173
```

Production default:

```text
ALLOWED_ORIGINS=
```

ProductionではStep 4のNginx `/api` reverse proxyによりsame-originを基本とする。

## 6. TLS reverse proxy対応

`frontend/nginx.conf` は外部TLS terminatorが設定した `X-Forwarded-Proto` を保持し、存在しない場合だけNginx自身の `$scheme` を利用する。

これにより、外部URLが `https://...`、Nginxへの内部hopがHTTPでもsame-origin判定を誤らない。

## 7. Frontend変更

削除:

```text
localStorage cpim.token
localStorage cpim.user
SPA Authorization: Bearer 自動注入
```

追加:

```text
axios withCredentials=true
/auth/me session hydration
unsafe request X-CSRF-Token injection
server-side /auth/logout
async router auth initialization
```

JWT自体はBrowser JavaScriptから読み取らない。JavaScriptが読むCookieはCSRF tokenだけである。

## 8. OpenAPI / E2E

OpenAPI:

- Browser `/auth/login` responseからJWT fieldを除外
- `/auth/token` を追加
- `/auth/logout` を追加
- cookieAuth + bearerAuthを記述

E2E:

- 既存business API fixtureは `/api/auth/token` でBearer取得
- Browser UI loginは `/api/auth/login` を使用
- 新規 `auth-cookie-csrf.spec.ts` で2テスト追加

新規検証:

```text
HttpOnly session cookie
Secure production cookie
SameSite=Strict
JWT localStorage不存在
/auth/me cookie session
CSRFなしlogout -> 403
SPA logout -> cookie消去
/api/auth/token -> Bearer token
Bearer /auth/me -> success
/api/auth/token does not set session cookie
```

Existing 18 tests + Step 5 2 tests = 20 tests defined.

## 9. Static Guard

新設:

```text
backend/scripts/check_cookie_auth_csrf.py
```

結果:

```text
Cookie auth/CORS/CSRF Static Guard: 47/47 PASS
Production Config Guard:             41/41 PASS
Demo Credentials Guard:              41/41 PASS
Production Frontend Guard:            51/51 PASS
RBAC Guard:                            PASS
All backend/scripts/check_*.py:        29/29 PASS
```

RBAC Guardでは `/auth/logout` のみをauthenticated self-service mutationとして明示例外化した。業務mutation 111 routesは従来どおり `requirePermission()` 必須。

## 10. 既存Planning Guard

```text
0039 Real-Time Dispatch / Rescheduling: 106 checks PASS
0040 Production Control Tower:          95/95 PASS
0041 Recovery Planning:                 98/98 PASS
```

## 11. Runtime / dependency gate

```text
Runtime version policy: PASS
Dependency lock check:  PASS
CI/dependency policy:   PASS
OpenAPI JSON parse:      PASS
Python guard compile:    PASS
Nginx syntax:            PASS
TypeScript syntax transpile: PASS
```

依存packageの追加/更新はしていないため、Go/npm lockfilesは変更していない。

## 12. Go / Frontend test実行制約

この実行環境はGo 1.23.2 / Node 22.16.0 / npm 10.9.2で、指定runtime Go 1.26.10 / Node 24.20.0ではない。またGo module cacheと外部network accessがなく、Go dependency downloadが失敗する。

確認できたもの:

```text
isolated config package test: PASS
Go changed files: gofmt parse PASS
TypeScript changed files: TypeScript transpile syntax PASS
Nginx config syntax: PASS
```

未実行でCI/開発Macへ残すもの:

```text
go mod verify
go vet ./...
go test -race ./...
frontend npm ci / typecheck / build
Docker production build
Playwright 20/20 runtime execution
```

GitHub ActionsはStep 5 Guardとproduction topology E2Eを実行する構成へ更新済み。

## 13. Migration / 業務不変条件

```text
Migration count = 41
latest = 0041
0042 = absent
0003 checksum = 75a0b6251a0a293bed38205bb5268f82304b56f885d506ab048a355903ebac9c
```

変更していない主要領域:

- backend/migrations
- MRP logic
- Detailed Scheduling logic
- Full Pegging logic
- Supplier/Inventory policy logic
- Maintenance/OEE/Dispatch logic
- Production Control Tower business logic
- Recovery Planning business logic

Step 5はauthentication transport / browser security / deployment proxy boundaryの変更であり、Planning semanticsは変更していない。

## 14. 次Step

次は引き継ぎ順序どおり:

```text
Step 6 — Login Rate Limit / Auth Audit
```

Login success/failure/throttled/logout等のaudit eventと、IP + usernameの別bucket rate limitを追加する。
