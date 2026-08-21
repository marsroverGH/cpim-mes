import { test, expect } from '@playwright/test'

async function adminToken(page: any) {
  const loginRes = await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'admin123' } })
  expect(loginRes.ok()).toBeTruthy()
  const auth = await loginRes.json()
  expect(auth.token).toBeTruthy()
  return auth.token as string
}

function isoDay(offset: number) {
  return new Date(Date.now() + offset * 86400000).toISOString().slice(0, 10)
}

function isoTime(offset: number) {
  return new Date(`${isoDay(offset)}T00:00:00Z`).toISOString()
}

async function jsonArray(page: any, path: string, headers: any) {
  const res = await page.request.get(path, { headers })
  expect(res.ok()).toBeTruthy()
  return await res.json() as any[]
}

async function createPO(page: any, headers: any, data: any) {
  const res = await page.request.post('/api/purchase-orders', { headers, data })
  expect(res.status()).toBe(201)
  return await res.json()
}

test.describe('Supplier Scheduling / Lead-Time Reliability', () => {
  test('confirmation, ASN and actual receipt history drive a canonical supplier planning date', async ({ page }) => {
    const token = await adminToken(page)
    const headers = { Authorization: `Bearer ${token}` }
    const suffix = Date.now().toString()

    const items = await jsonArray(page, '/api/items', headers)
    const purchased = items.find((x: any) => x.type === 'RM' || x.type === 'PP')
    expect(purchased).toBeTruthy()
    const supplier = `E2E-LT-${suffix}`

    // Current open PO: supplier confirmation becomes planning evidence.
    const current = await createPO(page, headers, {
      poNo: `PO-SCHED-${suffix}`,
      itemId: purchased.id,
      supplier,
      quantity: 12,
      orderDate: isoTime(0),
      dueDate: isoTime(5),
      status: 'OPEN'
    })
    const confirmedDate = isoDay(8)
    const confirmEventId = crypto.randomUUID()
    const confirmBody = { eventId: confirmEventId, eventType: 'CONFIRM', quantity: 12, confirmedDeliveryDate: confirmedDate, supplierReference: `CONF-${suffix}` }
    const confirm = await page.request.post(`/api/purchase-orders/${current.id}/supplier-schedule/events`, { headers, data: confirmBody })
    expect(confirm.status()).toBe(201)
    const retry = await page.request.post(`/api/purchase-orders/${current.id}/supplier-schedule/events`, { headers, data: confirmBody })
    expect(retry.status()).toBe(201)
    expect((await retry.json()).id).toBe(confirmEventId)

    let pos = await jsonArray(page, '/api/purchase-orders', headers)
    let planned = pos.find((x: any) => x.id === current.id)
    expect(planned).toBeTruthy()
    expect(planned.scheduleSource).toBe('SUPPLIER_CONFIRMATION')
    expect(planned.expectedDeliveryDate.slice(0, 10)).toBe(confirmedDate)

    // ASN is stronger evidence than the supplier confirmation.
    const asnDate = isoDay(7)
    const asn = await page.request.post(`/api/purchase-orders/${current.id}/supplier-schedule/events`, {
      headers,
      data: { eventId: crypto.randomUUID(), eventType: 'ASN', quantity: 12, asnNo: `ASN-${suffix}`, expectedArrivalDate: asnDate }
    })
    expect(asn.status()).toBe(201)
    pos = await jsonArray(page, '/api/purchase-orders', headers)
    planned = pos.find((x: any) => x.id === current.id)
    expect(planned.scheduleSource).toBe('ASN')
    expect(planned.expectedDeliveryDate.slice(0, 10)).toBe(asnDate)

    const historyRes = await page.request.get(`/api/purchase-orders/${current.id}/supplier-schedule`, { headers })
    expect(historyRes.ok()).toBeTruthy()
    const history = await historyRes.json()
    expect(history.map((x: any) => x.eventType)).toEqual(['CONFIRM', 'ASN'])
    expect(history).toHaveLength(2)

    // Three immutable actual receipt samples establish supplier+item reliability.
    for (let i = 0; i < 3; i++) {
      const historical = await createPO(page, headers, {
        poNo: `PO-LT-${suffix}-${i}`,
        itemId: purchased.id,
        supplier,
        quantity: 1,
        orderDate: isoTime(-(12 + i * 2)),
        dueDate: isoTime(-(2 + i)),
        status: 'OPEN'
      })
      const receive = await page.request.post(`/api/purchase-orders/${historical.id}/receive`, {
        headers,
        data: { receiptId: crypto.randomUUID(), quantity: 1, lotNo: `LT-${suffix}-${i}` }
      })
      expect(receive.ok()).toBeTruthy()
    }

    const refresh = await page.request.post('/api/supplier-scheduling/reliability/refresh', {
      headers,
      data: { windowDays: 365, minSamples: 3 }
    })
    expect(refresh.status()).toBe(201)
    const run = await refresh.json()
    expect(run.run.status).toBe('COMPLETE')
    expect(run.run.resultHash).toHaveLength(64)
    const exact = run.results.find((x: any) => x.supplierName === supplier && x.itemId === purchased.id)
    expect(exact).toBeTruthy()
    expect(exact.sampleCount).toBe(3)
    expect(exact.recommendedLeadDays).toBeGreaterThanOrEqual(Math.ceil(exact.p90LeadDays - 0.000001))
    expect(['MEDIUM', 'HIGH']).toContain(exact.confidence)

    // A new unconfirmed PO now uses the reliability-adjusted planning date.
    const unconfirmed = await createPO(page, headers, {
      poNo: `PO-REL-${suffix}`,
      itemId: purchased.id,
      supplier,
      quantity: 2,
      orderDate: isoTime(0),
      dueDate: isoTime(1),
      status: 'OPEN'
    })
    pos = await jsonArray(page, '/api/purchase-orders', headers)
    const rel = pos.find((x: any) => x.id === unconfirmed.id)
    expect(rel).toBeTruthy()
    expect(rel.scheduleSource).toBe('RELIABILITY')
    expect(rel.reliabilitySampleCount).toBeGreaterThanOrEqual(3)
    expect(rel.recommendedLeadTimeDays).toBe(exact.recommendedLeadDays)
    expect(rel.expectedDeliveryDate.slice(0, 10) >= rel.dueDate.slice(0, 10)).toBeTruthy()

    const latestRes = await page.request.get('/api/supplier-scheduling/reliability', { headers })
    expect(latestRes.ok()).toBeTruthy()
    const latest = await latestRes.json()
    expect(latest.some((x: any) => x.supplierName === supplier && x.itemId === purchased.id)).toBeTruthy()
  })
})
