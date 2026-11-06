import { test, expect } from '@playwright/test'
import { E2E_ADMIN_USERNAME, E2E_ADMIN_PASSWORD } from '../support/credentials'

async function tokenFor(request: any, username: string, password: string) {
  const res = await request.post('/api/auth/token', { data: { username, password } })
  if (!res.ok()) return { res, token: '' }
  return { res, token: (await res.json()).token as string }
}

const authz = (token: string) => ({ Authorization: `Bearer ${token}` })

test.describe.serial('Step 7 User Administration', () => {
  const managedUsername = `managed-user-${Date.now()}`
  const initialPassword = 'Initial-Managed-Password-2026!'
  const resetPassword = 'Reset-Managed-Password-2026!'
  const changedPassword = 'Changed-Managed-Password-2026!'

  test('admin manages users while last-active-admin and immediate authorization invariants hold', async ({ request }) => {
    const adminLogin = await tokenFor(request, E2E_ADMIN_USERNAME, E2E_ADMIN_PASSWORD)
    expect(adminLogin.res.ok()).toBeTruthy()
    const adminToken = adminLogin.token

    const initialList = await request.get('/api/admin/users', { headers: authz(adminToken) })
    expect(initialList.ok()).toBeTruthy()
    const initialUsers = await initialList.json() as Array<{ id: string; username: string; role: string; isActive: boolean }>
    const bootstrapAdmin = initialUsers.find((u) => u.username === E2E_ADMIN_USERNAME)
    expect(bootstrapAdmin).toBeTruthy()
    expect(bootstrapAdmin?.role).toBe('admin')
    expect(bootstrapAdmin?.isActive).toBeTruthy()

    // Before another administrator exists, removing the bootstrap admin must
    // fail closed. These two routes use the same advisory-lock invariant.
    const demoteLast = await request.put(`/api/admin/users/${bootstrapAdmin!.id}/role`, {
      headers: authz(adminToken), data: { role: 'viewer' }
    })
    expect(demoteLast.status()).toBe(409)
    const disableLast = await request.put(`/api/admin/users/${bootstrapAdmin!.id}/active`, {
      headers: authz(adminToken), data: { isActive: false }
    })
    expect(disableLast.status()).toBe(409)

    const created = await request.post('/api/admin/users', {
      headers: authz(adminToken),
      data: { username: managedUsername, password: initialPassword, role: 'operator' }
    })
    expect(created.status()).toBe(201)
    const managed = await created.json() as { id: string; username: string; role: string; isActive: boolean; password?: string }
    expect(managed.username).toBe(managedUsername)
    expect(managed.role).toBe('operator')
    expect(managed.isActive).toBeTruthy()
    expect(managed.password).toBeUndefined()
    expect(JSON.stringify(managed)).not.toContain(initialPassword)

    const duplicate = await request.post('/api/admin/users', {
      headers: authz(adminToken),
      data: { username: managedUsername, password: initialPassword, role: 'viewer' }
    })
    expect(duplicate.status()).toBe(409)

    const operatorLogin = await tokenFor(request, managedUsername, initialPassword)
    expect(operatorLogin.res.ok()).toBeTruthy()
    const managedToken = operatorLogin.token

    const forbidden = await request.get('/api/admin/users', { headers: authz(managedToken) })
    expect(forbidden.status()).toBe(403)

    const roleUpdate = await request.put(`/api/admin/users/${managed.id}/role`, {
      headers: authz(adminToken), data: { role: 'planner' }
    })
    expect(roleUpdate.ok()).toBeTruthy()

    // The same already-issued JWT must observe the new DB role next request.
    const promotedMe = await request.get('/api/auth/me', { headers: authz(managedToken) })
    expect(promotedMe.ok()).toBeTruthy()
    expect((await promotedMe.json()).role).toBe('planner')

    const disabled = await request.put(`/api/admin/users/${managed.id}/active`, {
      headers: authz(adminToken), data: { isActive: false }
    })
    expect(disabled.ok()).toBeTruthy()
    const disabledMe = await request.get('/api/auth/me', { headers: authz(managedToken) })
    expect(disabledMe.status()).toBe(401)

    const reenabled = await request.put(`/api/admin/users/${managed.id}/active`, {
      headers: authz(adminToken), data: { isActive: true }
    })
    expect(reenabled.ok()).toBeTruthy()

    const reset = await request.post(`/api/admin/users/${managed.id}/reset-password`, {
      headers: authz(adminToken), data: { newPassword: resetPassword }
    })
    expect(reset.status()).toBe(204)
    expect((await tokenFor(request, managedUsername, initialPassword)).res.status()).toBe(401)

    const resetLogin = await tokenFor(request, managedUsername, resetPassword)
    expect(resetLogin.res.ok()).toBeTruthy()
    const resetToken = resetLogin.token

    const selfChange = await request.post('/api/auth/change-password', {
      headers: authz(resetToken),
      data: { currentPassword: resetPassword, newPassword: changedPassword }
    })
    expect(selfChange.status()).toBe(204)
    expect((await tokenFor(request, managedUsername, resetPassword)).res.status()).toBe(401)
    expect((await tokenFor(request, managedUsername, changedPassword)).res.ok()).toBeTruthy()

    const auditRes = await request.get('/api/audit-log?resource=user-administration', { headers: authz(adminToken) })
    expect(auditRes.ok()).toBeTruthy()
    const audit = await auditRes.json() as Array<Record<string, unknown>>
    const related = audit.filter((row) => JSON.stringify(row).includes(managedUsername))
    const actions = related.map((row) => row.action)
    expect(actions).toContain('USER_CREATED')
    expect(actions).toContain('ROLE_CHANGED')
    expect(actions).toContain('USER_DISABLED')
    expect(actions).toContain('USER_ENABLED')
    expect(actions).toContain('PASSWORD_RESET')
    expect(actions).toContain('PASSWORD_CHANGED')
    const serialized = JSON.stringify(related)
    expect(serialized).not.toContain(initialPassword)
    expect(serialized).not.toContain(resetPassword)
    expect(serialized).not.toContain(changedPassword)
  })

  test('admin UI exposes user administration and account password management', async ({ page }) => {
    await page.goto('/login')
    await page.getByLabel('ユーザー名').fill(E2E_ADMIN_USERNAME)
    await page.getByLabel('パスワード').fill(E2E_ADMIN_PASSWORD)
    await page.getByRole('button', { name: 'ログイン' }).click()
    await expect(page).toHaveURL(/\/$/)

    await expect(page.getByText('ユーザー管理')).toBeVisible()
    await page.getByText('ユーザー管理').click()
    await expect(page).toHaveURL(/\/admin\/users$/)
    await expect(page.getByRole('heading', { name: 'User Administration' })).toBeVisible()
    await expect(page.getByRole('cell', { name: E2E_ADMIN_USERNAME, exact: true })).toBeVisible()
    await expect(page.getByText(managedUsername)).toBeVisible()

    await page.getByText('アカウント').click()
    await expect(page).toHaveURL(/\/account$/)
    await expect(page.getByRole('button', { name: 'パスワードを変更' })).toBeVisible()

    await page.getByRole('button', { name: 'ログアウト' }).click()
    await page.getByLabel('ユーザー名').fill(managedUsername)
    await page.getByLabel('パスワード').fill(changedPassword)
    await page.getByRole('button', { name: 'ログイン' }).click()
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByText('ユーザー管理')).toHaveCount(0)
    await page.goto('/admin/users')
    await expect(page).toHaveURL(/\/$/)
  })
})
