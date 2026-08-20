import { test, expect, Page } from '@playwright/test'

async function login(page: Page) {
  await page.goto('/login')
  await page.getByLabel('ユーザー名').fill('admin')
  await page.getByLabel('パスワード').fill('admin123')
  await page.getByRole('button', { name: 'ログイン' }).click()
  await expect(page).toHaveURL('/')
}

test.describe('Items list & navigation', () => {
  test.beforeEach(login)

  test('shows seeded BIKE-100 in items list', async ({ page }) => {
    await page.goto('/items')
    await expect(page.getByRole('cell', { name: 'BIKE-100' }).first()).toBeVisible()
    await expect(page.getByRole('cell', { name: 'FRAME-1' }).first()).toBeVisible()
  })

  test('search filter narrows the table', async ({ page }) => {
    await page.goto('/items')
    await page.getByLabel(/検索/).first().fill('BIKE')
    await expect(page.getByRole('cell', { name: 'BIKE-100' })).toBeVisible()
    await expect(page.getByRole('cell', { name: 'FRAME-1' })).toHaveCount(0)
  })
})

test.describe('MRP execution', () => {
  test.beforeEach(login)

  test('runs MRP and displays planned orders for seeded demand', async ({ page }) => {
    await page.goto('/mrp')
    await page.getByRole('button', { name: /MRP実行/ }).click()

    // After run, seeded demand for BIKE-100 should result in at least one row
    await expect(page.getByRole('cell', { name: 'BIKE-100' }).first()).toBeVisible({ timeout: 10_000 })
  })
})

test.describe('OpenAPI', () => {
  test('docs page loads', async ({ page }) => {
    // No auth required for /api/docs
    await page.goto('/api/docs')
    await expect(page.getByText(/CPIM-MES API/)).toBeVisible()
  })
})
