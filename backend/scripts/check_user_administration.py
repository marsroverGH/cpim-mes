#!/usr/bin/env python3
"""Step 7 static guard: User Administration hardening invariants."""
from pathlib import Path
import json
import sys

ROOT = Path(__file__).resolve().parents[2]

def read(rel):
    return (ROOT / rel).read_text(encoding="utf-8")

checks=[]
def check(name, ok):
    checks.append((name, bool(ok)))

repo=read('backend/internal/repository/users.go')
svc=read('backend/internal/service/user_admin.go')
api=read('backend/internal/api/user_admin.go')
router=read('backend/internal/api/router.go')
rbac=read('backend/internal/api/rbac.go')
rbact=read('backend/internal/api/rbac_test.go')
audit=read('backend/internal/api/traceability.go')
models=read('backend/internal/domain/models.go')
service_container=read('backend/internal/service/service.go')
frontend_api=read('frontend/src/api/index.ts')
app=read('frontend/src/App.vue')
fr=read('frontend/src/router/index.ts')
admin_view=read('frontend/src/views/UserAdministration.vue')
account_view=read('frontend/src/views/Account.vue')
e2e=read('e2e/tests/user-administration.spec.ts')
make=read('Makefile')
ci=read('.github/workflows/ci.yml')
openapi=json.loads(read('backend/internal/api/openapi.json'))

# Schema/migration preservation
migs=sorted((ROOT/'backend/migrations').glob('*.sql'))
check('migration count is 46', len(migs)==46)
check('latest migration is 0046', migs[-1].name.startswith('0046_'))
check('migration 0042 exists', any(p.name.startswith('0042_') for p in migs))
check('existing user schema remains password-hidden in JSON', 'PasswordHash string' in models and 'json:"-"' in models)

# Repository correctness and concurrency
check('user administration advisory lock exists', 'userAdminAdvisoryLockID' in repo and 'pg_advisory_xact_lock' in repo)
check('role update uses transaction', 'func (r *UserRepo) UpdateRole' in repo and 'BeginTxx' in repo)
check('active update uses transaction', 'func (r *UserRepo) UpdateActive' in repo)
check('user row is FOR UPDATE locked', 'SELECT * FROM users WHERE id=$1 FOR UPDATE' in repo)
check('last active admin excludes target', "role='admin' AND is_active=true AND id<>$1" in repo)
check('last active admin has explicit error', 'ErrLastActiveAdmin' in repo)
check('role demotion invokes last-admin guard', 'u.IsActive && u.Role == domain.RoleAdmin && role != domain.RoleAdmin' in repo and 'ensureAnotherActiveAdmin' in repo)
check('disable invokes last-admin guard', 'u.IsActive && !active && u.Role == domain.RoleAdmin' in repo)
check('username conflict is explicit', 'ErrUsernameExists' in repo and 'ON CONFLICT (username) DO NOTHING' in repo)
check('create returning prevents phantom user', 'RETURNING *' in repo and 'errors.Is(err, sql.ErrNoRows)' in repo)
check('password change holds user row lock', 'func (r *UserRepo) ChangeOwnPassword' in repo and 'userForUpdate(ctx, tx, id)' in repo)
check('current password bcrypt verification exists', 'bcrypt.CompareHashAndPassword' in repo and 'ErrCurrentPasswordMismatch' in repo)

# Service policy
check('user admin service wired', 'UserAdmin *UserAdminService' in service_container and 'NewUserAdminService(r.Users)' in service_container)
check('managed password minimum 15 bytes', 'ManagedPasswordMinBytes = 15' in svc)
check('managed password bcrypt maximum 72 bytes', 'ManagedPasswordMaxBytes = 72' in svc)
check('managed roles restricted to four roles', all(x in svc for x in ['domain.RoleAdmin','domain.RolePlanner','domain.RoleOperator','domain.RoleViewer']))
check('unknown role validation exists', 'role must be admin, planner, operator, or viewer' in svc)
check('username bounded to 128 bytes', 'ManagedUsernameMaxBytes = 128' in svc)
check('password reset hashes with bcrypt', 'ResetPassword' in svc and 'bcrypt.GenerateFromPassword' in svc)
check('self password change requires current password', 'currentPassword == ""' in svc and 'ChangeOwnPassword' in svc)
check('last admin repository error maps to conflict', 'cannot disable or demote the last active administrator' in svc)

# RBAC/routes
check('admin users permission declared', 'PermAdminUsersManage' in rbac and 'admin.users.manage' in rbac)
check('admin user list protected', 'requirePermission(PermAdminUsersManage)).Get("/admin/users"' in router)
check('admin user create protected', 'requirePermission(PermAdminUsersManage)).Post("/admin/users"' in router)
check('role update protected', 'requirePermission(PermAdminUsersManage)).Put("/admin/users/{id}/role"' in router)
check('active update protected', 'requirePermission(PermAdminUsersManage)).Put("/admin/users/{id}/active"' in router)
check('password reset protected', 'requirePermission(PermAdminUsersManage)).Post("/admin/users/{id}/reset-password"' in router)
check('self password change authenticated route exists', 'secured.Post("/auth/change-password", srv.changeOwnPassword)' in router)
check('planner denied user management in RBAC test', 'planner cannot manage users' in rbact)
check('operator denied user management in RBAC test', 'operator cannot manage users' in rbact)

# Audit/secrets
check('admin password bodies excluded from generic audit', 'strings.HasPrefix(r.URL.Path, "/api/admin/users")' in audit)
for event in ['USER_CREATED','ROLE_CHANGED','USER_DISABLED','USER_ENABLED','PASSWORD_RESET','PASSWORD_CHANGED']:
    check(f'{event} audit event exists', event in api)
check('dedicated user audit resource exists', 'Resource:   "user-administration"' in api)
check('passwords not added to dedicated audit details', 'details["password"]' not in api and 'details["newPassword"]' not in api and 'details["currentPassword"]' not in api)
check('admin API never serializes password hash', 'PasswordHash' not in api)

# Frontend
check('frontend user admin API exists', 'export const UserAdminApi' in frontend_api)
check('frontend has no password response field', 'passwordHash' not in frontend_api)
check('admin users route exists', "path: '/admin/users'" in fr and 'requiresAdmin: true' in fr)
check('router blocks non-admin admin page', 'to.meta.requiresAdmin && !auth.isAdmin' in fr)
check('account route exists', "path: '/account'" in fr)
check('admin navigation is role-filtered', 'adminOnly: true' in app and 'auth.isAdmin' in app)
check('admin UI exists', 'User Administration' in admin_view and 'Reset Password' in admin_view)
check('account password UI exists', 'パスワードを変更' in account_view and 'current-password' in account_view)
check('frontend enforces 15-72 byte hint', '>= 15' in account_view and '<= 72' in account_view and 'TextEncoder' in account_view)

# OpenAPI
paths=openapi.get('paths',{})
for path in ['/admin/users','/admin/users/{id}/role','/admin/users/{id}/active','/admin/users/{id}/reset-password','/auth/change-password']:
    check(f'OpenAPI documents {path}', path in paths)
check('OpenAPI password formats are secret inputs', openapi['components']['schemas']['CreateManagedUserInput']['properties']['password'].get('format')=='password')
check('OpenAPI managed user excludes password', 'password' not in openapi['components']['schemas']['ManagedUser']['properties'])

# E2E / gates
check('E2E verifies operator forbidden', "expect(forbidden.status()).toBe(403)" in e2e)
check('E2E verifies last admin demotion conflict', 'demoteLast.status()).toBe(409)' in e2e)
check('E2E verifies last admin disable conflict', 'disableLast.status()).toBe(409)' in e2e)
check('E2E verifies role immediate effect', "expect((await promotedMe.json()).role).toBe('planner')" in e2e)
check('E2E verifies disable immediate effect', 'expect(disabledMe.status()).toBe(401)' in e2e)
check('E2E verifies password reset', 'reset-password' in e2e and 'resetPassword' in e2e)
check('E2E verifies self password change', '/api/auth/change-password' in e2e and 'changedPassword' in e2e)
check('E2E verifies password absent from audit', 'not.toContain(initialPassword)' in e2e and 'not.toContain(resetPassword)' in e2e)
check('Makefile user admin guard target', 'check-user-admin' in make)
check('CI runs user admin guard', 'check_user_administration.py' in ci)

failed=[name for name,ok in checks if not ok]
for name,ok in checks:
    print(('PASS' if ok else 'FAIL')+': '+name)
if failed:
    print(f'\nUser Administration Static Guard: {len(checks)-len(failed)}/{len(checks)} PASS')
    sys.exit(1)
print(f'\nUser Administration Static Guard: {len(checks)}/{len(checks)} PASS')
