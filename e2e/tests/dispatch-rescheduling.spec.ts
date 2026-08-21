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
const dateOnly = (d = 0) => new Date(Date.now() + d * 86400000).toISOString().slice(0, 10)
const dateTime = (d = 0) => `${dateOnly(d)}T00:00:00Z`

// This spec is executed once as a dedicated acceptance test and then again as
// part of the full suite against the same database. Do not leak an IN_PROGRESS
// operation from the dedicated run into the full-suite run: that would
// correctly be treated as an EXECUTED commitment and block the next candidate.
let cleanupStartedOperationId = ''
test.afterEach(async ({ page }) => {
  const opId = cleanupStartedOperationId
  cleanupStartedOperationId = ''
  if (!opId) return
  const login = await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'admin123' } })
  if (!login.ok()) return
  const token = (await login.json()).token as string
  await page.request.post(`/api/wo-operations/${opId}/complete`, {
    headers: { Authorization: `Bearer ${token}` },
    data: { completedQty: 1, notes: '0039 E2E cleanup after autonomous-reschedule assertion' }
  })
})

test.describe('Real-Time Dispatching / Dynamic Rescheduling / Schedule Adherence', () => {
  test('active execution schedule dispatches work, re-plans future work and preserves auditable reschedule root cause', async ({ page }) => {
    const token = await adminToken(page)
    const headers = { Authorization: `Bearer ${token}` }
    const suffix = Date.now().toString()
    const items = await jsonArray(page, '/api/items', headers)
    const wheel = items.find((x: any) => x.code === 'WHEEL-1')
    expect(wheel).toBeTruthy()

    // A zero-minute frozen zone makes the activation branch deterministic for E2E;
    // firm changes remain audited rather than silently rewritten.
    const policyRes = await page.request.post('/api/dispatch-policy-versions', {
      headers,
      data: { freezeMinutes: 0, firmMinutes: 60, startLateThresholdMinutes: 0, completionLateThresholdMinutes: 0, autoReschedule: true, minAutoIntervalMinutes: 0, setupMatchBonus: 20 }
    })
    expect(policyRes.status()).toBe(201)
    const policy = await policyRes.json()
    expect(policy.status).toBe('DRAFT')
    const activatePolicy = await page.request.post(`/api/dispatch-policy-versions/${policy.id}/activate`, { headers, data: {} })
    expect(activatePolicy.ok()).toBeTruthy()
    expect((await activatePolicy.json()).status).toBe('ACTIVE')

    const wo1Res = await page.request.post('/api/work-orders', {
      headers, data: { orderNo: `WO-DISP-A-${suffix}`, itemId: wheel.id, quantity: 1, startDate: dateTime(0), dueDate: dateTime(5), status: 'PLANNED' }
    })
    expect(wo1Res.status()).toBe(201)
    const wo1 = await wo1Res.json()
    expect((await page.request.post(`/api/work-orders/${wo1.id}/release`, { headers, data: {} })).ok()).toBeTruthy()

    // Normal Detailed Scheduling now becomes the explicit active execution schedule.
    const sourceRes = await page.request.post('/api/detailed-scheduling/run', { headers, data: { startDate: dateOnly(0), horizonDays: 28 } })
    expect(sourceRes.ok()).toBeTruthy()
    const source = await sourceRes.json()
    const state1Res = await page.request.get('/api/schedule-execution', { headers })
    expect(state1Res.ok()).toBeTruthy()
    const state1 = await state1Res.json()
    expect(state1.activeRunId).toBe(source.run.id)

    const boardRes = await page.request.get('/api/dispatch', { headers })
    expect(boardRes.ok()).toBeTruthy()
    const board = await boardRes.json()
    expect(board.execution.activeRunId).toBe(source.run.id)
    expect(board.items.some((x: any) => x.workOrderId === wo1.id && ['READY','QUEUED','LATE_START'].includes(x.dispatchStatus))).toBeTruthy()
    expect(board.items.some((x: any) => ['FROZEN','FIRM','FLEXIBLE','EXECUTED'].includes(x.timeFence))).toBeTruthy()

    const adhRes = await page.request.post('/api/schedule-adherence/snapshots', { headers, data: {} })
    expect(adhRes.status()).toBe(201)
    const adh = await adhRes.json()
    expect(adh.snapshot.activeRunId).toBe(source.run.id)
    expect(adh.snapshot.resultHash).toHaveLength(64)
    expect(Number(adh.summary.onTimeStartPct)).toBeGreaterThanOrEqual(0)

    // Add new firm demand after the source schedule. This guarantees an ADDED
    // operation in the candidate and therefore a controlled activation.
    const wo2Res = await page.request.post('/api/work-orders', {
      headers, data: { orderNo: `WO-DISP-B-${suffix}`, itemId: wheel.id, quantity: 1, startDate: dateTime(0), dueDate: dateTime(6), status: 'PLANNED' }
    })
    expect(wo2Res.status()).toBe(201)
    const wo2 = await wo2Res.json()
    expect((await page.request.post(`/api/work-orders/${wo2.id}/release`, { headers, data: {} })).ok()).toBeTruthy()

    const replanRes = await page.request.post('/api/dynamic-rescheduling/run', {
      headers, data: { triggerType: 'PRIORITY_CHANGE', triggerRef: wo2.id, reason: 'E2E new released work', horizonDays: 28 }
    })
    expect(replanRes.status()).toBe(201)
    const replan = await replanRes.json()
    expect(replan.run.status).toBe('ACTIVATED')
    expect(replan.run.candidateRunId).toBeTruthy()
    expect(replan.run.resultHash).toHaveLength(64)
    expect(replan.changes.some((x: any) => x.workOrderId === wo2.id && x.changeType === 'ADDED')).toBeTruthy()
    expect(replan.changes.every((x: any) => x.frozenConflict === false)).toBeTruthy()

    const state2 = await (await page.request.get('/api/schedule-execution', { headers })).json()
    expect(state2.activeRunId).toBe(replan.run.candidateRunId)
    const board2 = await (await page.request.get('/api/dispatch', { headers })).json()
    expect(board2.items.some((x: any) => x.workOrderId === wo2.id)).toBeTruthy()

    // Shop Floor actuals enqueue a DB signal and invoke the autonomous SYSTEM bridge.
    const ops = await jsonArray(page, `/api/work-orders/${wo2.id}/operations`, headers)
    const op = ops[0]
    expect(op).toBeTruthy()
    const startRes = await page.request.post(`/api/wo-operations/${op.id}/start`, { headers, data: {} })
    expect(startRes.ok()).toBeTruthy()
    cleanupStartedOperationId = op.id
    const runs = await jsonArray(page, '/api/dynamic-rescheduling/runs', headers)
    const systemRun = runs.find((x: any) => x.actorType === 'SYSTEM' && ['SHOP_FLOOR_PROGRESS','LATE_OPERATION'].includes(x.triggerType))
    expect(systemRun).toBeTruthy()
    // Once an operation is executing, autonomous impact analysis may produce a candidate,
    // but it must never activate a rewrite of that executed commitment.
    if (systemRun.status === 'BLOCKED') expect(Number(systemRun.executionConflicts)).toBeGreaterThan(0)

    // Full Pegging follows the active execution schedule and exposes the
    // reschedule decision as causal evidence rather than only the latest run.
    const customerRes = await page.request.post('/api/customers', {
      headers, data: { customerNo: `DISP-${suffix}`, name: `Dispatch Customer ${suffix}`, status: 'ACTIVE', shipTo: 'Tokyo' }
    })
    expect(customerRes.status()).toBe(201)
    const customer = await customerRes.json()
    const soRes = await page.request.post('/api/sales-orders', {
      headers,
      data: { orderNo: `SO-DISP-${suffix}`, customerId: customer.id, requestedDate: dateOnly(0), lines: [{ itemId: wheel.id, quantity: 82, unitPrice: 1000, requestedDate: dateOnly(0) }] }
    })
    expect(soRes.status()).toBe(201)
    const so = await soRes.json()
    expect((await page.request.post(`/api/sales-orders/${so.order.id}/confirm`, { headers, data: {} })).ok()).toBeTruthy()
    const pegRes = await page.request.post(`/api/sales-orders/${so.order.id}/pegging/run`, { headers, data: { horizonDays: 60 } })
    expect(pegRes.status()).toBe(201)
    const peg = await pegRes.json()
    expect(peg.nodes.some((x: any) => x.nodeType === 'RESCHEDULE_RUN')).toBeTruthy()
    expect(peg.edges.some((x: any) => x.edgeType === 'RESCHEDULED_BY')).toBeTruthy()
  })
})
