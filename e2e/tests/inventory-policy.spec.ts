import { test, expect } from '@playwright/test'

async function adminToken(page: any) {
  const loginRes = await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'admin123' } })
  expect(loginRes.ok()).toBeTruthy()
  const auth = await loginRes.json()
  return auth.token as string
}
function day(offset: number) { return new Date(Date.now() + offset * 86400000).toISOString().slice(0, 10) }
function time(offset: number) { return new Date(`${day(offset)}T12:00:00Z`).toISOString() }
async function arr(page: any, path: string, headers: any) {
  const r = await page.request.get(path, { headers }); expect(r.ok()).toBeTruthy(); return await r.json() as any[]
}

test.describe('Statistical Safety Stock / Inventory Policy', () => {
  test('service-level policy drives SS/ROP/Min-Max, MRP/ATP protection, versioning and pegging exceptions', async ({ page }) => {
    const token = await adminToken(page)
    const headers = { Authorization: `Bearer ${token}` }
    const suffix = Date.now().toString()
    const items = await arr(page, '/api/items', headers)
    const frame = items.find((x: any) => x.code === 'FRAME-1')
    expect(frame).toBeTruthy()

    // Create genuine ISSUE-history variability against the seeded quality-OK lot.
    const lots = await arr(page, `/api/items/${frame.id}/lots`, headers)
    const lot = lots.find((x: any) => (x.balance ?? x.quantity) >= 10 && x.qualityStatus === 'OK')
    expect(lot).toBeTruthy()
    for (const [i, qty] of [1, 3, 2].entries()) {
      const issue = await page.request.post('/api/inventory/transactions', {
        headers,
        data: { id: crypto.randomUUID(), itemId: frame.id, quantity: -qty, txnType: 'ISSUE', refDoc: `IP-E2E-${suffix}-${i}`, lotId: lot.id, occurredAt: time(-6 + i * 2) }
      })
      expect(issue.status()).toBe(201)
    }

    const create = await page.request.post('/api/inventory-policy-versions', {
      headers,
      data: { itemId: frame.id, policyMethod: 'STATISTICAL', replenishmentMethod: 'MIN_MAX', serviceLevel: 0.95, demandWindowDays: 7, minHistoryDays: 1, orderCycleDays: 7, effectiveFrom: day(0), notes: 'E2E statistical policy' }
    })
    expect(create.status()).toBe(201)
    const statistical = await create.json()
    expect(statistical.status).toBe('DRAFT')
    const activate = await page.request.post(`/api/inventory-policy-versions/${statistical.id}/activate`, { headers, data: {} })
    expect(activate.ok()).toBeTruthy()

    const refresh = await page.request.post('/api/inventory-policies/refresh', { headers, data: { asOfDate: day(0) } })
    expect(refresh.status()).toBe(201)
    const calc = await refresh.json()
    expect(calc.run.status).toBe('COMPLETE')
    expect(calc.run.resultHash).toHaveLength(64)
    const result = calc.results.find((x: any) => x.itemId === frame.id)
    expect(result).toBeTruthy()
    expect(result.serviceLevel).toBeCloseTo(0.95, 6)
    expect(result.zValue).toBeCloseTo(1.64485, 3)
    expect(result.stddevDailyDemand).toBeGreaterThan(0)
    expect(result.safetyStock).toBeGreaterThan(0)
    expect(result.reorderPoint).toBeGreaterThan(result.safetyStock)
    expect(result.minQty).toBeCloseTo(result.reorderPoint, 6)
    expect(result.maxQty).toBeGreaterThan(result.minQty)

    const current = await arr(page, '/api/inventory-policies', headers)
    const effective = current.find((x: any) => x.itemId === frame.id)
    expect(effective.policyVersionId).toBe(statistical.id)
    expect(effective.calculationStatus).toBe('CALCULATED')
    expect(effective.replenishmentMethod).toBe('MIN_MAX')

    // MRP exposes the exact policy target used for netting.
    const mrpRes = await page.request.post('/api/mrp/run', { headers, data: { horizonDays: 35, startDate: day(0) } })
    expect(mrpRes.ok()).toBeTruthy()
    const mrp = await mrpRes.json()
    const frameMRP = mrp.find((x: any) => x.itemId === frame.id && x.inventoryPolicyId === statistical.id)
    expect(frameMRP).toBeTruthy()
    expect(frameMRP.inventoryPolicyMode).toBe('MIN_MAX')
    expect(frameMRP.safetyStockTarget).toBeCloseTo(result.safetyStock, 5)
    expect(frameMRP.reorderPoint).toBeCloseTo(result.reorderPoint, 5)

    // ATP protects statistical safety stock from new customer commitments.
    const atpRes = await page.request.get(`/api/items/${frame.id}/atp?horizonDays=28&bucketDays=7`, { headers })
    expect(atpRes.ok()).toBeTruthy()
    const atp = await atpRes.json()
    expect(atp.inventoryPolicyId).toBe(statistical.id)
    expect(atp.safetyStockProtected).toBeCloseTo(result.safetyStock, 5)
    expect(atp.serviceLevel).toBeCloseTo(0.95, 6)

    // Activate a second version; the prior version is archived, proving version semantics.
    const fixedCreate = await page.request.post('/api/inventory-policy-versions', {
      headers,
      data: { itemId: frame.id, policyMethod: 'FIXED', replenishmentMethod: 'SAFETY_STOCK', serviceLevel: 0.99, demandWindowDays: 7, minHistoryDays: 1, orderCycleDays: 7, fixedSafetyStock: 999999, effectiveFrom: day(0), notes: 'E2E breach policy' }
    })
    expect(fixedCreate.status()).toBe(201)
    const fixed = await fixedCreate.json()
    expect((await page.request.post(`/api/inventory-policy-versions/${fixed.id}/activate`, { headers, data: {} })).ok()).toBeTruthy()
    expect((await page.request.post('/api/inventory-policies/refresh', { headers, data: { asOfDate: day(0) } })).status()).toBe(201)
    const versions = await arr(page, `/api/inventory-policy-versions?itemId=${frame.id}`, headers)
    expect(versions.find((x: any) => x.id === statistical.id)?.status).toBe('ARCHIVED')
    expect(versions.find((x: any) => x.id === fixed.id)?.status).toBe('ACTIVE')

    // 0034 Full Pegging now identifies the active policy itself as the root cause.
    const customerRes = await page.request.post('/api/customers', { headers, data: { customerNo: `IP-${suffix}`, name: `Inventory Policy Customer ${suffix}`, status: 'ACTIVE' } })
    expect(customerRes.status()).toBe(201)
    const customer = await customerRes.json()
    const soRes = await page.request.post('/api/sales-orders', { headers, data: { orderNo: `SO-IP-${suffix}`, customerId: customer.id, requestedDate: day(7), lines: [{ itemId: frame.id, quantity: 1, unitPrice: 1, requestedDate: day(7) }] } })
    expect(soRes.status()).toBe(201)
    const so = await soRes.json()
    expect((await page.request.post(`/api/sales-orders/${so.order.id}/confirm`, { headers, data: {} })).ok()).toBeTruthy()
    const pegRes = await page.request.post(`/api/sales-orders/${so.order.id}/pegging/run`, { headers, data: { horizonDays: 60 } })
    expect(pegRes.status()).toBe(201)
    const peg = await pegRes.json()
    expect(peg.nodes.some((x: any) => x.nodeType === 'INVENTORY_POLICY' && x.entityId === fixed.id)).toBeTruthy()
    expect(peg.exceptions.some((x: any) => x.exceptionType === 'SAFETY_STOCK_BREACH')).toBeTruthy()
  })
})
