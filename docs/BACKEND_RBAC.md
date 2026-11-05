# Backend RBAC hardening

This version enforces authorization in the Go backend. Hiding buttons in the frontend is not considered a security control.

## Role model

| Area / action | viewer | operator | planner | admin |
|---|---:|---:|---:|---:|
| Read authenticated business data | Yes | Yes | Yes | Yes |
| Item master create/update/delete/import | No | No | No | Yes |
| BOM add/delete/recompute LLC | No | No | Yes | Yes |
| Demand / MPS / MRP / forecast / CRP | No | No | Yes | Yes |
| Manual inventory / lot adjustment | No | No | No | Yes |
| Cycle count generation | No | No | Yes | Yes |
| Cycle count result recording | No | Yes | Yes | Yes |
| WO create / release / close-status management | No | No | Yes | Yes |
| WO progress / partial completion | No | Yes | Yes | Yes |
| Shop-floor operation start/stop/complete | No | Yes | Yes | Yes |
| PO create | No | No | Yes | Yes |
| PO receive | No | Yes | Yes | Yes |
| Quality inspection recording | No | Yes | Yes | Yes |
| Work-center / routing / calendar master changes | No | No | No | Yes |
| Item-group master changes | No | No | No | Yes |
| S&OP / RCCP profile changes | No | No | Yes | Yes |
| ECO create / component editing | No | No | Yes | Yes |
| ECO approve / apply / cancel | No | No | No | Yes |
| Audit log read | No | No | Yes | Yes |
| AI agent ask | Yes | Yes | Yes | Yes |
| User administration | No | No | No | Yes |

`admin` is an all-permissions role. Unknown roles are denied by default.

## Enforcement

Business/admin mutations in `router.go` are registered through `requirePermission(...)`. The only authenticated non-RBAC mutations are self-service logout and self-service password change; both still require authentication and cookie requests remain CSRF-protected. Authorization happens after JWT authentication and before the handler executes. Missing permission returns HTTP `403 Forbidden`.

`backend/scripts/check_rbac_routes.py` is a dependency-free static guard that fails if a secured mutation route is added without permission middleware. It also verifies critical BOM, inventory, WO, PO, and ECO mappings.

## Stale JWT privilege prevention

JWT role claims are no longer trusted for the full token lifetime. On every authenticated request, `AuthService.VerifyCurrent` reloads the current user row by JWT user ID and refreshes `username` and `role` from the database. If the user is deleted or `is_active=false`, the request is rejected with `401 Unauthorized`.

This means an admin can demote or deactivate a user and the change takes effect on the next request, even if that user still holds an unexpired token.

## /api/auth/me

`GET /api/auth/me` now returns the effective current role and an explicit `permissions` array. This can be used by the frontend for UX, but backend middleware remains authoritative.

## Audit behavior

The existing audit middleware wraps secured mutation handlers. Permission-denied mutation attempts return 403 and are recorded by the audit pipeline, subject to the existing best-effort audit behavior.

## Validation performed for this build

- Backend RBAC static guard detects 115 permission-protected mutation routes after Step 7.
- `admin.users.manage` is admin-only; planner/operator/viewer denial is unit-tested.
- Self-service logout and password change are the only authenticated mutation routes intentionally exempt from business RBAC.
- Role/deactivation changes continue to use per-request DB authorization refresh.
- Step 7 User Administration invariants are additionally checked by `backend/scripts/check_user_administration.py`.

No database migration is required for Step 7; the existing `users.role` and `users.is_active` columns are used.
