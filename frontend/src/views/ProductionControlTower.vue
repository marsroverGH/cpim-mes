<template>
  <div>
    <div class="d-flex align-center mb-4">
      <div>
        <h1 class="text-h5">Production Control Tower</h1>
        <div class="text-body-2 text-medium-emphasis">
          Sales Orderへの影響、Root Cause、優先度、推奨介入、担当・解決状況を統合して管理します。
        </div>
      </div>

      <v-spacer />

      <v-btn
        class="mr-2"
        variant="outlined"
        prepend-icon="mdi-refresh"
        :loading="loading"
        @click="load"
      >
        更新
      </v-btn>

      <v-btn
        color="primary"
        prepend-icon="mdi-radar"
        :loading="refreshing"
        :disabled="!canManage"
        @click="refreshTower"
      >
        Control Tower Refresh
      </v-btn>
    </div>

    <v-alert
      v-if="errorMessage"
      type="error"
      variant="tonal"
      class="mb-4"
    >
      {{ errorMessage }}
    </v-alert>

    <v-row class="mb-2">
      <v-col cols="12" sm="6" md="2">
        <v-card>
          <v-card-text>
            <div class="text-caption">Cases</div>
            <div class="text-h5">{{ summary.totalCases }}</div>
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" sm="6" md="2">
        <v-card>
          <v-card-text>
            <div class="text-caption">P1</div>
            <div class="text-h5">{{ summary.p1Cases }}</div>
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" sm="6" md="2">
        <v-card>
          <v-card-text>
            <div class="text-caption">P2</div>
            <div class="text-h5">{{ summary.p2Cases }}</div>
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" sm="6" md="2">
        <v-card>
          <v-card-text>
            <div class="text-caption">Open</div>
            <div class="text-h5">{{ summary.openCases }}</div>
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" sm="6" md="2">
        <v-card>
          <v-card-text>
            <div class="text-caption">Unassigned</div>
            <div class="text-h5">{{ summary.unassignedCases }}</div>
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" sm="6" md="2">
        <v-card>
          <v-card-text>
            <div class="text-caption">Revenue at Risk</div>
            <div class="text-h6">{{ money(summary.revenueAtRisk) }}</div>
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>

    <v-card class="mb-4">
      <v-card-text>
        <v-row align="center">
          <v-col cols="12" md="3">
            <v-select
              v-model="statusFilter"
              :items="statuses"
              clearable
              label="Case Status"
              data-testid="control-tower-status-filter"
            />
          </v-col>

          <v-col cols="12" md="3">
            <v-select
              v-model="priorityFilter"
              :items="priorities"
              clearable
              label="Priority"
            />
          </v-col>

          <v-col cols="12" md="3">
            <v-btn
              variant="outlined"
              prepend-icon="mdi-filter"
              data-testid="control-tower-filter-button"
              @click="load"
            >
              Filter
            </v-btn>
          </v-col>

          <v-col cols="12" md="3" class="text-right text-caption">
            As-of {{ fmtDateTime(dashboard?.asOf) }}
          </v-col>
        </v-row>
      </v-card-text>
    </v-card>

    <v-card>
      <v-card-title class="d-flex align-center">
        Intervention Priority
        <v-spacer />
        <v-chip
          size="small"
          :color="summary.p1Cases ? 'error' : 'success'"
        >
          P1 {{ summary.p1Cases }}
        </v-chip>
      </v-card-title>

      <v-data-table
        :headers="headers"
        :items="cases"
        density="compact"
        :items-per-page="25"
      >
        <template #item.priorityBand="{ item }">
          <v-chip
            size="x-small"
            :color="priorityColor(item.priorityBand)"
          >
            {{ item.priorityBand || '-' }}
          </v-chip>
        </template>

        <template #item.order="{ item }">
          <div>{{ item.salesOrderNo }}</div>
          <div class="text-caption">
            {{ item.customerNo }} {{ item.customerName }}
          </div>
        </template>

        <template #item.item="{ item }">
          <div>{{ item.itemCode || '-' }}</div>
          <div class="text-caption">{{ item.itemName || '' }}</div>
        </template>

        <template #item.risk="{ item }">
          <div>{{ money(item.revenueAtRisk) }}</div>
          <div class="text-caption">
            {{ item.impactDays || 0 }} day(s)
          </div>
        </template>

        <template #item.rootCause="{ item }">
          <div>{{ item.rootCauseType || item.exceptionType }}</div>
          <div class="text-caption root-ref">
            {{ item.rootCauseRef || '-' }}
          </div>
        </template>

        <template #item.currentStatus="{ item }">
          <v-chip
            size="x-small"
            :color="statusColor(item.currentStatus)"
          >
            {{ item.currentStatus }}
          </v-chip>
        </template>

        <template #item.owner="{ item }">
          {{ item.ownerUsername || '未割当' }}
        </template>

        <template #item.actions="{ item }">
          <v-btn
            size="x-small"
            variant="text"
            @click="openCase(item)"
          >
            Detail
          </v-btn>

          <v-btn
            v-if="canManage && item.currentStatus === 'OPEN'"
            size="x-small"
            variant="text"
            @click="openAction(item, 'ACKNOWLEDGE')"
          >
            Ack
          </v-btn>

          <v-btn
            v-if="canManage && actionable(item)"
            size="x-small"
            variant="text"
            @click="openAction(item, 'ASSIGN')"
          >
            Assign
          </v-btn>

          <v-btn
            v-if="canManage && ['ACKNOWLEDGED','ASSIGNED'].includes(item.currentStatus)"
            size="x-small"
            variant="text"
            @click="openAction(item, 'START')"
          >
            Start
          </v-btn>

          <v-btn
            v-if="canManage && actionable(item)"
            size="x-small"
            variant="text"
            color="success"
            @click="openAction(item, 'RESOLVE')"
          >
            Resolve
          </v-btn>

          <v-btn
            v-if="canManage && item.currentStatus === 'RESOLVED'"
            size="x-small"
            variant="text"
            color="warning"
            @click="openAction(item, 'REOPEN')"
          >
            Reopen
          </v-btn>

          <v-btn
            v-if="canManage && item.currentStatus === 'RESOLVED'"
            size="x-small"
            variant="text"
            color="success"
            @click="openAction(item, 'CLOSE')"
          >
            Close
          </v-btn>
        </template>
      </v-data-table>
    </v-card>

    <v-dialog v-model="detailDialog" max-width="1100">
      <v-card>
        <v-card-title class="d-flex align-center">
          Case Detail
          <v-spacer />
          <v-btn
            icon="mdi-close"
            variant="text"
            @click="detailDialog=false"
          />
        </v-card-title>

        <v-card-text v-if="selectedCase">
          <v-row>
            <v-col cols="12" md="6">
              <v-list density="compact">
                <v-list-item
                  title="Sales Order"
                  :subtitle="`${selectedCase.salesOrderNo} / ${selectedCase.customerName}`"
                />
                <v-list-item
                  title="Item"
                  :subtitle="`${selectedCase.itemCode || '-'} ${selectedCase.itemName || ''}`"
                />
                <v-list-item
                  title="Exception"
                  :subtitle="selectedCase.exceptionType"
                />
                <v-list-item
                  title="Root Cause"
                  :subtitle="`${selectedCase.rootCauseType || '-'} / ${selectedCase.rootCauseRef || '-'}`"
                />
                <v-list-item
                  title="Priority"
                  :subtitle="`${selectedCase.priorityBand || '-'} / ${num(selectedCase.priorityScore)} points`"
                />
                <v-list-item
                  title="Revenue at Risk"
                  :subtitle="money(selectedCase.revenueAtRisk)"
                />
              </v-list>
            </v-col>

            <v-col cols="12" md="6">
              <v-table density="compact">
                <tbody>
                  <tr><td>Severity</td><td>{{ num(selectedCase.severityScore) }}</td></tr>
                  <tr><td>Lateness</td><td>{{ num(selectedCase.latenessScore) }}</td></tr>
                  <tr><td>Revenue</td><td>{{ num(selectedCase.revenueScore) }}</td></tr>
                  <tr><td>Customer</td><td>{{ num(selectedCase.customerScore) }}</td></tr>
                  <tr><td>Material</td><td>{{ num(selectedCase.materialScore) }}</td></tr>
                  <tr><td>Capacity</td><td>{{ num(selectedCase.capacityScore) }}</td></tr>
                  <tr><td>Supplier</td><td>{{ num(selectedCase.supplierScore) }}</td></tr>
                  <tr><td>Execution</td><td>{{ num(selectedCase.executionScore) }}</td></tr>
                  <tr><td>Aging</td><td>{{ num(selectedCase.agingScore) }}</td></tr>
                </tbody>
              </v-table>
            </v-col>
          </v-row>

          <v-divider class="my-4" />

          <h3 class="text-subtitle-1 mb-2">Recommended Interventions</h3>

          <v-list lines="three">
            <v-list-item
              v-for="x in recommendations"
              :key="x.id"
            >
              <template #prepend>
                <v-avatar size="28">{{ x.rankNo }}</v-avatar>
              </template>

              <v-list-item-title>
                {{ x.actionType }} — {{ x.title }}
              </v-list-item-title>

              <v-list-item-subtitle>
                {{ x.reason }}
              </v-list-item-subtitle>

              <div class="text-caption mt-1">
                {{ x.targetType }} {{ x.targetRef || '' }}
                <span v-if="x.requiresApproval">
                  / Approval required
                </span>
              </div>
            </v-list-item>

            <v-list-item
              v-if="!recommendations.length"
              title="Recommendationなし"
            />
          </v-list>

          <v-divider class="my-4" />

          <h3 class="text-subtitle-1 mb-2">Case History</h3>

          <v-data-table
            :headers="actionHeaders"
            :items="caseActions"
            density="compact"
            :items-per-page="10"
          >
            <template #item.occurredAt="{ item }">
              {{ fmtDateTime(item.occurredAt) }}
            </template>
          </v-data-table>
        </v-card-text>
      </v-card>
    </v-dialog>

    <v-dialog v-model="actionDialog" max-width="560">
      <v-card :title="`${pendingAction || ''} Control Tower Case`">
        <v-card-text>
          <v-text-field
            v-if="pendingAction === 'ASSIGN'"
            v-model="assignedToUserId"
            label="担当 User UUID"
            hint="現在のUsersテーブルに存在するActive User UUID"
            persistent-hint
          />

          <v-textarea
            v-model="actionComment"
            label="Comment"
            rows="3"
            class="mt-3"
          />
        </v-card-text>

        <v-card-actions>
          <v-spacer />
          <v-btn @click="actionDialog=false">Cancel</v-btn>
          <v-btn
            color="primary"
            :loading="actionBusy"
            @click="submitAction"
          >
            実行
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar
      v-model="snackbar"
      :color="errorMessage ? 'error' : 'success'"
    >
      {{ errorMessage || successMessage || '完了しました' }}
    </v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  ControlTowerApi,
  type ControlTowerCaseAction,
  type ControlTowerCaseActionType,
  type ControlTowerCurrentCase,
  type ControlTowerDashboard,
  type ControlTowerRecommendation
} from '@/api'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

const loading = ref(false)
const refreshing = ref(false)
const actionBusy = ref(false)

const dashboard = ref<ControlTowerDashboard | null>(null)

const statusFilter = ref<string>()
const priorityFilter = ref<string>()

const selectedCase = ref<ControlTowerCurrentCase | null>(null)
const recommendations = ref<ControlTowerRecommendation[]>([])
const caseActions = ref<ControlTowerCaseAction[]>([])

const detailDialog = ref(false)
const actionDialog = ref(false)

const actionCase = ref<ControlTowerCurrentCase | null>(null)
const pendingAction = ref<ControlTowerCaseActionType>()
const assignedToUserId = ref('')
const actionComment = ref('')

const snackbar = ref(false)
const errorMessage = ref('')
const successMessage = ref('')

const statuses = [
  'OPEN',
  'ACKNOWLEDGED',
  'ASSIGNED',
  'IN_PROGRESS',
  'RESOLVED',
  'CLOSED'
]

const priorities = ['P1', 'P2', 'P3', 'P4']

const canManage = computed(
  () => auth.role === 'admin' || auth.role === 'planner'
)

const cases = computed(() => dashboard.value?.cases || [])

const summary = computed(() =>
  dashboard.value?.summary || {
    totalCases: 0,
    openCases: 0,
    p1Cases: 0,
    p2Cases: 0,
    unassignedCases: 0,
    revenueAtRisk: 0
  }
)

const headers = [
  { title: 'Priority', key: 'priorityBand' },
  { title: 'Score', key: 'priorityScore' },
  { title: 'Order / Customer', key: 'order' },
  { title: 'Item', key: 'item' },
  { title: 'Exception', key: 'exceptionType' },
  { title: 'Business Risk', key: 'risk' },
  { title: 'Root Cause', key: 'rootCause' },
  { title: 'Owner', key: 'owner' },
  { title: 'Status', key: 'currentStatus' },
  { title: '', key: 'actions', sortable: false }
]

const actionHeaders = [
  { title: 'Time', key: 'occurredAt' },
  { title: 'Action', key: 'actionType' },
  { title: 'From', key: 'fromStatus' },
  { title: 'To', key: 'toStatus' },
  { title: 'Actor', key: 'actorUsername' },
  { title: 'Assigned', key: 'assignedToUsername' },
  { title: 'Comment', key: 'comment' }
]

function num(v?: number) {
  return Number(v || 0).toLocaleString(
    'ja-JP',
    { maximumFractionDigits: 3 }
  )
}

function money(v?: number) {
  return new Intl.NumberFormat(
    'ja-JP',
    {
      style: 'currency',
      currency: 'JPY',
      maximumFractionDigits: 0
    }
  ).format(Number(v || 0))
}

function fmtDateTime(v?: string) {
  return v
    ? new Date(v).toLocaleString('ja-JP')
    : '-'
}

function priorityColor(v?: string) {
  if (v === 'P1') return 'error'
  if (v === 'P2') return 'warning'
  if (v === 'P3') return 'info'
  return 'grey'
}

function statusColor(v: string) {
  if (v === 'RESOLVED' || v === 'CLOSED') return 'success'
  if (v === 'IN_PROGRESS') return 'primary'
  if (v === 'ASSIGNED') return 'info'
  if (v === 'ACKNOWLEDGED') return 'secondary'
  return 'warning'
}

function actionable(x: ControlTowerCurrentCase) {
  return !['RESOLVED', 'CLOSED'].includes(x.currentStatus)
}

function notifySuccess(message: string) {
  errorMessage.value = ''
  successMessage.value = message
  snackbar.value = true
}

function notifyError(err: unknown) {
  const e = err as any

  errorMessage.value =
    e?.response?.data?.message ||
    e?.response?.data?.error ||
    e?.message ||
    String(err)

  successMessage.value = ''
  snackbar.value = true
}

async function load() {
  loading.value = true
  errorMessage.value = ''

  try {
    dashboard.value = await ControlTowerApi.dashboard({
      status: statusFilter.value,
      priorityBand: priorityFilter.value
    })
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

async function refreshTower() {
  refreshing.value = true

  try {
    const r = await ControlTowerApi.refresh()

    await load()

    notifySuccess(
      `Refresh完了: ${r.exceptionsEvaluated} exceptions / ` +
      `${r.snapshotsCreated} snapshots / ` +
      `${r.recommendationsCreated} recommendations`
    )
  } catch (e) {
    notifyError(e)
  } finally {
    refreshing.value = false
  }
}

async function openCase(item: ControlTowerCurrentCase) {
  try {
    const [c, r, a] = await Promise.all([
      ControlTowerApi.case(item.caseId),
      ControlTowerApi.recommendations(item.caseId),
      ControlTowerApi.actions(item.caseId)
    ])

    selectedCase.value = c
    recommendations.value = r || []
    caseActions.value = a || []

    detailDialog.value = true
  } catch (e) {
    notifyError(e)
  }
}

function openAction(
  item: ControlTowerCurrentCase,
  action: ControlTowerCaseActionType
) {
  actionCase.value = item
  pendingAction.value = action
  assignedToUserId.value = ''
  actionComment.value = ''
  actionDialog.value = true
}

async function submitAction() {
  if (!actionCase.value || !pendingAction.value) return

  if (
    pendingAction.value === 'ASSIGN' &&
    !assignedToUserId.value.trim()
  ) {
    notifyError(new Error('ASSIGNには担当User UUIDが必要です'))
    return
  }

  actionBusy.value = true

  try {
    await ControlTowerApi.act(
      actionCase.value.caseId,
      {
        actionType: pendingAction.value,
        ...(pendingAction.value === 'ASSIGN'
          ? { assignedToUserId: assignedToUserId.value.trim() }
          : {}),
        comment: actionComment.value
      }
    )

    actionDialog.value = false

    const id = actionCase.value.caseId

    await load()

    const current =
      dashboard.value?.cases.find(x => x.caseId === id)

    if (detailDialog.value && current) {
      await openCase(current)
    }

    notifySuccess('Case actionを記録しました')
  } catch (e) {
    notifyError(e)
  } finally {
    actionBusy.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.root-ref {
  max-width: 260px;
  overflow-wrap: anywhere;
}
</style>
