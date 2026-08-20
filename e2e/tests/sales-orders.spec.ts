import { test, expect } from '@playwright/test'
import { randomUUID } from 'node:crypto'

async function adminToken(page: any) {
  const loginRes = await page.request.post('/api/auth/login', {
    data: { username: 'admin', password: 'admin123' }
  })
  expect(loginRes.ok()).toBeTruthy()
  const auth = await loginRes.json()
  expect(auth.token).toBeTruthy()
  return auth.token as string
}

test.describe('Sales Order flow', () => {
  test('customer order confirms, allocates and ships through the unified lot ledger', async ({ page }) => {
    const token = await adminToken(page)
    const headers = { Authorization: `Bearer ${token}` }
    const suffix = Date.now().toString()

    const itemsRes = await page.request.get('/api/items', { headers })
    expect(itemsRes.ok()).toBeTruthy()
    const items = await itemsRes.json()
    const frame = items.find((x: any) => x.code === 'FRAME-1')
    expect(frame).toBeTruthy()

    const customerRes = await page.request.post('/api/customers', {
      headers,
      data: { customerNo: `E2E-${suffix}`, name: `E2E Customer ${suffix}`, status: 'ACTIVE', shipTo: 'Tokyo' }
    })
    expect(customerRes.status()).toBe(201)
    const customer = await customerRes.json()

    const promised = new Date(Date.now() + 7 * 86400000).toISOString().slice(0, 10)
    const orderRes = await page.request.post('/api/sales-orders', {
      headers,
      data: {
        orderNo: `SO-E2E-${suffix}`,
        customerId: customer.id,
        requestedDate: promised,
        promisedDate: promised,
        lines: [{ itemId: frame.id, quantity: 1, unitPrice: 1000, promisedDate: promised }]
      }
    })
    expect(orderRes.status()).toBe(201)
    let detail = await orderRes.json()
    expect(detail.order.status).toBe('DRAFT')
    const lineId = detail.lines[0].id

    const confirmRes = await page.request.post(`/api/sales-orders/${detail.order.id}/confirm`, { headers })
    expect(confirmRes.ok()).toBeTruthy()
    detail = await confirmRes.json()
    expect(detail.order.status).toBe('CONFIRMED')

    const allocationId = randomUUID()
    const allocRes = await page.request.post(`/api/sales-order-lines/${lineId}/allocate`, {
      headers, data: { allocationId, quantity: 1 }
    })
    expect(allocRes.ok()).toBeTruthy()
    detail = await allocRes.json()
    expect(detail.lines[0].allocatedQty).toBe(1)

    // Idempotent retry must not double-reserve.
    const allocRetry = await page.request.post(`/api/sales-order-lines/${lineId}/allocate`, {
      headers, data: { allocationId, quantity: 1 }
    })
    expect(allocRetry.ok()).toBeTruthy()
    detail = await allocRetry.json()
    expect(detail.lines[0].allocatedQty).toBe(1)

    const shipmentId = randomUUID()
    const shipRes = await page.request.post(`/api/sales-order-lines/${lineId}/ship`, {
      headers, data: { shipmentId, quantity: 1, carrier: 'E2E', trackingNo: suffix }
    })
    expect(shipRes.status()).toBe(201)
    detail = await shipRes.json()
    expect(detail.order.status).toBe('SHIPPED')
    expect(detail.order.openQty).toBe(0)
    expect(detail.order.shippedQty).toBe(1)
    expect(detail.lines[0].allocatedQty).toBe(0)
    expect(detail.shipments).toHaveLength(1)

    // Idempotent shipment retry must not double-issue stock.
    const shipRetry = await page.request.post(`/api/sales-order-lines/${lineId}/ship`, {
      headers, data: { shipmentId, quantity: 1, carrier: 'E2E', trackingNo: suffix }
    })
    expect(shipRetry.status()).toBe(201)
    detail = await shipRetry.json()
    expect(detail.order.shippedQty).toBe(1)
  })
})
