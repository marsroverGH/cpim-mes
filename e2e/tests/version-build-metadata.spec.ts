import { expect, test } from '@playwright/test'

test.describe('Step 9 version / build metadata', () => {
  test('public /version reports release, commit, UTC build time and migration latest', async ({ request }) => {
    const response = await request.get('/version')
    expect(response.status()).toBe(200)
    expect(response.headers()['cache-control']).toContain('no-store')

    const body = await response.json()
    expect(body.version).toBe(process.env.E2E_EXPECTED_VERSION || 'v0.44.0')

    const expectedCommit = process.env.E2E_EXPECTED_COMMIT
    if (expectedCommit) {
      expect(body.commit).toBe(expectedCommit)
    } else {
      expect(typeof body.commit).toBe('string')
      expect(body.commit.length).toBeGreaterThan(0)
    }

    expect(body.migrationLatest).toBe(46)
    expect(typeof body.buildTime).toBe('string')
    expect(body.buildTime).not.toBe('unknown')
    expect(Number.isNaN(Date.parse(body.buildTime))).toBeFalsy()
    expect(body.buildTime).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/)
  })
})
