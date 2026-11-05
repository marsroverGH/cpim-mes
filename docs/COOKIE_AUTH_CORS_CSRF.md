# Cookie Authentication / CORS / CSRF

Target: **v0.41.1 Production Hardening — Step 5**

## Browser authentication

`POST /api/auth/login` authenticates the user and sets two cookies:

- production session: `__Host-cpim-session` — JWT, `HttpOnly`, `Secure`, `Path=/`, configured `SameSite`
- production CSRF: `__Host-cpim-csrf` — signed CSRF token, readable by the SPA, `Secure`, `Path=/`, same `SameSite`

Development uses unprefixed `cpim-session` / `cpim-csrf` because `__Host-` cookies require `Secure`.

The JWT is never returned by the browser login response and is never stored in `localStorage` or `sessionStorage`. The SPA restores login state with `GET /api/auth/me`.

`POST /api/auth/logout` clears both cookies. Cookie-authenticated logout is itself CSRF-protected.

## CLI/API Bearer compatibility

`POST /api/auth/token` accepts the same username/password JSON and returns the JWT explicitly for non-browser clients. It does not set session cookies.

Bearer requests remain:

```http
Authorization: Bearer <jwt>
```

Bearer-authenticated requests do not require the browser CSRF header.

## CSRF protection

Unsafe cookie-authenticated methods (`POST`, `PUT`, `PATCH`, `DELETE`, and any non-safe method) require all of:

1. session authentication from the HttpOnly cookie;
2. `X-CSRF-Token` header;
3. the matching CSRF cookie;
4. a valid HMAC signature bound to the exact JWT session;
5. no `Sec-Fetch-Site: cross-site` signal;
6. when `Origin` is present, same-origin or an explicit configured `ALLOWED_ORIGINS` match.

The token is a signed double-submit value generated from a cryptographic random nonce and HMAC-SHA256 over the session JWT plus nonce. SameSite remains defense-in-depth, not the sole CSRF control.

## CORS

Credentialed CORS cannot use wildcard origins. `ALLOWED_ORIGINS` therefore accepts only explicit `http://` or `https://` origins and rejects any `*` in every environment.

Development default:

```text
http://localhost:5173,http://127.0.0.1:5173
```

Production default is empty because the Nginx frontend and `/api` reverse proxy are same-origin. When explicit CORS is enabled, the backend sends credentialed CORS and permits `Authorization`, `Content-Type`, and `X-CSRF-Token` headers.

## Production TLS boundary

Production forces `COOKIE_SECURE=true`. The `__Host-` prefix additionally requires Secure, no Domain attribute, and `Path=/`. TLS must therefore terminate at the application edge/load balancer/reverse proxy before deployment to non-localhost clients.

## Validation

```bash
cd backend
go test -mod=readonly ./internal/config ./internal/service ./internal/api
cd ..
python3 backend/scripts/check_cookie_auth_csrf.py
```

CI also runs the full Go race suite, frontend typecheck/build, all historical Static Guards, Docker production topology, and Playwright. `e2e/tests/auth-cookie-csrf.spec.ts` verifies browser cookies, absence of JWT in localStorage, `/auth/me`, CSRF rejection, logout, and explicit Bearer-token compatibility.

## Invariants

Step 5 does not add migration `0042`, modify migrations `0001`-`0041`, or change MRP, Detailed Scheduling, Pegging, Control Tower, or Recovery Planning semantics.
