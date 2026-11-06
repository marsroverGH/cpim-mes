import { test, expect } from '@playwright/test'
import { E2E_ADMIN_USERNAME, E2E_ADMIN_PASSWORD } from '../support/credentials'

async function tokenFor(request: any, username: string, password: string) {
  const res = await request.post('/api/auth/token', { data: { username, password } })
  expect(res.ok()).toBeTruthy()
  return (await res.json()).token as string
}

const authz = (token: string) => ({ Authorization: `Bearer ${token}` })
const day = (offset: number) => {
  const d = new Date(); d.setUTCDate(d.getUTCDate() + offset)
  return d.toISOString().slice(0, 10)
}

function isoDay(value: string) { return new Date(value).toISOString().slice(0, 10) }

function shiftDay(value: string, delta: number) {
  const d = new Date(`${value}T00:00:00Z`)
  d.setUTCDate(d.getUTCDate() + delta)
  return d.toISOString().slice(0, 10)
}

test.describe.serial('0042 Controlled Recovery Plan Execution', () => {
  test('published evidence requires preflight + independent admin approval before operational mutation', async ({ page, request }) => {
    const adminToken = await tokenFor(request, E2E_ADMIN_USERNAME, E2E_ADMIN_PASSWORD)
    const adminHeaders = authz(adminToken)
    const suffix = Date.now().toString()
    const plannerUsername = `recovery-planner-${suffix}`
    const plannerPassword = 'Recovery-Planner-Password-2026!'

    const plannerRes = await request.post('/api/admin/users', {
      headers: adminHeaders,
      data: { username: plannerUsername, password: plannerPassword, role: 'planner' }
    })
    expect(plannerRes.status()).toBe(201)
    const plannerToken = await tokenFor(request, plannerUsername, plannerPassword)
    const plannerHeaders = authz(plannerToken)

    const itemsRes = await request.get('/api/items', { headers: adminHeaders })
    expect(itemsRes.ok()).toBeTruthy()
    const items = await itemsRes.json()
    const wheel = items.find((x: any) => x.code === 'WHEEL-1')
    expect(wheel).toBeTruthy()

    const woStart = day(5)
    const woDue = day(8)
    const orderNo = `WO-REC-EXEC-${suffix}`
    const woRes = await request.post('/api/work-orders', {
      headers: adminHeaders,
      data: { orderNo, itemId: wheel.id, quantity: 1, startDate: woStart, dueDate: woDue, status: 'PLANNED' }
    })
    expect(woRes.status()).toBe(201)
    const wo = await woRes.json()

    const scenarioRes = await request.post('/api/recovery-scenarios', {
      headers: plannerHeaders,
      data: { name: `Executable Recovery ${suffix}`, description: '0042 E2E controlled execution' }
    })
    expect(scenarioRes.status()).toBe(201)
    const scenario = await scenarioRes.json()

    const actionRes = await request.post(`/api/recovery-scenarios/${scenario.id}/actions`, {
      headers: plannerHeaders,
      data: {
        actionType: 'RESCHEDULE_WO', targetType: 'WORK_ORDER', targetRef: `WO:${orderNo}`,
        parameters: { recoveryDays: 1 }, estimatedCost: 1000, note: 'E2E controlled date recovery'
      }
    })
    expect(actionRes.status()).toBe(201)

    const simRes = await request.post(`/api/recovery-scenarios/${scenario.id}/simulate`, {
      headers: plannerHeaders, data: { horizonDays: 90 }
    })
    expect(simRes.status()).toBe(201)
    const sim = await simRes.json()
    expect(sim.run.status).toBe('SUCCEEDED')

    // 0041 invariant: simulation is still side-effect free.
    let woList = await (await request.get('/api/work-orders', { headers: adminHeaders })).json()
    let current = woList.find((x: any) => x.id === wo.id)
    expect(isoDay(current.startDate)).toBe(woStart)
    expect(isoDay(current.dueDate)).toBe(woDue)

    const publishRes = await request.post(`/api/recovery-scenarios/${scenario.id}/publish`, {
      headers: plannerHeaders, data: { runId: sim.run.id, comment: 'Approved planning evidence only' }
    })
    expect(publishRes.status()).toBe(201)

    // Publish remains evidence-only as well.
    woList = await (await request.get('/api/work-orders', { headers: adminHeaders })).json()
    current = woList.find((x: any) => x.id === wo.id)
    expect(isoDay(current.startDate)).toBe(woStart)
    expect(isoDay(current.dueDate)).toBe(woDue)

    const prepRes = await request.post(`/api/recovery-scenarios/${scenario.id}/executions`, {
      headers: plannerHeaders,
      data: { idempotencyKey: `e2e-${suffix}`, comment: 'Planner requests controlled execution' }
    })
    expect(prepRes.status()).toBe(201)
    let execution = await prepRes.json()
    expect(execution.execution.status).toBe('PREPARED')
    expect(execution.actions).toHaveLength(1)

    const repeatPrepare = await request.post(`/api/recovery-scenarios/${scenario.id}/executions`, {
      headers: plannerHeaders,
      data: { idempotencyKey: `e2e-${suffix}`, comment: 'same key must reuse execution' }
    })
    expect(repeatPrepare.ok()).toBeTruthy()
    expect((await repeatPrepare.json()).execution.id).toBe(execution.execution.id)

    const pfRes = await request.post(`/api/recovery-executions/${execution.execution.id}/preflight`, { headers: plannerHeaders, data: {} })
    expect(pfRes.ok()).toBeTruthy()
    execution = await pfRes.json()
    expect(execution.execution.status).toBe('PREFLIGHT_PASSED')
    expect(execution.actions[0].preflightStatus).toBe('READY')
    expect(execution.execution.preflightHash).toHaveLength(64)

    // Planner cannot self-approve; approval is admin-only and DB also enforces
    // prepared_by != approved_by.
    const plannerApprove = await request.post(`/api/recovery-executions/${execution.execution.id}/approve`, {
      headers: plannerHeaders, data: { comment: 'must not be accepted' }
    })
    expect(plannerApprove.status()).toBe(403)

    const approveRes = await request.post(`/api/recovery-executions/${execution.execution.id}/approve`, {
      headers: adminHeaders, data: { comment: 'Independent admin approval' }
    })
    expect(approveRes.ok()).toBeTruthy()
    execution = await approveRes.json()
    expect(execution.execution.status).toBe('APPROVED')
    expect(execution.execution.approvalHash).toHaveLength(64)
    expect(execution.execution.approvedByUsername).toBe(E2E_ADMIN_USERNAME)

    // Still no operational mutation before explicit execute.
    woList = await (await request.get('/api/work-orders', { headers: adminHeaders })).json()
    current = woList.find((x: any) => x.id === wo.id)
    expect(isoDay(current.startDate)).toBe(woStart)
    expect(isoDay(current.dueDate)).toBe(woDue)

    const executeRes = await request.post(`/api/recovery-executions/${execution.execution.id}/execute`, { headers: adminHeaders, data: {} })
    expect(executeRes.ok()).toBeTruthy()
    execution = await executeRes.json()
    expect(execution.execution.status).toBe('SUCCEEDED')
    expect(execution.execution.succeededActionCount).toBe(1)
    expect(execution.execution.executionResultHash).toHaveLength(64)
    expect(execution.actions[0].executionStatus).toBe('SUCCEEDED')
    expect(execution.actions[0].resultHash).toHaveLength(64)
    expect(execution.actions[0].operationalRef).toBe(`WO:${orderNo}`)

    woList = await (await request.get('/api/work-orders', { headers: adminHeaders })).json()
    current = woList.find((x: any) => x.id === wo.id)
    expect(isoDay(current.startDate)).toBe(shiftDay(woStart, -1))
    expect(isoDay(current.dueDate)).toBe(shiftDay(woDue, -1))

    // Terminal execute is idempotent/read-only: it returns the durable result
    // and must not apply the date shift a second time.
    const repeatExecute = await request.post(`/api/recovery-executions/${execution.execution.id}/execute`, { headers: adminHeaders, data: {} })
    expect(repeatExecute.ok()).toBeTruthy()
    woList = await (await request.get('/api/work-orders', { headers: adminHeaders })).json()
    current = woList.find((x: any) => x.id === wo.id)
    expect(isoDay(current.startDate)).toBe(shiftDay(woStart, -1))
    expect(isoDay(current.dueDate)).toBe(shiftDay(woDue, -1))

    // A wildcard action is valid What-if evidence but is never executable.
    const wildcardScenarioRes = await request.post('/api/recovery-scenarios', {
      headers: plannerHeaders,
      data: { name: `Wildcard Recovery ${suffix}`, description: 'simulation-only wildcard' }
    })
    expect(wildcardScenarioRes.status()).toBe(201)
    const wildcardScenario = await wildcardScenarioRes.json()
    const wildcardActionRes = await request.post(`/api/recovery-scenarios/${wildcardScenario.id}/actions`, {
      headers: plannerHeaders,
      data: {
        actionType: 'RELEASE_WO', targetType: 'WORK_ORDER', targetRef: '*',
        parameters: {}, estimatedCost: 0, note: 'simulation-only wildcard'
      }
    })
    expect(wildcardActionRes.status()).toBe(201)
    const wildcardSimRes = await request.post(`/api/recovery-scenarios/${wildcardScenario.id}/simulate`, {
      headers: plannerHeaders, data: { horizonDays: 90 }
    })
    expect(wildcardSimRes.status()).toBe(201)
    const wildcardSim = await wildcardSimRes.json()
    const wildcardPublishRes = await request.post(`/api/recovery-scenarios/${wildcardScenario.id}/publish`, {
      headers: plannerHeaders, data: { runId: wildcardSim.run.id, comment: 'simulation evidence' }
    })
    expect(wildcardPublishRes.status()).toBe(201)
    const wildcardPrepRes = await request.post(`/api/recovery-scenarios/${wildcardScenario.id}/executions`, {
      headers: plannerHeaders,
      data: { idempotencyKey: `wildcard-${suffix}`, comment: 'must block at preflight' }
    })
    expect(wildcardPrepRes.status()).toBe(201)
    const wildcardPrepared = await wildcardPrepRes.json()
    const wildcardPFRes = await request.post(`/api/recovery-executions/${wildcardPrepared.execution.id}/preflight`, {
      headers: plannerHeaders, data: {}
    })
    expect(wildcardPFRes.ok()).toBeTruthy()
    const wildcardExecution = await wildcardPFRes.json()
    expect(wildcardExecution.execution.status).toBe('PREFLIGHT_BLOCKED')
    expect(wildcardExecution.actions[0].preflightStatus).toBe('BLOCKED')
    expect(wildcardExecution.actions[0].errorText).toContain('wildcard targetRef is simulation-only')

    // UI smoke: execution page is visible to the authenticated administrator.
    await page.goto('/login')
    await page.getByLabel('ユーザー名').fill(E2E_ADMIN_USERNAME)
    await page.getByLabel('パスワード').fill(E2E_ADMIN_PASSWORD)
    await page.getByRole('button', { name: 'ログイン' }).click()
    await page.goto('/recovery-execution')
    await expect(page.getByRole('heading', { name: 'Controlled Recovery Execution' })).toBeVisible()
    await expect(page.getByText('Published recovery evidence')).toBeVisible()
  })
})
