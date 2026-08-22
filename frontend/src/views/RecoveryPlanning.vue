<template>
  <v-container fluid class="pa-4">
    <div class="d-flex align-center flex-wrap ga-3 mb-4">
      <div>
        <h1 class="text-h5 font-weight-bold">
          Recovery Planning
        </h1>

        <div class="text-body-2 text-medium-emphasis">
          Scenario-Based Recovery Planning / What-if Simulation
        </div>
      </div>

      <v-spacer />

      <v-btn
        color="primary"
        prepend-icon="mdi-plus"
        @click="createDialog = true"
      >
        New Scenario
      </v-btn>

      <v-btn
        variant="outlined"
        prepend-icon="mdi-refresh"
        :loading="loading"
        @click="refreshAll"
      >
        Refresh
      </v-btn>
    </div>

    <v-alert
      type="info"
      variant="tonal"
      class="mb-4"
    >
      Simulation and Publish create Recovery Planning evidence only.
      They do not directly update Purchase Orders, Work Orders,
      Work Centers, or Sales Orders.
    </v-alert>

    <!-- Comparison -->
    <v-card class="mb-4">
      <v-card-title class="d-flex align-center">
        Scenario Comparison

        <v-spacer />

        <v-chip
          v-if="activeBaselineHash"
          size="small"
          variant="outlined"
        >
          Baseline {{ shortHash(activeBaselineHash) }}
        </v-chip>
      </v-card-title>

      <v-card-text>
        <div
          v-if="comparisons.length === 0"
          class="text-medium-emphasis py-4"
        >
          Simulate at least one scenario to compare recovery plans.
        </div>

        <v-row v-else>
          <v-col
            v-for="item in comparisons"
            :key="item.scenarioId"
            cols="12"
            md="6"
            xl="4"
          >
            <v-card
              variant="outlined"
              :class="{
                'best-scenario': item.comparisonRank === 1
              }"
              height="100%"
            >
              <v-card-title class="d-flex align-center">
                <v-chip
                  v-if="item.comparisonRank === 1"
                  color="success"
                  size="small"
                  class="mr-2"
                >
                  Best
                </v-chip>

                <span class="text-subtitle-1">
                  {{ item.name }}
                </span>

                <v-spacer />

                <v-chip
                  size="small"
                  :color="statusColor(comparisonStatus(item))"
                >
                  {{ comparisonStatus(item) }}
                </v-chip>
              </v-card-title>

              <v-card-subtitle>
                {{ item.scenarioNo }}
              </v-card-subtitle>

              <v-card-text>
                <div class="score-box mb-4">
                  <div class="text-caption text-medium-emphasis">
                    Recovery Score
                  </div>

                  <div class="text-h4 font-weight-bold">
                    {{ number(item.recoveryScore, 1) }}
                  </div>
                </div>

                <v-row dense>
                  <v-col cols="6">
                    <metric
                      label="P1 Reduction"
                      :value="String(item.p1Reduction)"
                    />
                  </v-col>

                  <v-col cols="6">
                    <metric
                      label="Open Case Reduction"
                      :value="String(item.openCaseReduction)"
                    />
                  </v-col>

                  <v-col cols="6">
                    <metric
                      label="Revenue Recovered"
                      :value="money(item.recoveredRevenue)"
                    />
                  </v-col>

                  <v-col cols="6">
                    <metric
                      label="Impact Days Recovered"
                      :value="String(item.impactDaysRecovered)"
                    />
                  </v-col>

                  <v-col cols="6">
                    <metric
                      label="Action Cost"
                      :value="money(item.estimatedActionCost)"
                    />
                  </v-col>

                  <v-col cols="6">
                    <metric
                      label="Net Value"
                      :value="money(item.netValue)"
                    />
                  </v-col>
                </v-row>

                <v-divider class="my-3" />

                <div class="text-caption text-medium-emphasis mb-1">
                  Baseline → Simulated
                </div>

                <div class="text-body-2">
                  P1:
                  {{ item.baselineP1Cases }}
                  →
                  {{ item.simulatedP1Cases }}
                </div>

                <div class="text-body-2">
                  Revenue at Risk:
                  {{ money(item.baselineRevenueAtRisk) }}
                  →
                  {{ money(item.simulatedRevenueAtRisk) }}
                </div>

                <div class="text-body-2">
                  Impact Days:
                  {{ item.baselineImpactDays }}
                  →
                  {{ item.simulatedImpactDays }}
                </div>
              </v-card-text>

              <v-card-actions>
                <v-btn
                  variant="text"
                  @click="selectById(item.scenarioId)"
                >
                  Open
                </v-btn>

                <v-spacer />

                <v-chip
                  v-if="item.isPublished"
                  color="success"
                  size="small"
                  prepend-icon="mdi-check-decagram"
                >
                  Published
                </v-chip>

                <v-btn
                  v-else-if="comparisonStatus(item) === 'SIMULATED'"
                  color="success"
                  variant="flat"
                  prepend-icon="mdi-check-decagram"
                  :loading="publishingId === item.scenarioId"
                  @click="publishComparison(item)"
                >
                  Publish
                </v-btn>
              </v-card-actions>
            </v-card>
          </v-col>
        </v-row>
      </v-card-text>
    </v-card>

    <v-row>
      <!-- Scenario list -->
      <v-col cols="12" lg="4">
        <v-card height="100%">
          <v-card-title class="d-flex align-center">
            Scenarios

            <v-spacer />

            <v-select
              v-model="statusFilter"
              :items="statusItems"
              density="compact"
              variant="outlined"
              hide-details
              clearable
              label="Status"
              style="max-width: 170px"
              @update:model-value="loadScenarios"
            />
          </v-card-title>

          <v-divider />

          <v-list
            v-if="scenarios.length"
            lines="three"
          >
            <v-list-item
              v-for="scenario in scenarios"
              :key="scenario.id"
              :active="selectedScenario?.id === scenario.id"
              @click="selectScenario(scenario)"
            >
              <template #prepend>
                <v-icon>
                  mdi-flask-outline
                </v-icon>
              </template>

              <v-list-item-title>
                {{ scenario.name }}
              </v-list-item-title>

              <v-list-item-subtitle>
                {{ scenario.scenarioNo }}
              </v-list-item-subtitle>

              <v-list-item-subtitle>
                Baseline:
                {{ dateTime(scenario.baselineAsOf) }}
              </v-list-item-subtitle>

              <template #append>
                <v-chip
                  size="x-small"
                  :color="statusColor(scenario.status)"
                >
                  {{ scenario.status }}
                </v-chip>
              </template>
            </v-list-item>
          </v-list>

          <v-card-text
            v-else
            class="text-medium-emphasis"
          >
            No Recovery Scenarios.
          </v-card-text>
        </v-card>
      </v-col>

      <!-- Scenario detail -->
      <v-col cols="12" lg="8">
        <v-card v-if="selectedScenario">
          <v-card-title class="d-flex align-center flex-wrap ga-2">
            <div>
              {{ selectedScenario.name }}

              <div class="text-caption text-medium-emphasis">
                {{ selectedScenario.scenarioNo }}
              </div>
            </div>

            <v-spacer />

            <v-chip
              :color="statusColor(selectedScenario.status)"
            >
              {{ selectedScenario.status }}
            </v-chip>
          </v-card-title>

          <v-card-text>
            <div class="text-body-2 mb-3">
              {{
                selectedScenario.description ||
                'No description'
              }}
            </div>

            <v-row dense class="mb-3">
              <v-col cols="12" md="4">
                <metric
                  label="Baseline As Of"
                  :value="dateTime(selectedScenario.baselineAsOf)"
                />
              </v-col>

              <v-col cols="12" md="4">
                <metric
                  label="Created By"
                  :value="selectedScenario.createdByUsername"
                />
              </v-col>

              <v-col cols="12" md="4">
                <metric
                  label="Last Updated"
                  :value="dateTime(selectedScenario.updatedAt)"
                />
              </v-col>
            </v-row>

            <v-divider class="mb-4" />

            <div class="d-flex align-center mb-3">
              <div class="text-subtitle-1 font-weight-bold">
                Recovery Actions
              </div>

              <v-spacer />

              <v-btn
                v-if="canEdit"
                size="small"
                color="primary"
                prepend-icon="mdi-plus"
                @click="openActionDialog"
              >
                Add Action
              </v-btn>
            </div>

            <v-table density="compact">
              <thead>
                <tr>
                  <th>#</th>
                  <th>Action</th>
                  <th>Target</th>
                  <th>Parameters</th>
                  <th class="text-right">Cost</th>
                  <th>Note</th>
                  <th />
                </tr>
              </thead>

              <tbody>
                <tr
                  v-for="action in actions"
                  :key="action.id"
                >
                  <td>
                    {{ action.sequenceNo }}
                  </td>

                  <td>
                    {{ action.actionType }}
                  </td>

                  <td>
                    <div>
                      {{ action.targetType }}
                    </div>

                    <div class="text-caption target-ref">
                      {{ action.targetRef }}
                    </div>
                  </td>

                  <td>
                    <code class="text-caption">
                      {{ JSON.stringify(action.parameters) }}
                    </code>
                  </td>

                  <td class="text-right">
                    {{ money(action.estimatedCost) }}
                  </td>

                  <td>
                    {{ action.note }}
                  </td>

                  <td class="text-right">
                    <v-btn
                      v-if="canEdit"
                      icon="mdi-delete-outline"
                      size="x-small"
                      variant="text"
                      color="error"
                      @click="deleteAction(action)"
                    />
                  </td>
                </tr>

                <tr v-if="actions.length === 0">
                  <td
                    colspan="7"
                    class="text-center text-medium-emphasis py-4"
                  >
                    No recovery actions defined.
                  </td>
                </tr>
              </tbody>
            </v-table>

            <v-divider class="my-4" />

            <div class="d-flex align-center flex-wrap ga-3">
              <v-text-field
                v-model.number="horizonDays"
                type="number"
                label="Horizon Days"
                min="1"
                max="730"
                density="compact"
                variant="outlined"
                hide-details
                style="max-width: 160px"
              />

              <v-btn
                color="primary"
                prepend-icon="mdi-play"
                :disabled="
                  actions.length === 0 ||
                  selectedScenario.status === 'PUBLISHED' ||
                  selectedScenario.status === 'ARCHIVED'
                "
                :loading="simulating"
                @click="simulate"
              >
                Simulate
              </v-btn>

              <v-chip
                v-if="lastSimulation?.reused"
                color="info"
                size="small"
              >
                Reused canonical result
              </v-chip>
            </div>
          </v-card-text>
        </v-card>

        <v-card
          v-else
          class="pa-6 text-medium-emphasis"
        >
          Select or create a Recovery Scenario.
        </v-card>
      </v-col>
    </v-row>

    <!-- Latest simulation -->
    <v-card
      v-if="lastSimulation"
      class="mt-4"
    >
      <v-card-title>
        Latest Simulation Result
      </v-card-title>

      <v-card-subtitle>
        Run {{ shortHash(lastSimulation.run.id) }}
        /
        Baseline {{ shortHash(lastSimulation.run.baselineHash) }}
      </v-card-subtitle>

      <v-card-text>
        <v-row>
          <v-col cols="6" md="2">
            <metric
              label="Recovery Score"
              :value="number(lastSimulation.summary.recoveryScore, 1)"
            />
          </v-col>

          <v-col cols="6" md="2">
            <metric
              label="P1 Reduction"
              :value="String(lastSimulation.summary.p1Reduction)"
            />
          </v-col>

          <v-col cols="6" md="2">
            <metric
              label="Open Reduction"
              :value="String(lastSimulation.summary.openCaseReduction)"
            />
          </v-col>

          <v-col cols="6" md="2">
            <metric
              label="Days Recovered"
              :value="String(lastSimulation.summary.impactDaysRecovered)"
            />
          </v-col>

          <v-col cols="6" md="2">
            <metric
              label="Recovered Revenue"
              :value="money(lastSimulation.summary.recoveredRevenue)"
            />
          </v-col>

          <v-col cols="6" md="2">
            <metric
              label="Net Value"
              :value="money(lastSimulation.summary.netValue)"
            />
          </v-col>
        </v-row>

        <v-divider class="my-4" />

        <div class="text-subtitle-1 font-weight-bold mb-2">
          Case Projection
        </div>

        <v-table density="compact">
          <thead>
            <tr>
              <th>Case</th>
              <th>Priority</th>
              <th>Resolved</th>
              <th>Impact Days</th>
              <th>Revenue at Risk</th>
              <th>Recovered</th>
            </tr>
          </thead>

          <tbody>
            <tr
              v-for="item in lastSimulation.cases"
              :key="item.caseId"
            >
              <td>
                {{ shortHash(item.caseId) }}
              </td>

              <td>
                {{ item.baselinePriorityBand }}
                →
                {{ item.simulatedPriorityBand }}
              </td>

              <td>
                <v-chip
                  size="x-small"
                  :color="item.simulatedResolved ? 'success' : 'default'"
                >
                  {{ item.simulatedResolved ? 'YES' : 'NO' }}
                </v-chip>
              </td>

              <td>
                {{ item.baselineImpactDays }}
                →
                {{ item.simulatedImpactDays }}
              </td>

              <td>
                {{ money(item.baselineRevenueAtRisk) }}
                →
                {{ money(item.simulatedRevenueAtRisk) }}
              </td>

              <td>
                {{ money(item.revenueRecovered) }}
              </td>
            </tr>
          </tbody>
        </v-table>
      </v-card-text>
    </v-card>

    <!-- Create Scenario Dialog -->
    <v-dialog
      v-model="createDialog"
      max-width="600"
    >
      <v-card>
        <v-card-title>
          New Recovery Scenario
        </v-card-title>

        <v-card-text>
          <v-text-field
            v-model="newScenario.name"
            label="Scenario Name"
            autofocus
          />

          <v-textarea
            v-model="newScenario.description"
            label="Description"
            rows="3"
          />
        </v-card-text>

        <v-card-actions>
          <v-spacer />

          <v-btn
            @click="createDialog = false"
          >
            Cancel
          </v-btn>

          <v-btn
            color="primary"
            :loading="creating"
            :disabled="!newScenario.name.trim()"
            @click="createScenario"
          >
            Create
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Action Dialog -->
    <v-dialog
      v-model="actionDialog"
      max-width="720"
    >
      <v-card>
        <v-card-title>
          Add Recovery Action
        </v-card-title>

        <v-card-text>
          <v-row>
            <v-col cols="12" md="6">
              <v-select
                v-model="newAction.actionType"
                :items="actionTypes"
                label="Action Type"
                @update:model-value="applyActionPreset"
              />
            </v-col>

            <v-col cols="12" md="6">
              <v-select
                v-model="newAction.targetType"
                :items="targetTypes"
                label="Target Type"
              />
            </v-col>

            <v-col cols="12">
              <v-text-field
                v-model="newAction.targetRef"
                label="Target Reference"
                hint="Example: PO:<uuid>, WO:<uuid>, WC:<uuid>"
                persistent-hint
              />
            </v-col>

            <v-col cols="12">
              <v-textarea
                v-model="parametersJson"
                label="Parameters (JSON)"
                rows="4"
                hint='Example: {"recoveryDays":3}'
                persistent-hint
              />
            </v-col>

            <v-col cols="12" md="4">
              <v-text-field
                v-model.number="newAction.estimatedCost"
                type="number"
                min="0"
                label="Estimated Cost"
              />
            </v-col>

            <v-col cols="12" md="8">
              <v-text-field
                v-model="newAction.note"
                label="Note"
              />
            </v-col>
          </v-row>

          <v-alert
            v-if="parameterError"
            type="error"
            variant="tonal"
          >
            {{ parameterError }}
          </v-alert>
        </v-card-text>

        <v-card-actions>
          <v-spacer />

          <v-btn @click="actionDialog = false">
            Cancel
          </v-btn>

          <v-btn
            color="primary"
            :loading="addingAction"
            :disabled="!newAction.targetRef.trim()"
            @click="addAction"
          >
            Add
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar
      v-model="snackbar.show"
      :color="snackbar.color"
      timeout="5000"
    >
      {{ snackbar.message }}
    </v-snackbar>
  </v-container>
</template>

<script setup lang="ts">
import {
  computed,
  defineComponent,
  h,
  onMounted,
  ref
} from 'vue'

import {
  RecoveryPlanningApi,
  type RecoveryActionType,
  type RecoveryScenario,
  type RecoveryScenarioAction,
  type RecoveryScenarioComparison,
  type RecoveryScenarioStatus,
  type RecoverySimulationExecution,
  type RecoveryTargetType
} from '@/api'

const Metric = defineComponent({
  name: 'Metric',
  props: {
    label: {
      type: String,
      required: true
    },
    value: {
      type: String,
      required: true
    }
  },
  setup(props) {
    return () =>
      h(
        'div',
        { class: 'metric-box' },
        [
          h(
            'div',
            {
              class:
                'text-caption text-medium-emphasis'
            },
            props.label
          ),
          h(
            'div',
            {
              class:
                'text-body-1 font-weight-medium'
            },
            props.value
          )
        ]
      )
  }
})

const loading = ref(false)
const creating = ref(false)
const addingAction = ref(false)
const simulating = ref(false)
const publishingId = ref('')

const scenarios = ref<RecoveryScenario[]>([])
const actions = ref<RecoveryScenarioAction[]>([])
const comparisons = ref<RecoveryScenarioComparison[]>([])

const selectedScenario =
  ref<RecoveryScenario | null>(null)

const lastSimulation =
  ref<RecoverySimulationExecution | null>(null)

const statusFilter =
  ref<RecoveryScenarioStatus | null>(null)

const horizonDays = ref(90)
const activeBaselineHash = ref('')

const createDialog = ref(false)
const actionDialog = ref(false)

const newScenario = ref({
  name: '',
  description: ''
})

const newAction = ref<{
  actionType: RecoveryActionType
  targetType: RecoveryTargetType
  targetRef: string
  estimatedCost: number
  note: string
}>({
  actionType: 'EXPEDITE_PO',
  targetType: 'PURCHASE_ORDER',
  targetRef: '',
  estimatedCost: 0,
  note: ''
})

const parametersJson = ref(
  JSON.stringify(
    { expediteDays: 1 },
    null,
    2
  )
)

const parameterError = ref('')

const snackbar = ref({
  show: false,
  message: '',
  color: 'success'
})

const statusItems: RecoveryScenarioStatus[] = [
  'DRAFT',
  'SIMULATED',
  'PUBLISHED',
  'ARCHIVED'
]

const actionTypes: RecoveryActionType[] = [
  'EXPEDITE_PO',
  'ALTERNATE_WORK_CENTER',
  'ADD_OVERTIME_CAPACITY',
  'RESCHEDULE_WO',
  'RELEASE_WO'
]

const targetTypes: RecoveryTargetType[] = [
  'PURCHASE_ORDER',
  'WORK_ORDER',
  'WORK_ORDER_OPERATION',
  'WORK_CENTER'
]

const canEdit = computed(
  () => selectedScenario.value?.status === 'DRAFT'
)

function showSuccess(message: string) {
  snackbar.value = {
    show: true,
    message,
    color: 'success'
  }
}

function showError(error: unknown) {
  const e = error as any

  const message =
    e?.response?.data?.error ??
    e?.response?.data?.message ??
    e?.message ??
    String(error)

  snackbar.value = {
    show: true,
    message,
    color: 'error'
  }
}

function money(value: number | null | undefined) {
  return new Intl.NumberFormat(
    'ja-JP',
    {
      style: 'currency',
      currency: 'JPY',
      maximumFractionDigits: 0
    }
  ).format(Number(value ?? 0))
}

function number(
  value: number | null | undefined,
  digits = 0
) {
  return Number(value ?? 0).toFixed(digits)
}

function dateTime(value?: string | null) {
  if (!value) return '-'

  const d = new Date(value)

  if (Number.isNaN(d.getTime())) {
    return value
  }

  return d.toLocaleString('ja-JP')
}

function shortHash(value?: string | null) {
  if (!value) return '-'

  if (value.length <= 12) {
    return value
  }

  return `${value.slice(0, 8)}…${value.slice(-4)}`
}

function statusColor(status?: string) {
  switch (status) {
    case 'DRAFT':
      return 'default'

    case 'SIMULATED':
      return 'info'

    case 'PUBLISHED':
      return 'success'

    case 'ARCHIVED':
      return 'warning'

    default:
      return 'default'
  }
}

function comparisonStatus(
  item: RecoveryScenarioComparison
): RecoveryScenarioStatus {
  return (
    item.status ??
    item.scenarioStatus ??
    (item.isPublished ? 'PUBLISHED' : 'SIMULATED')
  )
}

async function loadScenarios() {
  loading.value = true

  try {
    scenarios.value =
      await RecoveryPlanningApi.scenarios(
        statusFilter.value ?? ''
      )

    if (selectedScenario.value) {
      const current =
        scenarios.value.find(
          x =>
            x.id ===
            selectedScenario.value?.id
        )

      if (current) {
        selectedScenario.value = current
      }
    }
  } catch (error) {
    showError(error)
  } finally {
    loading.value = false
  }
}

async function loadComparisons(
  baselineHash = activeBaselineHash.value
) {
  try {
    comparisons.value =
      await RecoveryPlanningApi.comparison(
        baselineHash || undefined
      )
  } catch (error) {
    showError(error)
  }
}

async function selectScenario(
  scenario: RecoveryScenario
) {
  selectedScenario.value = scenario
  lastSimulation.value = null

  try {
    actions.value =
      await RecoveryPlanningApi.actions(
        scenario.id
      )
  } catch (error) {
    actions.value = []
    showError(error)
  }
}

async function selectById(id: string) {
  let scenario =
    scenarios.value.find(
      x => x.id === id
    )

  if (!scenario) {
    try {
      scenario =
        await RecoveryPlanningApi.scenario(id)
    } catch (error) {
      showError(error)
      return
    }
  }

  await selectScenario(scenario)
}

async function createScenario() {
  creating.value = true

  try {
    const created =
      await RecoveryPlanningApi.createScenario({
        name: newScenario.value.name.trim(),
        description:
          newScenario.value.description.trim()
      })

    createDialog.value = false

    newScenario.value = {
      name: '',
      description: ''
    }

    await loadScenarios()
    await selectById(created.id)

    showSuccess(
      `Scenario ${created.scenarioNo} created`
    )
  } catch (error) {
    showError(error)
  } finally {
    creating.value = false
  }
}

function openActionDialog() {
  parameterError.value = ''

  newAction.value = {
    actionType: 'EXPEDITE_PO',
    targetType: 'PURCHASE_ORDER',
    targetRef: '',
    estimatedCost: 0,
    note: ''
  }

  parametersJson.value =
    JSON.stringify(
      { expediteDays: 1 },
      null,
      2
    )

  actionDialog.value = true
}

function applyActionPreset(
  actionType: RecoveryActionType
) {
  parameterError.value = ''

  switch (actionType) {
    case 'EXPEDITE_PO':
      newAction.value.targetType =
        'PURCHASE_ORDER'

      parametersJson.value =
        JSON.stringify(
          { expediteDays: 1 },
          null,
          2
        )

      break

    case 'ALTERNATE_WORK_CENTER':
      newAction.value.targetType =
        'WORK_ORDER_OPERATION'

      parametersJson.value =
        JSON.stringify(
          {
            alternateWorkCenterRef: '',
            recoveryDays: 1
          },
          null,
          2
        )

      break

    case 'ADD_OVERTIME_CAPACITY':
      newAction.value.targetType =
        'WORK_CENTER'

      parametersJson.value =
        JSON.stringify(
          { overtimeMinutes: 480 },
          null,
          2
        )

      break

    case 'RESCHEDULE_WO':
      newAction.value.targetType =
        'WORK_ORDER'

      parametersJson.value =
        JSON.stringify(
          { recoveryDays: 1 },
          null,
          2
        )

      break

    case 'RELEASE_WO':
      newAction.value.targetType =
        'WORK_ORDER'

      parametersJson.value =
        JSON.stringify(
          {},
          null,
          2
        )

      break
  }
}

async function addAction() {
  if (!selectedScenario.value) {
    return
  }

  parameterError.value = ''

  let parameters:
    Record<string, unknown>

  try {
    const parsed =
      JSON.parse(parametersJson.value)

    if (
      parsed === null ||
      Array.isArray(parsed) ||
      typeof parsed !== 'object'
    ) {
      throw new Error(
        'Parameters must be a JSON object'
      )
    }

    parameters = parsed
  } catch (error) {
    parameterError.value =
      error instanceof Error
        ? error.message
        : String(error)

    return
  }

  addingAction.value = true

  try {
    await RecoveryPlanningApi.addAction(
      selectedScenario.value.id,
      {
        actionType:
          newAction.value.actionType,

        targetType:
          newAction.value.targetType,

        targetRef:
          newAction.value.targetRef.trim(),

        parameters,

        estimatedCost:
          Number(
            newAction.value.estimatedCost || 0
          ),

        note:
          newAction.value.note.trim()
      }
    )

    actions.value =
      await RecoveryPlanningApi.actions(
        selectedScenario.value.id
      )

    actionDialog.value = false

    showSuccess('Recovery action added')
  } catch (error) {
    showError(error)
  } finally {
    addingAction.value = false
  }
}

async function deleteAction(
  action: RecoveryScenarioAction
) {
  if (!selectedScenario.value) {
    return
  }

  if (
    !window.confirm(
      `Delete ${action.actionType}?`
    )
  ) {
    return
  }

  try {
    await RecoveryPlanningApi.deleteAction(
      selectedScenario.value.id,
      action.id
    )

    actions.value =
      await RecoveryPlanningApi.actions(
        selectedScenario.value.id
      )

    showSuccess('Recovery action deleted')
  } catch (error) {
    showError(error)
  }
}

async function simulate() {
  if (!selectedScenario.value) {
    return
  }

  simulating.value = true

  try {
    const result =
      await RecoveryPlanningApi.simulate(
        selectedScenario.value.id,
        Number(horizonDays.value)
      )

    lastSimulation.value = result

    activeBaselineHash.value =
      result.run.baselineHash

    await loadScenarios()

    const refreshed =
      scenarios.value.find(
        x =>
          x.id ===
          selectedScenario.value?.id
      )

    if (refreshed) {
      selectedScenario.value =
        refreshed
    }

    await loadComparisons(
      result.run.baselineHash
    )

    showSuccess(
      result.reused
        ? 'Canonical simulation result reused'
        : 'Recovery scenario simulated'
    )
  } catch (error) {
    showError(error)
  } finally {
    simulating.value = false
  }
}

async function publishComparison(
  item: RecoveryScenarioComparison
) {
  if (
    !window.confirm(
      `Publish recovery plan "${item.name}"?`
    )
  ) {
    return
  }

  publishingId.value =
    item.scenarioId

  try {
    await RecoveryPlanningApi.publish(
      item.scenarioId,
      item.runId,
      'Approved from Recovery Planning comparison'
    )

    await loadScenarios()
    await loadComparisons(
      item.baselineHash
    )

    if (
      selectedScenario.value?.id ===
      item.scenarioId
    ) {
      const refreshed =
        scenarios.value.find(
          x => x.id === item.scenarioId
        )

      if (refreshed) {
        selectedScenario.value =
          refreshed
      }
    }

    showSuccess(
      `Recovery plan ${item.scenarioNo} published`
    )
  } catch (error) {
    showError(error)
  } finally {
    publishingId.value = ''
  }
}

async function refreshAll() {
  await loadScenarios()
  await loadComparisons()

  if (selectedScenario.value) {
    await selectScenario(
      selectedScenario.value
    )
  }
}

onMounted(
  async () => {
    await loadScenarios()
    await loadComparisons()
  }
)
</script>

<style scoped>
.best-scenario {
  border-width: 2px;
}

.score-box {
  text-align: center;
  padding: 14px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 8px;
}

.metric-box {
  min-height: 54px;
}

.target-ref {
  max-width: 280px;
  overflow-wrap: anywhere;
}

code {
  white-space: normal;
  overflow-wrap: anywhere;
}
</style>
