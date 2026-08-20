import { test, expect } from '@playwright/test'

test.describe('Login flow', () => {
  test('redirects unauthenticated users to /login', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/login$/)
    await expect(page.getByText('CPIM-MES ログイン')).toBeVisible()
  })

  test('admin can log in with seed credentials', async ({ page }) => {
    await page.goto('/login')
    await page.getByLabel('ユーザー名').fill('admin')
    await page.getByLabel('パスワード').fill('admin123')
    await page.getByRole('button', { name: 'ログイン' }).click()

    // After login, redirect to "/" and dashboard renders
    await expect(page).toHaveURL('/')
    await expect(page.getByText(/admin.*\(admin\)/)).toBeVisible()
    // Dashboard KPI cards
    await expect(page.getByText('品目数')).toBeVisible()
  })

  test('rejects invalid credentials', async ({ page }) => {
    await page.goto('/login')
    await page.getByLabel('ユーザー名').fill('admin')
    await page.getByLabel('パスワード').fill('wrong-password')
    await page.getByRole('button', { name: 'ログイン' }).click()
    await expect(page.getByText(/失敗|invalid/i)).toBeVisible()
  })
})
