import { test, expect } from '@playwright/test'

async function adminToken(page: any) {
  const res = await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'admin123' } })
  expect(res.ok()).toBeTruthy()
  return (await res.json()).token as string
}
async function jsonArray(page: any, path: string, headers: any) {
  const res = await page.request.get(path, { headers })
  expect(res.ok()).toBeTruthy()
  return await res.json() as any[]
}
const dateOnly = (d = 0) => {
  const x = new Date(Date.now() + d * 86400000)
  return x.toISOString().slice(0, 10)
}
const dateTime = (d = 0) => `${dateOnly(d)}T00:00:00Z`

test.describe('OEE / Production Performance / Actual Capacity Feedback', () => {
  test('Shop Floor actuals generate OEE feedback reused by Detailed Scheduling, CTP and Pegging', async ({ page }) => {
    const token = await adminToken(page)
    const headers = { Authorization: `Bearer ${token}` }
    const suffix = Date.now().toString()

    const items = await jsonArray(page, '/api/items', headers)
    const wheel = items.find((x: any) => x.code === 'WHEEL-1')
    expect(wheel).toBeTruthy()

    // 1) Produce immutable Shop Floor evidence on WC-ASSY.
    const actualWORes = await page.request.post('/api/work-orders', {
      headers,
      data: { orderNo: `WO-OEE-ACT-${suffix}`, itemId: wheel.id, quantity: 1, startDate: dateTime(0), dueDate: dateTime(2), status: 'PLANNED' }
    })
    expect(actualWORes.status()).toBe(201)
    const actualWO = await actualWORes.json()
    expect((await page.request.post(`/api/work-orders/${actualWO.id}/release`, { headers, data: {} })).ok()).toBeTruthy()
    const ops = await jsonArray(page, `/api/work-orders/${actualWO.id}/operations`, headers)
    expect(ops).toHaveLength(1)
    const op = ops[0]
    expect((await page.request.post(`/api/wo-operations/${op.id}/start`, { headers, data: {} })).ok()).toBeTruthy()
    expect((await page.request.post(`/api/wo-operations/${op.id}/scrap`, { headers, data: { quantity: 0.25, notes: 'OEE quality loss evidence' } })).ok()).toBeTruthy()
    expect((await page.request.post(`/api/wo-operations/${op.id}/complete`, { headers, data: { completedQty: 1, notes: 'OEE good output' } })).ok()).toBeTruthy()
    const logs = await jsonArray(page, `/api/wo-operations/${op.id}/logs`, headers)
    expect(logs.some((x: any) => x.eventType === 'SCRAP' && Number(x.quantity) > 0)).toBeTruthy()

    // 2) Calculate OEE and create an explicit DRAFT actual-capacity recommendation.
    const perfRes = await page.request.post('/api/production-performance/runs', {
      headers,
      data: { windowStart: dateOnly(0), windowEnd: dateOnly(0), minCompletedOps: 1 }
    })
    expect(perfRes.status()).toBe(201)
    const perf = await perfRes.json()
    expect(perf.run.status).toBe('COMPLETE')
    expect(perf.run.resultHash).toHaveLength(64)
    const assy = perf.results.find((x: any) => x.workCenterCode === 'WC-ASSY')
    expect(assy).toBeTruthy()
    expect(assy.sampleCount).toBeGreaterThanOrEqual(1)
    expect(assy.goodQuantity).toBeGreaterThanOrEqual(1)
    expect(assy.rejectQuantity).toBeGreaterThan(0)
    expect(assy.quality).toBeLessThan(1)
    const draft = perf.feedback.find((x: any) => x.workCenterId === assy.workCenterId)
    expect(draft).toBeTruthy()
    expect(draft.status).toBe('DRAFT')

    const activateRes = await page.request.post(`/api/capacity-feedback/${draft.id}/activate`, {
      headers, data: { effectiveFrom: dateOnly(0), notes: 'E2E activate empirical capacity' }
    })
    expect(activateRes.ok()).toBeTruthy()
    const active = await activateRes.json()
    expect(active.status).toBe('ACTIVE')

    // 3) A new firm WO is scheduled with the ACTIVE empirical capacity profile.
    const planWORes = await page.request.post('/api/work-orders', {
      headers,
      data: { orderNo: `WO-OEE-PLAN-${suffix}`, itemId: wheel.id, quantity: 1, startDate: dateTime(0), dueDate: dateTime(0), status: 'PLANNED' }
    })
    expect(planWORes.status()).toBe(201)
    const planWO = await planWORes.json()
    expect((await page.request.post(`/api/work-orders/${planWO.id}/release`, { headers, data: {} })).ok()).toBeTruthy()

    const dsRes = await page.request.post('/api/detailed-scheduling/run', { headers, data: { startDate: dateOnly(0), horizonDays: 28 } })
    expect(dsRes.ok()).toBeTruthy()
    const ds = await dsRes.json()
    expect(ds.run.status).toBe('COMPLETE')
    expect(ds.capacityFeedback.some((x: any) => x.feedbackVersionId === active.id && x.workCenterId === assy.workCenterId)).toBeTruthy()

    // 4) CTP still uses the same Detailed allocator, now calibrated by feedback.
    const customerRes = await page.request.post('/api/customers', {
      headers, data: { customerNo: `OEE-${suffix}`, name: `OEE Customer ${suffix}`, status: 'ACTIVE', shipTo: 'Tokyo' }
    })
    expect(customerRes.status()).toBe(201)
    const customer = await customerRes.json()
    const ctpSORes = await page.request.post('/api/sales-orders', {
      headers,
      data: { orderNo: `SO-OEE-CTP-${suffix}`, customerId: customer.id, requestedDate: dateOnly(7), lines: [{ itemId: wheel.id, quantity: 100, unitPrice: 1000, requestedDate: dateOnly(7) }] }
    })
    expect(ctpSORes.status()).toBe(201)
    const ctpSO = await ctpSORes.json()
    const promiseRes = await page.request.post(`/api/sales-orders/${ctpSO.order.id}/promise/check`, { headers, data: { horizonDays: 120 } })
    expect(promiseRes.status()).toBe(201)
    const promise = await promiseRes.json()
    expect(promise.lines[0].ctpQty).toBeGreaterThan(0)

    // 5) Force formal WO usage in Pegging and retain OEE feedback as causal evidence.
    const pegSORes = await page.request.post('/api/sales-orders', {
      headers,
      data: { orderNo: `SO-OEE-PEG-${suffix}`, customerId: customer.id, requestedDate: dateOnly(0), lines: [{ itemId: wheel.id, quantity: 82, unitPrice: 1000, requestedDate: dateOnly(0) }] }
    })
    expect(pegSORes.status()).toBe(201)
    const pegSO = await pegSORes.json()
    expect((await page.request.post(`/api/sales-orders/${pegSO.order.id}/confirm`, { headers, data: {} })).ok()).toBeTruthy()
    const pegRes = await page.request.post(`/api/sales-orders/${pegSO.order.id}/pegging/run`, { headers, data: { horizonDays: 60 } })
    expect(pegRes.status()).toBe(201)
    const peg = await pegRes.json()
    expect(peg.nodes.some((x: any) => x.nodeType === 'CAPACITY_FEEDBACK' && x.entityId === active.id)).toBeTruthy()
    expect(peg.exceptions.some((x: any) => x.exceptionType === 'OEE_CAPACITY_RISK')).toBeTruthy()
  })
})
