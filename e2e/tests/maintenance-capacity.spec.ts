import { test, expect } from '@playwright/test'

async function adminToken(page: any) {
  const res = await page.request.post('/api/auth/login', { data: { username: 'admin', password: 'admin123' } })
  expect(res.ok()).toBeTruthy()
  const auth = await res.json()
  return auth.token as string
}

async function arr(page: any, path: string, headers: any) {
  const r = await page.request.get(path, { headers })
  expect(r.ok()).toBeTruthy()
  return await r.json() as any[]
}

function day(offset: number) {
  return new Date(Date.now() + offset * 86400000).toISOString().slice(0, 10)
}

function isoAt(offset: number, hour = 0) {
  return new Date(`${day(offset)}T${String(hour).padStart(2, '0')}:00:00Z`).toISOString()
}

test.describe('Maintenance + Capacity Downtime', () => {
  test('breakdown reduces finite capacity and is reused by CTP and pegging root-cause evidence', async ({ page }) => {
    const token = await adminToken(page)
    const headers = { Authorization: `Bearer ${token}` }
    const suffix = Date.now().toString()

    const workCenters = await arr(page, '/api/work-centers', headers)
    const assy = workCenters.find((x: any) => x.code === 'WC-ASSY')
    expect(assy).toBeTruthy()
    expect(assy.machineCount).toBeGreaterThan(0)

    // Full outage spans the entire 28-day detailed-scheduling horizon. BREAKDOWN
    // defaults to ACTIVE and removes all assembly machines from finite capacity.
    const eventRes = await page.request.post('/api/maintenance-events', {
      headers,
      data: {
        workCenterId: assy.id,
        eventType: 'BREAKDOWN',
        startAt: isoAt(0, 0),
        endAt: isoAt(40, 23),
        unavailableMachines: assy.machineCount,
        unavailableWorkers: 0,
        reason: `E2E breakdown ${suffix}`,
        sourceRef: `BRK-${suffix}`
      }
    })
    expect(eventRes.status()).toBe(201)
    const eventDetail = await eventRes.json()
    expect(eventDetail.current.status).toBe('ACTIVE')
    expect(eventDetail.current.eventType).toBe('BREAKDOWN')

    const dsRes = await page.request.post('/api/detailed-scheduling/run', {
      headers,
      data: { startDate: day(0), horizonDays: 28 }
    })
    expect(dsRes.ok()).toBeTruthy()
    const ds = await dsRes.json()
    expect(ds.run.status).toBe('COMPLETE')
    expect(ds.maintenance.some((x: any) => x.maintenanceEventId === eventDetail.event.id && x.revisionNo === 1)).toBeTruthy()
    expect(ds.batches.some((x: any) => x.workCenterId === assy.id && x.scheduleStatus === 'UNSCHEDULED')).toBeTruthy()
    expect(ds.segments.some((x: any) => x.workCenterId === assy.id)).toBeFalsy()

    // CTP uses the same finite allocator. A deliberately large new BIKE demand
    // cannot consume the unavailable assembly capacity inside the promise horizon.
    const items = await arr(page, '/api/items', headers)
    const bike = items.find((x: any) => x.code === 'BIKE-100')
    expect(bike).toBeTruthy()
    const customerRes = await page.request.post('/api/customers', {
      headers,
      data: { customerNo: `MNT-${suffix}`, name: `Maintenance CTP ${suffix}`, status: 'ACTIVE' }
    })
    expect(customerRes.status()).toBe(201)
    const customer = await customerRes.json()
    const soRes = await page.request.post('/api/sales-orders', {
      headers,
      data: {
        orderNo: `SO-MNT-${suffix}`,
        customerId: customer.id,
        requestedDate: day(7),
        lines: [{ itemId: bike.id, quantity: 10000, unitPrice: 1, requestedDate: day(7) }]
      }
    })
    expect(soRes.status()).toBe(201)
    const so = await soRes.json()
    const promiseRes = await page.request.post(`/api/sales-orders/${so.order.id}/promise/check`, {
      headers,
      data: { horizonDays: 35 }
    })
    expect(promiseRes.status()).toBe(201)
    const promise = await promiseRes.json()
    expect(promise.lines).toHaveLength(1)
    expect(['CAPACITY', 'MATERIAL', 'HORIZON']).toContain(promise.lines[0].constraintType)
    expect(promise.lines[0].atpQty + promise.lines[0].ctpQty).toBeLessThan(10000)

    // Confirm a small order and trace the latest Detailed Schedule. The exact
    // maintenance revision used by that schedule must appear in the root-cause graph.
    const smallRes = await page.request.post('/api/sales-orders', {
      headers,
      data: {
        orderNo: `SO-MNT-PEG-${suffix}`,
        customerId: customer.id,
        requestedDate: day(7),
        lines: [{ itemId: bike.id, quantity: 1, unitPrice: 1, requestedDate: day(7) }]
      }
    })
    expect(smallRes.status()).toBe(201)
    const small = await smallRes.json()
    expect((await page.request.post(`/api/sales-orders/${small.order.id}/confirm`, { headers, data: {} })).ok()).toBeTruthy()
    const pegRes = await page.request.post(`/api/sales-orders/${small.order.id}/pegging/run`, {
      headers,
      data: { horizonDays: 60 }
    })
    expect(pegRes.status()).toBe(201)
    const peg = await pegRes.json()
    expect(peg.nodes.some((x: any) => x.nodeType === 'MAINTENANCE_EVENT' && x.entityId === eventDetail.event.id)).toBeTruthy()
    expect(peg.exceptions.some((x: any) => x.exceptionType === 'BREAKDOWN_CAPACITY')).toBeTruthy()

    // Revision evidence is append-only and terminal completion removes the event
    // from future effective-capacity calculations without rewriting revision 1.
    const completeRes = await page.request.post(`/api/maintenance-events/${eventDetail.event.id}/revisions`, {
      headers,
      data: { status: 'COMPLETED', reason: `restored ${suffix}` }
    })
    expect(completeRes.status()).toBe(201)
    const completed = await completeRes.json()
    expect(completed.current.status).toBe('COMPLETED')
    expect(completed.revisions).toHaveLength(2)
    expect(completed.revisions[0].status).toBe('ACTIVE')
    expect(completed.revisions[1].status).toBe('COMPLETED')
  })
})
