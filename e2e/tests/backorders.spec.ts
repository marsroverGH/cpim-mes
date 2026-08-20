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

test.describe('Backorder Processing / Product Allocation', () => {
  test('priority + allocation Preview is side-effect free and Publish is auditable', async ({ page }) => {
    const token = await adminToken(page)
    const headers = { Authorization: `Bearer ${token}` }
    const suffix = Date.now().toString()

    const items = await jsonArray(page, '/api/items', headers)
    const frame = items.find((x: any) => x.code === 'FRAME-1')
    expect(frame).toBeTruthy()

    const serviceClasses = await jsonArray(page, '/api/customer-service-classes', headers)
    expect(serviceClasses.some((x: any) => x.code === 'STRATEGIC')).toBeTruthy()
    expect(serviceClasses.some((x: any) => x.code === 'OTHER')).toBeTruthy()

    async function createCustomer(no: string, cls: string) {
      const res = await page.request.post('/api/customers', { headers, data: { customerNo: no, name: no, status: 'ACTIVE', shipTo: 'Tokyo' } })
      expect(res.status()).toBe(201)
      const c = await res.json()
      const clsRes = await page.request.put(`/api/customers/${c.id}/service-class`, { headers, data: { serviceClassCode: cls } })
      expect(clsRes.ok()).toBeTruthy()
      return await clsRes.json()
    }
    const strategic = await createCustomer(`BOP-S-${suffix}`, 'STRATEGIC')
    const other = await createCustomer(`BOP-O-${suffix}`, 'OTHER')

    const existingPlans = await jsonArray(page, '/api/product-allocation-plans', headers)
    for (const existing of existingPlans.filter((x: any) => x.plan.itemId === frame.id && x.plan.status === 'ACTIVE')) {
      const d = await page.request.post(`/api/product-allocation-plans/${existing.plan.id}/deactivate`, { headers })
      expect(d.ok()).toBeTruthy()
    }

    const today = new Date().toISOString().slice(0, 10)
    const to = new Date(Date.now() + 90 * 86400000).toISOString().slice(0, 10)
    const planRes = await page.request.post('/api/product-allocation-plans', {
      headers,
      data: {
        itemId: frame.id, name: `BOP Allocation ${suffix}`, effectiveFrom: today, effectiveTo: to,
        buckets: [
          { serviceClassCode: 'STRATEGIC', allocationPct: 65, priorityRank: 1 },
          { serviceClassCode: 'STANDARD', allocationPct: 30, priorityRank: 2 },
          { serviceClassCode: 'OTHER', allocationPct: 5, priorityRank: 3 }
        ]
      }
    })
    expect(planRes.status()).toBe(201)
    let plan = await planRes.json()
    const activateRes = await page.request.post(`/api/product-allocation-plans/${plan.plan.id}/activate`, { headers })
    expect(activateRes.ok()).toBeTruthy()
    plan = await activateRes.json()
    expect(plan.plan.status).toBe('ACTIVE')

    const requested = new Date(Date.now() + 2 * 86400000).toISOString().slice(0, 10)
    async function createConfirmedOrder(customer: any, label: string, priority: string) {
      const res = await page.request.post('/api/sales-orders', {
        headers,
        data: { orderNo: `SO-${label}-${suffix}`, customerId: customer.id, requestedDate: requested, lines: [{ itemId: frame.id, quantity: 4, unitPrice: 10000, requestedDate: requested }] }
      })
      expect(res.status()).toBe(201)
      let detail = await res.json()
      const p = await page.request.put(`/api/sales-orders/${detail.order.id}/priority`, { headers, data: { priority } })
      expect(p.ok()).toBeTruthy()
      const confirm = await page.request.post(`/api/sales-orders/${detail.order.id}/confirm`, { headers })
      expect(confirm.ok()).toBeTruthy()
      detail = await confirm.json()
      expect(detail.order.status).toBe('CONFIRMED')
      return detail
    }
    const strategicOrder = await createConfirmedOrder(strategic, 'STRATEGIC', 'NORMAL')
    const expediteOtherOrder = await createConfirmedOrder(other, 'EXPEDITE', 'EXPEDITE')

    const beforeWO = (await jsonArray(page, '/api/work-orders', headers)).length
    const beforePO = (await jsonArray(page, '/api/purchase-orders', headers)).length
    const beforeSchedules = (await jsonArray(page, '/api/detailed-scheduling/runs', headers)).length
    const beforeTxns = (await jsonArray(page, `/api/inventory/${frame.id}/transactions`, headers)).length

    const previewRes = await page.request.post('/api/backorders/preview', { headers, data: { horizonDays: 90, filterItemId: frame.id } })
    expect(previewRes.status()).toBe(201)
    const preview = await previewRes.json()
    expect(preview.run.status).toBe('SUCCEEDED')
    expect(preview.run.resultHash).toHaveLength(64)
    const strategicLine = preview.lines.find((x: any) => x.salesOrderId === strategicOrder.order.id)
    const otherLine = preview.lines.find((x: any) => x.salesOrderId === expediteOtherOrder.order.id)
    expect(strategicLine).toBeTruthy()
    expect(otherLine).toBeTruthy()
    expect(otherLine.rankNo).toBeLessThan(strategicLine.rankNo) // order priority outranks service class
    expect(otherLine.allocationPlanId).toBe(plan.plan.id)
    expect(otherLine.allocationBucketPct).toBeCloseTo(5, 6)
    expect(otherLine.atpQty + otherLine.ctpQty + otherLine.backorderQty).toBeCloseTo(4, 6)

    expect((await jsonArray(page, '/api/work-orders', headers)).length).toBe(beforeWO)
    expect((await jsonArray(page, '/api/purchase-orders', headers)).length).toBe(beforePO)
    expect((await jsonArray(page, '/api/detailed-scheduling/runs', headers)).length).toBe(beforeSchedules)
    expect((await jsonArray(page, `/api/inventory/${frame.id}/transactions`, headers)).length).toBe(beforeTxns)

    const publishRes = await page.request.post('/api/backorders/publish', { headers, data: { runId: preview.run.id } })
    expect(publishRes.ok()).toBeTruthy()
    const published = await publishRes.json()
    expect(published.publication).toBeTruthy()
    expect(published.publication.runId).toBe(preview.run.id)
    expect(published.publication.resultHash).toBe(preview.run.resultHash)

    const rerunRes = await page.request.get(`/api/backorders/runs/${preview.run.id}`, { headers })
    expect(rerunRes.ok()).toBeTruthy()
    expect((await rerunRes.json()).publication).toBeTruthy()

    for (const order of [strategicOrder, expediteOtherOrder]) {
      const detailRes = await page.request.get(`/api/sales-orders/${order.order.id}`, { headers })
      expect(detailRes.ok()).toBeTruthy()
      const detail = await detailRes.json()
      const line = published.lines.find((x: any) => x.salesOrderId === order.order.id)
      if (line.backorderQty > 0) expect(detail.lines[0].promisedDate ?? null).toBeNull()
      else expect(detail.lines[0].promisedDate).toBeTruthy()
    }

    expect((await jsonArray(page, '/api/work-orders', headers)).length).toBe(beforeWO)
    expect((await jsonArray(page, '/api/purchase-orders', headers)).length).toBe(beforePO)
    expect((await jsonArray(page, '/api/detailed-scheduling/runs', headers)).length).toBe(beforeSchedules)
    expect((await jsonArray(page, `/api/inventory/${frame.id}/transactions`, headers)).length).toBe(beforeTxns)
  })
})
