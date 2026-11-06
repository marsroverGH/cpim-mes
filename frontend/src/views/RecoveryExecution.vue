<template>
  <v-container fluid class="pa-4">
    <div class="d-flex align-center flex-wrap ga-3 mb-4">
      <div>
        <h1 class="text-h5 font-weight-bold">Controlled Recovery Execution</h1>
        <div class="text-body-2 text-medium-emphasis">
          Published recovery evidence → Preflight → Independent approval → Operational execution
        </div>
      </div>
      <v-spacer />
      <v-btn variant="outlined" prepend-icon="mdi-refresh" :loading="loading" @click="refreshAll">Refresh</v-btn>
    </div>

    <v-alert type="warning" variant="tonal" class="mb-4">
      Execution changes operational Purchase Orders, Work Orders, Work Center capacity overrides, and scheduling signals.
      Wildcard targets are never executable. Approval and execution require an administrator; the approver must differ from the preparer.
    </v-alert>

    <v-card class="mb-4">
      <v-card-title>Prepare from Published Scenario</v-card-title>
      <v-card-text>
        <v-row>
          <v-col cols="12" md="5">
            <v-select v-model="prepare.scenarioId" :items="publishedScenarios" item-title="name" item-value="id" label="Published Scenario" variant="outlined" />
          </v-col>
          <v-col cols="12" md="4">
            <v-text-field v-model="prepare.idempotencyKey" label="Idempotency Key" variant="outlined" />
          </v-col>
          <v-col cols="12" md="3">
            <v-text-field v-model="prepare.comment" label="Prepare Comment" variant="outlined" />
          </v-col>
        </v-row>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn color="primary" :disabled="!prepare.scenarioId || !prepare.idempotencyKey" :loading="working" @click="prepareExecution">Prepare Execution</v-btn>
      </v-card-actions>
    </v-card>

    <v-card>
      <v-card-title class="d-flex align-center">
        Executions
        <v-spacer />
        <v-select v-model="statusFilter" :items="statusItems" clearable density="compact" hide-details label="Status" style="max-width: 220px" @update:model-value="loadExecutions" />
      </v-card-title>
      <v-divider />
      <v-data-table :headers="headers" :items="executions" :loading="loading" item-value="id">
        <template #item.status="{ item }">
          <v-chip size="small" :color="statusColor(item.status)">{{ item.status }}</v-chip>
        </template>
        <template #item.actions="{ item }">
          <div class="d-flex ga-2 flex-wrap py-1">
            <v-btn size="small" variant="outlined" @click="openDetail(item.id)">Detail</v-btn>
            <v-btn size="small" color="info" variant="tonal" :disabled="!['PREPARED','PREFLIGHT_BLOCKED','PREFLIGHT_PASSED'].includes(item.status)" @click="preflight(item.id)">Preflight</v-btn>
            <v-btn v-if="auth.isAdmin" size="small" color="success" variant="tonal" :disabled="item.status !== 'PREFLIGHT_PASSED'" @click="approve(item.id)">Approve</v-btn>
            <v-btn v-if="auth.isAdmin" size="small" color="error" variant="flat" :disabled="item.status !== 'APPROVED'" @click="execute(item.id)">Execute</v-btn>
          </div>
        </template>
      </v-data-table>
    </v-card>

    <v-dialog v-model="detailDialog" max-width="1200">
      <v-card v-if="detail">
        <v-card-title class="d-flex align-center">
          Execution {{ detail.execution.id }}
          <v-spacer />
          <v-chip :color="statusColor(detail.execution.status)">{{ detail.execution.status }}</v-chip>
        </v-card-title>
        <v-card-text>
          <v-row dense class="mb-3">
            <v-col cols="12" md="4"><b>Prepared by:</b> {{ detail.execution.preparedByUsername }}</v-col>
            <v-col cols="12" md="4"><b>Approved by:</b> {{ detail.execution.approvedByUsername || '-' }}</v-col>
            <v-col cols="12" md="4"><b>Executed by:</b> {{ detail.execution.executedByUsername || '-' }}</v-col>
          </v-row>
          <v-alert v-if="detail.execution.failureReason" type="error" variant="tonal" class="mb-3">{{ detail.execution.failureReason }}</v-alert>
          <v-table density="compact">
            <thead><tr><th>Seq</th><th>Action</th><th>Target</th><th>Preflight</th><th>Execution</th><th>Operational Ref</th><th>Error</th></tr></thead>
            <tbody>
              <tr v-for="a in detail.actions" :key="a.id">
                <td>{{ a.sequenceNo }}</td><td>{{ a.actionType }}</td><td>{{ a.targetType }} / {{ a.targetRef }}</td>
                <td><v-chip size="x-small" :color="statusColor(a.preflightStatus)">{{ a.preflightStatus }}</v-chip></td>
                <td><v-chip size="x-small" :color="statusColor(a.executionStatus)">{{ a.executionStatus }}</v-chip></td>
                <td>{{ a.operationalRef || '-' }}</td><td>{{ a.errorText || '-' }}</td>
              </tr>
            </tbody>
          </v-table>
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn @click="detailDialog=false">Close</v-btn></v-card-actions>
      </v-card>
    </v-dialog>
  </v-container>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { http } from '@/api'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const loading = ref(false)
const working = ref(false)
const executions = ref<any[]>([])
const publishedScenarios = ref<any[]>([])
const statusFilter = ref<string | null>(null)
const detailDialog = ref(false)
const detail = ref<any | null>(null)
const prepare = reactive({ scenarioId: '', idempotencyKey: '', comment: '' })
const statusItems = ['PREPARED','PREFLIGHT_PASSED','PREFLIGHT_BLOCKED','APPROVED','EXECUTING','SUCCEEDED','FAILED','PARTIAL_FAILED','CANCELLED']
const headers = [
  { title: 'Prepared', key: 'preparedAt' }, { title: 'Status', key: 'status' },
  { title: 'Scenario', key: 'scenarioId' }, { title: 'Prepared By', key: 'preparedByUsername' },
  { title: 'Actions', key: 'actionCount' }, { title: 'Operations', key: 'actions', sortable: false }
]

function statusColor(s: string) {
  if (['SUCCEEDED','APPROVED','PREFLIGHT_PASSED','READY'].includes(s)) return 'success'
  if (['FAILED','PARTIAL_FAILED','PREFLIGHT_BLOCKED','BLOCKED'].includes(s)) return 'error'
  if (['EXECUTING','RUNNING'].includes(s)) return 'warning'
  return 'default'
}
async function loadScenarios() { const { data } = await http.get('/recovery-scenarios', { params: { status: 'PUBLISHED' } }); publishedScenarios.value = data }
async function loadExecutions() { const { data } = await http.get('/recovery-executions', { params: statusFilter.value ? { status: statusFilter.value } : {} }); executions.value = data }
async function refreshAll() { loading.value = true; try { await Promise.all([loadScenarios(), loadExecutions()]) } finally { loading.value = false } }
async function prepareExecution() { working.value = true; try { const { data } = await http.post(`/recovery-scenarios/${prepare.scenarioId}/executions`, { idempotencyKey: prepare.idempotencyKey, comment: prepare.comment }); detail.value = data; detailDialog.value = true; await loadExecutions() } finally { working.value = false } }
async function openDetail(id: string) { const { data } = await http.get(`/recovery-executions/${id}`); detail.value = data; detailDialog.value = true }
async function preflight(id: string) { working.value = true; try { await http.post(`/recovery-executions/${id}/preflight`, {}); await loadExecutions(); await openDetail(id) } finally { working.value = false } }
async function approve(id: string) { const comment = window.prompt('Approval comment') || ''; working.value = true; try { await http.post(`/recovery-executions/${id}/approve`, { comment }); await loadExecutions(); await openDetail(id) } finally { working.value = false } }
async function execute(id: string) { if (!window.confirm('Execute approved recovery plan against operational data?')) return; working.value = true; try { await http.post(`/recovery-executions/${id}/execute`, {}); await loadExecutions(); await openDetail(id) } finally { working.value = false } }
onMounted(refreshAll)
</script>
