# User Administration — v0.41.1 Step 7

Step 7 adds production-safe user lifecycle administration without changing the database schema. The existing `users` table from migration `0003_users.sql` remains authoritative and migration latest stays at `0041`.

## Authorization model

A new backend permission is defined:

```text
admin.users.manage
```

`admin` receives all permissions, including this one. `planner`, `operator`, and `viewer` do not receive it. Frontend route visibility is only UX; backend `requirePermission(PermAdminUsersManage)` is authoritative.

## API

```text
GET  /api/admin/users
POST /api/admin/users
PUT  /api/admin/users/{id}/role
PUT  /api/admin/users/{id}/active
POST /api/admin/users/{id}/reset-password
POST /api/auth/change-password
```

The first five routes require `admin.users.manage`. `POST /api/auth/change-password` is authenticated self-service and, for browser cookie authentication, remains protected by the existing CSRF middleware.

## Password policy

Managed passwords use the existing bcrypt implementation and must be:

```text
minimum: 15 bytes
maximum: 72 bytes
```

The 72-byte maximum is enforced before bcrypt. Newline, carriage-return, and NUL characters are rejected. Password values and password hashes are never returned by the API.

## Last active administrator invariant

The application must never allow its final active administrator to be disabled or demoted.

Both role and active-state mutations execute as:

```text
BEGIN
  pg_advisory_xact_lock(userAdminAdvisoryLockID)
  SELECT target user FOR UPDATE
  if removing active admin:
      count another active admin excluding target
      reject with 409 if none exists
  UPDATE users
COMMIT
```

The shared advisory lock is important: two concurrent requests cannot each observe two active administrators and then independently remove one, leaving zero.

No migration is added for this rule. Direct out-of-band SQL can still bypass application policy; production database write access should therefore remain restricted to the application/operations role.

## Immediate authorization changes

`AuthService.VerifyCurrent` reloads `users.role` and `users.is_active` on every authenticated request. Therefore:

- role changes take effect on the next request even for an already-issued JWT;
- disabling a user invalidates that user's next request with `401`;
- re-enabling a user allows a still-valid JWT to authenticate again until its normal expiry.

Password reset changes subsequent password authentication immediately. This release does not add a persistent session-generation/revocation column, so password reset by itself does not invalidate an already-issued Bearer JWT. Browser users can be disabled when immediate session invalidation is required.

## Audit events

Successful user security changes write dedicated events to the existing `audit_log` with `resource=user-administration`:

```text
USER_CREATED
ROLE_CHANGED
USER_DISABLED
USER_ENABLED
PASSWORD_RESET
PASSWORD_CHANGED
```

The generic mutation audit middleware deliberately removes request bodies for `/api/admin/users...`, while `/api/auth/...` bodies are already excluded. Dedicated audit payloads contain only non-secret target metadata and request ID. Passwords, hashes, JWTs, cookies, and CSRF tokens are not logged.

## Frontend

Two pages are added:

```text
/account       authenticated users: change own password
/admin/users   administrators only: user lifecycle management
```

The admin page supports create, role update, enable/disable, and reset password. Direct navigation by a non-admin is redirected, while the backend still independently enforces authorization.

## Verification

Step 7 adds:

```text
backend/scripts/check_user_administration.py
e2e/tests/user-administration.spec.ts
backend/internal/service/user_admin_test.go
```

The E2E scenario verifies admin creation, non-admin denial, last-admin protection, immediate role/active changes, password reset, self-service password change, and secret-free audit evidence.
