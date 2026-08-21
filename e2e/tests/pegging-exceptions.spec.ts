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

test.describe('Full Pegging / Exception Management', () => {
  test('Sales Order traces into a causal graph and late-promise exception is auditable', async ({ page }) => {
    const token = await adminToken(page)
    const headers = { Authorization: `Bearer ${token}` }
    const suffix = Date.now().toString()

    const items = await jsonArray(page, '/api/items', headers)
    const frame = items.find((x: any) => x.code === 'FRAME-1')
    expect(frame).toBeTruthy()

    const customerRes = await page.request.post('/api/customers', {
      headers,
      data: { customerNo: `PEG-${suffix}`, name: `Pegging Customer ${suffix}`, status: 'ACTIVE', shipTo: 'Tokyo' }
    })
    expect(customerRes.status()).toBe(201)
    const customer = await customerRes.json()

    const requested = new Date(Date.now() + 3 * 86400000).toISOString().slice(0, 10)
    const promised = new Date(Date.now() + 10 * 86400000).toISOString().slice(0, 10)
    const orderRes = await page.request.post('/api/sales-orders', {
      headers,
      data: {
        orderNo: `SO-PEG-${suffix}`, customerId: customer.id,
        requestedDate: requested, promisedDate: promised,
        lines: [{ itemId: frame.id, quantity: 1, unitPrice: 10000, requestedDate: requested, promisedDate: promised }]
      }
    })
    expect(orderRes.status()).toBe(201)
    let detail = await orderRes.json()
    const confirmRes = await page.request.post(`/api/sales-orders/${detail.order.id}/confirm`, { headers })
    expect(confirmRes.ok()).toBeTruthy()
    detail = await confirmRes.json()
    expect(detail.order.status).toBe('CONFIRMED')

    const runRes = await page.request.post(`/api/sales-orders/${detail.order.id}/pegging/run`, { headers, data: { horizonDays: 180 } })
    expect(runRes.status()).toBe(201)
    const graph = await runRes.json()
    expect(graph.run.status).toBe('SUCCEEDED')
    expect(graph.run.resultHash).toHaveLength(64)
    expect(graph.nodes.some((n: any) => n.nodeType === 'SALES_ORDER')).toBeTruthy()
    expect(graph.nodes.some((n: any) => n.nodeType === 'SALES_ORDER_LINE')).toBeTruthy()
    expect(graph.nodes.some((n: any) => n.nodeType === 'INVENTORY')).toBeTruthy()
    expect(graph.edges.some((e: any) => e.edgeType === 'HAS_LINE')).toBeTruthy()

    const late = graph.exceptions.find((x: any) => x.exceptionType === 'LATE_PROMISE')
    expect(late).toBeTruthy()
    expect(late.impactDays).toBeGreaterThanOrEqual(7)
    expect(Array.isArray(late.rootCausePath)).toBeTruthy()
    expect(late.rootCausePath[0]).toContain('SO:')

    const listRes = await page.request.get('/api/planning-exceptions?status=OPEN', { headers })
    expect(listRes.ok()).toBeTruthy()
    const current = await listRes.json()
    const listed = current.find((x: any) => x.id === late.id)
    expect(listed).toBeTruthy()
    expect(listed.salesOrderNo).toBe(detail.order.orderNo)

    const ackRes = await page.request.post(`/api/planning-exceptions/${late.id}/actions`, {
      headers, data: { actionType: 'ACKNOWLEDGE', comment: 'E2E reviewed root cause' }
    })
    expect(ackRes.status()).toBe(201)
    const action = await ackRes.json()
    expect(action.fromStatus).toBe('OPEN')
    expect(action.toStatus).toBe('ACKNOWLEDGED')

    const afterRes = await page.request.get('/api/planning-exceptions?status=ACKNOWLEDGED', { headers })
    expect(afterRes.ok()).toBeTruthy()
    const after = await afterRes.json()
    expect(after.some((x: any) => x.id === late.id && x.currentStatus === 'ACKNOWLEDGED')).toBeTruthy()

    const historyRes = await page.request.get(`/api/sales-orders/${detail.order.id}/pegging-runs`, { headers })
    expect(historyRes.ok()).toBeTruthy()
    expect((await historyRes.json()).some((x: any) => x.id === graph.run.id)).toBeTruthy()
  })
})
