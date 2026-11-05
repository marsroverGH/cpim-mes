import { expect, test } from '@playwright/test'

test('production reverse proxy keeps backend metrics private', async ({ request }) => {
  const response = await request.get('/metrics')
  expect(response.status()).toBe(404)
  expect(response.headers()['content-type'] ?? '').not.toContain('text/plain; version=0.0.4')
})
