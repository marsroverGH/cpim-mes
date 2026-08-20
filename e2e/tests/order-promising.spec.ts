import { test, expect } from '@playwright/test'

async function adminToken(page: any) {
  const loginRes = await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'admin123' } })
  expect(loginRes.ok()).toBeTruthy()
  const auth = await loginRes.json()
  expect(auth.token).toBeTruthy()
  return auth.token as string
}

async function jsonArray(page: any, path: string, headers: any) {
  const res = await page.request.get(path, { headers })
  expect(res.ok()).toBeTruthy()
  return await res.json() as any[]
}

test.describe('Advanced Order Promising', () => {
  test('DRAFT order is ATP/CTP checked without operational side effects and can be accepted', async ({ page }) => {
    const token = await adminToken(page)
    const headers = { Authorization: `Bearer ${token}` }
    const suffix = Date.now().toString()

    const items = await jsonArray(page, '/api/items', headers)
    const bike = items.find((x: any) => x.code === 'BIKE-100')
    expect(bike).toBeTruthy()

    const customerRes = await page.request.post('/api/customers', {
      headers,
      data: { customerNo: `CTP-${suffix}`, name: `CTP Customer ${suffix}`, status: 'ACTIVE', shipTo: 'Tokyo' }
    })
    expect(customerRes.status()).toBe(201)
    const customer = await customerRes.json()
    const requested = new Date(Date.now() + 7 * 86400000).toISOString().slice(0, 10)

    const orderRes = await page.request.post('/api/sales-orders', {
      headers,
      data: { orderNo: `SO-CTP-${suffix}`, customerId: customer.id, requestedDate: requested, lines: [{ itemId: bike.id, quantity: 1, unitPrice: 50000, requestedDate: requested }] }
    })
    expect(orderRes.status()).toBe(201)
    const order = await orderRes.json()
    expect(order.order.status).toBe('DRAFT')

    const beforeWO = (await jsonArray(page, '/api/work-orders', headers)).length
    const beforePO = (await jsonArray(page, '/api/purchase-orders', headers)).length
    const beforeSchedules = (await jsonArray(page, '/api/detailed-scheduling/runs', headers)).length
    const beforeTxns = (await jsonArray(page, `/api/inventory/${bike.id}/transactions`, headers)).length

    const checkRes = await page.request.post(`/api/sales-orders/${order.order.id}/promise/check`, { headers, data: { horizonDays: 180 } })
    expect(checkRes.status()).toBe(201)
    const promise = await checkRes.json()
    expect(promise.run.status).toBe('SUCCEEDED')
    expect(promise.run.resultHash).toHaveLength(64)
    expect(promise.lines).toHaveLength(1)
    expect(promise.lines[0].requestedQty).toBe(1)
    expect(promise.lines[0].atpQty + promise.lines[0].ctpQty).toBeCloseTo(1, 6)
    expect(promise.lines[0].earliestFullDate).toBeTruthy()
    expect(promise.confirmations.length).toBeGreaterThan(0)

    expect((await jsonArray(page, '/api/work-orders', headers)).length).toBe(beforeWO)
    expect((await jsonArray(page, '/api/purchase-orders', headers)).length).toBe(beforePO)
    expect((await jsonArray(page, '/api/detailed-scheduling/runs', headers)).length).toBe(beforeSchedules)
    expect((await jsonArray(page, `/api/inventory/${bike.id}/transactions`, headers)).length).toBe(beforeTxns)

    const acceptRes = await page.request.post(`/api/sales-orders/${order.order.id}/promise/accept`, { headers, data: { runId: promise.run.id } })
    expect(acceptRes.ok()).toBeTruthy()
    const accepted = await acceptRes.json()
    expect(accepted.acceptance).toBeTruthy()
    expect(accepted.acceptance.runId).toBe(promise.run.id)

    const detailRes = await page.request.get(`/api/sales-orders/${order.order.id}`, { headers })
    expect(detailRes.ok()).toBeTruthy()
    const detail = await detailRes.json()
    expect(detail.order.promisedDate).toBeTruthy()
    expect(detail.lines[0].promisedDate).toBeTruthy()

    const confirmRes = await page.request.post(`/api/sales-orders/${order.order.id}/confirm`, { headers })
    expect(confirmRes.ok()).toBeTruthy()
    expect((await confirmRes.json()).order.status).toBe('CONFIRMED')
  })
})
