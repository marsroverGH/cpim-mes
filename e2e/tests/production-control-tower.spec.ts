import { test, expect } from '@playwright/test'

async function adminAuth(page: any) {
  const loginRes = await page.request.post('/api/auth/login', {
    data: {
      username: 'admin',
      password: 'admin123'
    }
  })

  expect(loginRes.ok()).toBeTruthy()

  const auth = await loginRes.json()
  expect(auth.token).toBeTruthy()

  const headers = {
    Authorization: `Bearer ${auth.token}`
  }

  const meRes = await page.request.get('/api/auth/me', {
    headers
  })

  expect(meRes.ok()).toBeTruthy()

  const me = await meRes.json()

  expect(me.userId).toBeTruthy()
  expect(me.role).toBe('admin')

  return {
    token: auth.token as string,
    headers,
    userId: me.userId as string
  }
}

async function jsonArray(
  page: any,
  path: string,
  headers: any
) {
  const res = await page.request.get(path, { headers })

  expect(
    res.ok(),
    `GET ${path} returned ${res.status()}`
  ).toBeTruthy()

  return await res.json() as any[]
}

test.describe(
  'Production Control Tower / Constraint & Exception Prioritization',
  () => {
    test(
      'business impact drives a stable intervention case with recommendations and auditable workflow',
      async ({ page }) => {
        const auth = await adminAuth(page)
        const { headers, userId } = auth

        const suffix = Date.now().toString()

        //
        // 1. Create deterministic Sales Order / Pegging evidence.
        //

        const items = await jsonArray(
          page,
          '/api/items',
          headers
        )

        const frame = items.find(
          (x: any) => x.code === 'FRAME-1'
        )

        expect(frame).toBeTruthy()

        const customerRes = await page.request.post(
          '/api/customers',
          {
            headers,
            data: {
              customerNo: `CT-${suffix}`,
              name: `Control Tower Customer ${suffix}`,
              status: 'ACTIVE',
              shipTo: 'Tokyo'
            }
          }
        )

        expect(customerRes.status()).toBe(201)

        const customer = await customerRes.json()

        const requested =
          new Date(
            Date.now() + 2 * 86400000
          ).toISOString().slice(0, 10)

        const promised =
          new Date(
            Date.now() + 12 * 86400000
          ).toISOString().slice(0, 10)

        const orderRes = await page.request.post(
          '/api/sales-orders',
          {
            headers,
            data: {
              orderNo: `SO-CT-${suffix}`,
              customerId: customer.id,
              requestedDate: requested,
              promisedDate: promised,
              priority: 'EXPEDITE',
              lines: [
                {
                  itemId: frame.id,
                  quantity: 2,
                  unitPrice: 3000000,
                  requestedDate: requested,
                  promisedDate: promised
                }
              ]
            }
          }
        )

        expect(orderRes.status()).toBe(201)

        let detail = await orderRes.json()

        const confirmRes = await page.request.post(
          `/api/sales-orders/${detail.order.id}/confirm`,
          { headers }
        )

        expect(confirmRes.ok()).toBeTruthy()

        detail = await confirmRes.json()

        expect(detail.order.status).toBe('CONFIRMED')

        //
        // 2. Generate immutable Pegging / Exception evidence.
        //

        const peggingRes = await page.request.post(
          `/api/sales-orders/${detail.order.id}/pegging/run`,
          {
            headers,
            data: {
              horizonDays: 180
            }
          }
        )

        expect(peggingRes.status()).toBe(201)

        const graph = await peggingRes.json()

        expect(graph.run.status).toBe('SUCCEEDED')
        expect(graph.run.resultHash).toHaveLength(64)

        const late = graph.exceptions.find(
          (x: any) =>
            x.exceptionType === 'LATE_PROMISE'
        )

        expect(late).toBeTruthy()
        expect(late.impactDays).toBeGreaterThan(0)

        //
        // 3. Refresh Production Control Tower.
        //

        const refreshRes = await page.request.post(
          '/api/control-tower/refresh',
          {
            headers,
            data: {}
          }
        )

        expect(refreshRes.status()).toBe(201)

        const refresh = await refreshRes.json()

        expect(refresh.exceptionsEvaluated)
          .toBeGreaterThan(0)

        expect(refresh.casesTouched)
          .toBeGreaterThan(0)

        //
        // 4. Find the intervention case for this Sales Order.
        //

        const dashboardRes = await page.request.get(
          '/api/control-tower',
          { headers }
        )

        expect(dashboardRes.ok()).toBeTruthy()

        const dashboard = await dashboardRes.json()

        expect(dashboard.summary).toBeTruthy()
        expect(Array.isArray(dashboard.cases))
          .toBeTruthy()

        const ctCase = dashboard.cases.find(
          (x: any) =>
            x.salesOrderId === detail.order.id &&
            x.exceptionType === 'LATE_PROMISE'
        )

        expect(ctCase).toBeTruthy()

        expect(
          ['P1', 'P2', 'P3', 'P4']
        ).toContain(ctCase.priorityBand)

        expect(
          Number(ctCase.priorityScore)
        ).toBeGreaterThan(0)

        // 2 units × ¥3,000,000 = ¥6,000,000 open revenue at risk.
        expect(
          Number(ctCase.revenueAtRisk)
        ).toBeCloseTo(6000000, 2)

        expect(
          Number(ctCase.openOrderValue)
        ).toBeCloseTo(6000000, 2)

        expect(ctCase.snapshotId).toBeTruthy()
        expect(ctCase.currentStatus).toBe('OPEN')

        const firstSnapshotId = ctCase.snapshotId

        //
        // 5. Recommendation Engine must convert root cause into
        //    concrete planner interventions.
        //

        const recRes = await page.request.get(
          `/api/control-tower/cases/${ctCase.caseId}/recommendations`,
          { headers }
        )

        expect(recRes.ok()).toBeTruthy()

        const recommendations = await recRes.json()

        expect(recommendations.length)
          .toBeGreaterThanOrEqual(2)

        expect(
          recommendations[0].actionType
        ).toBe('RECALCULATE_PROMISE')

        expect(
          recommendations.some(
            (x: any) =>
              x.actionType === 'CONTACT_CUSTOMER'
          )
        ).toBeTruthy()

        //
        // 6. Refresh is idempotent while evaluated evidence / score band
        //    has not changed. The same canonical snapshot must be reused.
        //

        const secondRefreshRes =
          await page.request.post(
            '/api/control-tower/refresh',
            {
              headers,
              data: {}
            }
          )

        expect(secondRefreshRes.status()).toBe(201)

        const secondDashboardRes =
          await page.request.get(
            '/api/control-tower',
            { headers }
          )

        expect(secondDashboardRes.ok()).toBeTruthy()

        const secondDashboard =
          await secondDashboardRes.json()

        const caseAfterRefresh =
          secondDashboard.cases.find(
            (x: any) =>
              x.caseId === ctCase.caseId
          )

        expect(caseAfterRefresh).toBeTruthy()

        // Canonical result hash prevents snapshot churn.
        expect(
          caseAfterRefresh.snapshotId
        ).toBe(firstSnapshotId)

        //
        // 7. Unauthenticated workflow mutation must be rejected.
        //

        const anonymousMutation =
          await page.request.post(
            `/api/control-tower/cases/${ctCase.caseId}/actions`,
            {
              data: {
                actionType: 'ACKNOWLEDGE',
                comment: 'must not be accepted'
              }
            }
          )

        expect(anonymousMutation.status()).toBe(401)

        //
        // 8. Exercise append-only lifecycle:
        //    OPEN -> ACKNOWLEDGED -> ASSIGNED
        //         -> IN_PROGRESS -> RESOLVED -> CLOSED
        //

        const ackRes = await page.request.post(
          `/api/control-tower/cases/${ctCase.caseId}/actions`,
          {
            headers,
            data: {
              actionType: 'ACKNOWLEDGE',
              comment: 'E2E acknowledged business risk'
            }
          }
        )

        expect(ackRes.status()).toBe(201)

        const ack = await ackRes.json()

        expect(ack.fromStatus).toBe('OPEN')
        expect(ack.toStatus).toBe('ACKNOWLEDGED')

        const assignRes = await page.request.post(
          `/api/control-tower/cases/${ctCase.caseId}/actions`,
          {
            headers,
            data: {
              actionType: 'ASSIGN',
              assignedToUserId: userId,
              comment: 'Assigned to admin for E2E recovery'
            }
          }
        )

        expect(assignRes.status()).toBe(201)

        const assign = await assignRes.json()

        expect(assign.fromStatus).toBe('ACKNOWLEDGED')
        expect(assign.toStatus).toBe('ASSIGNED')
        expect(assign.assignedToUserId).toBe(userId)

        const startRes = await page.request.post(
          `/api/control-tower/cases/${ctCase.caseId}/actions`,
          {
            headers,
            data: {
              actionType: 'START',
              comment: 'Recovery work started'
            }
          }
        )

        expect(startRes.status()).toBe(201)

        const start = await startRes.json()

        expect(start.fromStatus).toBe('ASSIGNED')
        expect(start.toStatus).toBe('IN_PROGRESS')

        const resolveRes = await page.request.post(
          `/api/control-tower/cases/${ctCase.caseId}/actions`,
          {
            headers,
            data: {
              actionType: 'RESOLVE',
              comment: 'Recovery action completed'
            }
          }
        )

        expect(resolveRes.status()).toBe(201)

        const resolve = await resolveRes.json()

        expect(resolve.fromStatus).toBe('IN_PROGRESS')
        expect(resolve.toStatus).toBe('RESOLVED')

        const closeRes = await page.request.post(
          `/api/control-tower/cases/${ctCase.caseId}/actions`,
          {
            headers,
            data: {
              actionType: 'CLOSE',
              comment: 'Case formally closed'
            }
          }
        )

        expect(closeRes.status()).toBe(201)

        const close = await closeRes.json()

        expect(close.fromStatus).toBe('RESOLVED')
        expect(close.toStatus).toBe('CLOSED')

        //
        // 9. Verify current projection and append-only history.
        //

        const caseRes = await page.request.get(
          `/api/control-tower/cases/${ctCase.caseId}`,
          { headers }
        )

        expect(caseRes.ok()).toBeTruthy()

        const finalCase = await caseRes.json()

        expect(finalCase.currentStatus).toBe('CLOSED')
        expect(finalCase.ownerUserId).toBe(userId)

        const actionsRes = await page.request.get(
          `/api/control-tower/cases/${ctCase.caseId}/actions`,
          { headers }
        )

        expect(actionsRes.ok()).toBeTruthy()

        const actions = await actionsRes.json()

        expect(actions.length).toBeGreaterThanOrEqual(5)

        expect(
          actions.slice(-5).map(
            (x: any) => x.actionType
          )
        ).toEqual([
          'ACKNOWLEDGE',
          'ASSIGN',
          'START',
          'RESOLVE',
          'CLOSE'
        ])

        //
        // 10. Verify actual Production Control Tower UI.
        //

        await page.goto('/login')

        await page
          .getByLabel('ユーザー名')
          .fill('admin')

        await page
          .getByLabel('パスワード')
          .fill('admin123')

        await page
          .getByRole('button', {
            name: 'ログイン'
          })
          .click()

        await expect(page).toHaveURL('/')

        await page.goto('/production-control-tower')

        await expect(
          page.getByRole('heading', {
            name: 'Production Control Tower'
          })
        ).toBeVisible()

        await expect(
          page.getByText('Revenue at Risk')
        ).toBeVisible()

        // The dashboard is paginated. Filter to CLOSED first so the case
        // completed by this test is rendered in the current table page.
        // Vuetify's generated ARIA structure is implementation-dependent,
        // so use the explicit E2E contract exposed by this page.
        const statusFilter =
          page.getByTestId('control-tower-status-filter')

        await expect(statusFilter).toBeVisible()

        // Vuetify VSelect can be covered by its field wrapper, so avoid
        // pointer hit-testing entirely. Focus the real input and open the
        // menu through its keyboard interaction contract.
        const selectInput =
          statusFilter.locator('input')

        await expect(selectInput).toBeVisible()

        await selectInput.focus()
        await selectInput.press('ArrowDown')

        const activeOverlay =
          page
            .locator('.v-overlay--active')
            .filter({
              hasText: 'CLOSED'
            })
            .last()

        await expect(activeOverlay).toBeVisible()

        await activeOverlay
          .getByText('CLOSED', {
            exact: true
          })
          .last()
          .click()

        const filteredDashboard =
          page.waitForResponse(response => {
            if (response.request().method() !== 'GET') {
              return false
            }

            const url = new URL(response.url())

            return (
              url.pathname.endsWith('/api/control-tower') &&
              url.searchParams.get('status') === 'CLOSED'
            )
          })

        await page
          .getByTestId('control-tower-filter-button')
          .click()

        const filteredResponse =
          await filteredDashboard

        expect(filteredResponse.ok()).toBeTruthy()

        // This test closes only the selected LATE_PROMISE case for this
        // Sales Order, so order + exception type identifies the exact row.
        const caseRow = page
          .getByRole('row')
          .filter({
            hasText: detail.order.orderNo
          })
          .filter({
            hasText: ctCase.exceptionType
          })

        await expect(caseRow).toHaveCount(1)
        await expect(caseRow).toBeVisible()

        await expect(
          caseRow.getByText('CLOSED')
        ).toBeVisible()

        await caseRow
          .getByRole('button', {
            name: 'Detail'
          })
          .click()

        await expect(
          page.getByText(
            'Recommended Interventions'
          )
        ).toBeVisible()

        await expect(
          page.getByText(
            'Case History'
          )
        ).toBeVisible()

        await expect(
          page.getByText(
            'RECALCULATE_PROMISE',
            { exact: false }
          ).first()
        ).toBeVisible()
      }
    )
  }
)
