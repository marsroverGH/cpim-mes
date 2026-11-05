import { test, expect } from '@playwright/test'
import { E2E_ADMIN_USERNAME, E2E_ADMIN_PASSWORD } from '../support/credentials'

test.describe('Step 5 browser cookie authentication', () => {
  test('browser session is HttpOnly/Secure, JWT is not in localStorage, CSRF is enforced', async ({ page, context }) => {
    await page.goto('/login')
    await page.getByLabel('ユーザー名').fill(E2E_ADMIN_USERNAME)
    await page.getByLabel('パスワード').fill(E2E_ADMIN_PASSWORD)
    await page.getByRole('button', { name: 'ログイン' }).click()
    await expect(page).toHaveURL(/\/$/)

    const cookies = await context.cookies()
    const session = cookies.find((c) => c.name === '__Host-cpim-session')
    const csrf = cookies.find((c) => c.name === '__Host-cpim-csrf')
    expect(session).toBeTruthy()
    expect(session?.httpOnly).toBeTruthy()
    expect(session?.secure).toBeTruthy()
    expect(session?.sameSite).toBe('Strict')
    expect(csrf).toBeTruthy()
    expect(csrf?.httpOnly).toBeFalsy()
    expect(csrf?.secure).toBeTruthy()

    const storage = await page.evaluate(() => ({
      token: localStorage.getItem('cpim.token'),
      user: localStorage.getItem('cpim.user')
    }))
    expect(storage).toEqual({ token: null, user: null })

    const me = await page.request.get('/api/auth/me')
    expect(me.ok()).toBeTruthy()
    expect((await me.json()).username).toBe(E2E_ADMIN_USERNAME)

    const noCSRF = await page.request.post('/api/auth/logout')
    expect(noCSRF.status()).toBe(403)

    await page.getByRole('button', { name: 'ログアウト' }).click()
    await expect(page).toHaveURL(/\/login$/)
  })

  test('explicit token endpoint preserves Bearer API access without session cookies', async ({ request }) => {
    const res = await request.post('/api/auth/token', {
      data: { username: E2E_ADMIN_USERNAME, password: E2E_ADMIN_PASSWORD }
    })
    expect(res.ok()).toBeTruthy()
    const body = await res.json()
    expect(body.token).toBeTruthy()
    expect(res.headers()['set-cookie']).toBeUndefined()

    const me = await request.get('/api/auth/me', {
      headers: { Authorization: `Bearer ${body.token}` }
    })
    expect(me.ok()).toBeTruthy()
  })
})
