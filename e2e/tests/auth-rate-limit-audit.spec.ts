import { test, expect } from '@playwright/test'
import { E2E_ADMIN_USERNAME, E2E_ADMIN_PASSWORD } from '../support/credentials'

test.describe('Step 6 login throttling and authentication audit', () => {
  test('repeated failures return 429 and auth events persist without secrets', async ({ page, request }) => {
    const adminTokenRes = await request.post('/api/auth/token', {
      data: { username: E2E_ADMIN_USERNAME, password: E2E_ADMIN_PASSWORD }
    })
    expect(adminTokenRes.ok()).toBeTruthy()
    const adminToken = (await adminTokenRes.json()).token as string
    expect(adminToken).toBeTruthy()

    const probeUsername = `rate-limit-probe-${Date.now()}`
    let last = await request.post('/api/auth/token', {
      data: { username: probeUsername, password: 'definitely-not-valid' }
    })
    expect(last.status()).toBe(401)
    expect(await last.json()).toEqual({ error: 'invalid username or password' })

    // Default username threshold is 5 failed attempts per window.
    for (let i = 1; i < 5; i += 1) {
      last = await request.post('/api/auth/token', {
        data: { username: probeUsername, password: `wrong-${i}` }
      })
    }
    expect(last.status()).toBe(429)
    expect(Number(last.headers()['retry-after'])).toBeGreaterThan(0)

    // Throttling a different username must not prevent the administrator from
    // authenticating because the IP bucket has a higher independent threshold.
    const stillUsable = await request.post('/api/auth/token', {
      data: { username: E2E_ADMIN_USERNAME, password: E2E_ADMIN_PASSWORD }
    })
    expect(stillUsable.ok()).toBeTruthy()

    await page.goto('/login')
    await page.getByLabel('ユーザー名').fill(E2E_ADMIN_USERNAME)
    await page.getByLabel('パスワード').fill(E2E_ADMIN_PASSWORD)
    await page.getByRole('button', { name: 'ログイン' }).click()
    await expect(page).toHaveURL(/\/$/)
    await page.getByRole('button', { name: 'ログアウト' }).click()
    await expect(page).toHaveURL(/\/login$/)

    const probeAuditRes = await request.get(`/api/audit-log?username=${encodeURIComponent(probeUsername)}&resource=authentication`, {
      headers: { Authorization: `Bearer ${adminToken}` }
    })
    expect(probeAuditRes.ok()).toBeTruthy()
    const probeAudit = await probeAuditRes.json() as Array<Record<string, unknown>>
    const probeActions = probeAudit.map((row) => row.action)
    expect(probeActions).toContain('LOGIN_FAILURE')
    expect(probeActions).toContain('LOGIN_THROTTLED')

    const adminAuditRes = await request.get(`/api/audit-log?username=${encodeURIComponent(E2E_ADMIN_USERNAME)}&resource=authentication`, {
      headers: { Authorization: `Bearer ${adminToken}` }
    })
    expect(adminAuditRes.ok()).toBeTruthy()
    const adminAudit = await adminAuditRes.json() as Array<Record<string, unknown>>
    const adminActions = adminAudit.map((row) => row.action)
    expect(adminActions).toContain('LOGIN_SUCCESS')
    expect(adminActions).toContain('LOGOUT')

    const serialized = JSON.stringify([...probeAudit, ...adminAudit])
    expect(serialized).not.toContain(E2E_ADMIN_PASSWORD)
    expect(serialized).not.toContain(adminToken)
    expect(serialized).not.toContain('__Host-cpim-session')
    expect(serialized).not.toContain('__Host-cpim-csrf')
  })
})
