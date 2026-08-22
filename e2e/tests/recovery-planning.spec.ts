import { test, expect } from '@playwright/test'
import { createHash } from 'node:crypto'

async function adminToken(page: any) {
  const res = await page.request.post(
    '/api/auth/login',
    {
      data: {
        username: 'admin',
        password: 'admin123'
      }
    }
  )

  expect(res.ok()).toBeTruthy()

  const auth = await res.json()

  expect(auth.token).toBeTruthy()

  return auth.token as string
}

function canonical(value: any): any {
  if (Array.isArray(value)) {
    const rows = value.map(canonical)

    rows.sort(
      (a, b) =>
        JSON.stringify(a).localeCompare(
          JSON.stringify(b)
        )
    )

    return rows
  }

  if (
    value !== null &&
    typeof value === 'object'
  ) {
    const out: Record<string, any> = {}

    for (
      const key of Object.keys(value).sort()
    ) {
      out[key] = canonical(value[key])
    }

    return out
  }

  return value
}

function hash(value: any) {
  return createHash('sha256')
    .update(
      JSON.stringify(
        canonical(value)
      )
    )
    .digest('hex')
}

async function apiJSON(
  page: any,
  path: string,
  headers: Record<string, string>
) {
  const res =
    await page.request.get(
      path,
      { headers }
    )

  expect(
    res.ok(),
    `GET ${path}`
  ).toBeTruthy()

  return res.json()
}

async function operationalSnapshot(
  page: any,
  headers: Record<string, string>
) {
  const [
    workOrders,
    purchaseOrders,
    workCenters,
    salesOrders
  ] = await Promise.all([
    apiJSON(
      page,
      '/api/work-orders',
      headers
    ),

    apiJSON(
      page,
      '/api/purchase-orders',
      headers
    ),

    apiJSON(
      page,
      '/api/work-centers',
      headers
    ),

    apiJSON(
      page,
      '/api/sales-orders',
      headers
    )
  ])

  return {
    workOrders: hash(workOrders),
    purchaseOrders: hash(purchaseOrders),
    workCenters: hash(workCenters),
    salesOrders: hash(salesOrders)
  }
}

test.describe(
  '0041 Scenario-Based Recovery Planning',
  () => {
    test(
      'What-if simulation is canonical, comparable, publishable and operationally side-effect free',
      async ({ page }) => {
        const token =
          await adminToken(page)

        const headers = {
          Authorization: `Bearer ${token}`
        }

        const before =
          await operationalSnapshot(
            page,
            headers
          )

        const suffix =
          Date.now().toString()

        // --------------------------------------------------
        // Create DRAFT scenario.
        // --------------------------------------------------

        const createRes =
          await page.request.post(
            '/api/recovery-scenarios',
            {
              headers,
              data: {
                name:
                  `E2E Recovery ${suffix}`,

                description:
                  '0041 canonical What-if E2E'
              }
            }
          )

        expect(createRes.status()).toBe(201)

        const scenario =
          await createRes.json()

        expect(
          scenario.status
        ).toBe('DRAFT')

        expect(
          scenario.scenarioNo
        ).toBeTruthy()

        // --------------------------------------------------
        // Add one wildcard hypothetical action.
        //
        // This test remains independent from the presence
        // or absence of current Control Tower cases.
        // --------------------------------------------------

        const actionRes =
          await page.request.post(
            `/api/recovery-scenarios/${scenario.id}/actions`,
            {
              headers,
              data: {
                actionType:
                  'RELEASE_WO',

                targetType:
                  'WORK_ORDER',

                targetRef:
                  '*',

                parameters:
                  {},

                estimatedCost:
                  25000,

                note:
                  'E2E hypothetical release'
              }
            }
          )

        expect(actionRes.status()).toBe(201)

        const action =
          await actionRes.json()

        expect(
          action.actionType
        ).toBe('RELEASE_WO')

        // --------------------------------------------------
        // First simulation.
        // --------------------------------------------------

        const simulateRes =
          await page.request.post(
            `/api/recovery-scenarios/${scenario.id}/simulate`,
            {
              headers,
              data: {
                horizonDays: 90
              }
            }
          )

        expect(
          simulateRes.status()
        ).toBe(201)

        const simulation =
          await simulateRes.json()

        expect(
          simulation.run.status
        ).toBe('SUCCEEDED')

        expect(
          simulation.run.baselineHash
        ).toHaveLength(64)

        expect(
          simulation.run.requestHash
        ).toHaveLength(64)

        expect(
          simulation.run.resultHash
        ).toHaveLength(64)

        expect(
          simulation.summary.resultHash
        ).toHaveLength(64)

        expect(
          simulation.reused
        ).toBe(false)

        // Simulation must not mutate operational state.
        const afterSimulation =
          await operationalSnapshot(
            page,
            headers
          )

        expect(
          afterSimulation
        ).toEqual(before)

        // --------------------------------------------------
        // Same request must reuse canonical successful run.
        // --------------------------------------------------

        const repeatRes =
          await page.request.post(
            `/api/recovery-scenarios/${scenario.id}/simulate`,
            {
              headers,
              data: {
                horizonDays: 90
              }
            }
          )

        expect(
          repeatRes.status()
        ).toBe(201)

        const repeated =
          await repeatRes.json()

        expect(
          repeated.reused
        ).toBe(true)

        expect(
          repeated.run.id
        ).toBe(simulation.run.id)

        expect(
          repeated.run.requestHash
        ).toBe(
          simulation.run.requestHash
        )

        // --------------------------------------------------
        // Same-baseline comparison.
        // --------------------------------------------------

        const comparisonRes =
          await page.request.get(
            '/api/recovery-scenario-comparison',
            {
              headers,
              params: {
                baselineHash:
                  simulation.run.baselineHash
              }
            }
          )

        if (!comparisonRes.ok()) {
          const body =
            await comparisonRes.text()

          throw new Error(
            `comparison API failed: ` +
            `${comparisonRes.status()} ${body}`
          )
        }

        const comparisons =
          await comparisonRes.json()

        const comparison =
          comparisons.find(
            (x: any) =>
              x.scenarioId ===
              scenario.id
          )

        expect(comparison).toBeTruthy()

        expect(
          comparison.baselineHash
        ).toBe(
          simulation.run.baselineHash
        )

        expect(
          comparison.comparisonRank
        ).toBeGreaterThan(0)

        // --------------------------------------------------
        // Publish approval evidence.
        // --------------------------------------------------

        const publishRes =
          await page.request.post(
            `/api/recovery-scenarios/${scenario.id}/publish`,
            {
              headers,
              data: {
                runId:
                  simulation.run.id,

                comment:
                  'E2E approved recovery plan'
              }
            }
          )

        expect(
          publishRes.status()
        ).toBe(201)

        const publication =
          await publishRes.json()

        expect(
          publication.scenarioId
        ).toBe(scenario.id)

        expect(
          publication.runId
        ).toBe(simulation.run.id)

        expect(
          publication.publicationHash
        ).toHaveLength(64)

        // Publish is approval evidence only.
        const afterPublish =
          await operationalSnapshot(
            page,
            headers
          )

        expect(
          afterPublish
        ).toEqual(before)

        // --------------------------------------------------
        // Published lifecycle is visible and immutable.
        // --------------------------------------------------

        const detailRes =
          await page.request.get(
            `/api/recovery-scenarios/${scenario.id}`,
            { headers }
          )

        expect(
          detailRes.ok()
        ).toBeTruthy()

        const published =
          await detailRes.json()

        expect(
          published.status
        ).toBe('PUBLISHED')

        const illegalActionRes =
          await page.request.post(
            `/api/recovery-scenarios/${scenario.id}/actions`,
            {
              headers,
              data: {
                actionType:
                  'RELEASE_WO',

                targetType:
                  'WORK_ORDER',

                targetRef:
                  '*',

                parameters:
                  {},

                estimatedCost:
                  0
              }
            }
          )

        expect(
          illegalActionRes.ok()
        ).toBeFalsy()

        // --------------------------------------------------
        // UI smoke test.
        // --------------------------------------------------

        await page.goto('/login')

        await page
          .getByLabel('ユーザー名')
          .fill('admin')

        await page
          .getByLabel('パスワード')
          .fill('admin123')

        await page
          .getByRole(
            'button',
            { name: 'ログイン' }
          )
          .click()

        await expect(
          page.getByText(/admin.*\(admin\)/)
        ).toBeVisible()

        await page.goto(
          '/recovery-planning'
        )

        await expect(
          page.getByRole(
            'heading',
            { name: 'Recovery Planning' }
          )
        ).toBeVisible()

        await expect(
          page
            .getByText(
              scenario.name,
              { exact: true }
            )
            .first()
        ).toBeVisible()

        await expect(
          page.getByText(
            /Simulation and Publish create Recovery Planning evidence only/
          )
        ).toBeVisible()
      }
    )
  }
)
