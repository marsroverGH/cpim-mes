<template>
  <div>
    <v-card class="mb-4">
      <v-card-title class="d-flex align-center">
        Supplier Scheduling / Lead-Time Reliability
        <v-spacer />
        <v-btn color="primary" prepend-icon="mdi-chart-timeline-variant" :loading="busy" @click="refreshReliability">
          Reliability再計算
        </v-btn>
      </v-card-title>
      <v-card-text>
        <v-alert type="info" variant="tonal" density="compact" class="mb-4">
          実績Lead Timeは完全入荷済みPOから計算します。MRP/CTPは ASN → Supplier Confirmation → Reliability → PO Due Date の順で供給日を使用します。
        </v-alert>
        <v-row>
          <v-col cols="12" md="3">
            <v-text-field v-model.number="windowDays" type="number" min="30" max="3650" label="分析期間 (days)" />
          </v-col>
          <v-col cols="12" md="3">
            <v-text-field v-model.number="minSamples" type="number" min="1" max="1000" label="最低Sample数" />
          </v-col>
          <v-col cols="12" md="6" class="d-flex align-center ga-2 flex-wrap">
            <v-chip>Profiles {{ reliability.length }}</v-chip>
            <v-chip v-if="latestRun" color="success">Latest {{ fmtDateTime(latestRun.createdAt) }}</v-chip>
          </v-col>
        </v-row>
      </v-card-text>
    </v-card>

    <v-card class="mb-4">
      <v-card-title>Lead-Time Reliability Profiles</v-card-title>
      <v-card-text class="pb-0">
        <v-text-field v-model="search" prepend-inner-icon="mdi-magnify" label="Supplier / Item検索" clearable density="compact" />
      </v-card-text>
      <v-data-table :items="reliability" :headers="headers" :search="search" density="compact">
        <template #item.scope="{ item }">{{ item.itemCode || 'ALL ITEMS' }}</template>
        <template #item.averageLeadDays="{ item }">{{ n1(item.averageLeadDays) }}</template>
        <template #item.p90LeadDays="{ item }">{{ n1(item.p90LeadDays) }}</template>
        <template #item.onTimeRate="{ item }">{{ pct(item.onTimeRate) }}</template>
        <template #item.averageLatenessDays="{ item }">{{ n1(item.averageLatenessDays) }}</template>
        <template #item.confidence="{ item }">
          <v-chip size="small" :color="confidenceColor(item.confidence)">{{ item.confidence }}</v-chip>
        </template>
      </v-data-table>
    </v-card>

    <v-card class="mb-4">
      <v-card-title>Open PO Planning Schedule</v-card-title>
      <v-data-table :items="openPOs" :headers="poHeaders" density="compact">
        <template #item.dueDate="{ item }">{{ fmt(item.dueDate) }}</template>
        <template #item.confirmedDeliveryDate="{ item }">{{ fmt(item.confirmedDeliveryDate) }}</template>
        <template #item.asnExpectedArrivalDate="{ item }">{{ fmt(item.asnExpectedArrivalDate) }}</template>
        <template #item.expectedDeliveryDate="{ item }">{{ fmt(item.expectedDeliveryDate) }}</template>
        <template #item.reliabilityOnTimeRate="{ item }">{{ item.reliabilitySampleCount ? pct(item.reliabilityOnTimeRate || 0) : '—' }}</template>
        <template #item.scheduleSource="{ item }">
          <v-chip size="small" :color="sourceColor(item.scheduleSource)">{{ item.scheduleSource || 'PO_DUE_DATE' }}</v-chip>
        </template>
      </v-data-table>
    </v-card>

    <v-card>
      <v-card-title>Reliability Run History</v-card-title>
      <v-data-table :items="runs" :headers="runHeaders" density="compact">
        <template #item.window="{ item }">{{ fmt(item.windowStart) }} → {{ fmt(item.windowEnd) }}</template>
        <template #item.createdAt="{ item }">{{ fmtDateTime(item.createdAt) }}</template>
        <template #item.resultHash="{ item }"><code>{{ item.resultHash?.slice(0, 12) || '' }}</code></template>
      </v-data-table>
    </v-card>

    <v-snackbar v-model="snack" :color="snackColor">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  PurchaseOrdersApi, SupplierSchedulingApi,
  type PurchaseOrder, type SupplierLeadTimeResult, type SupplierLeadTimeRun
} from '@/api'

const reliability = ref<SupplierLeadTimeResult[]>([])
const runs = ref<SupplierLeadTimeRun[]>([])
const pos = ref<PurchaseOrder[]>([])
const busy = ref(false)
const search = ref('')
const windowDays = ref(365)
const minSamples = ref(3)
const snack = ref(false)
const snackText = ref('')
const snackColor = ref('success')

const headers = [
  { title: 'Supplier', key: 'supplierName' },
  { title: 'Scope', key: 'scope' },
  { title: 'Samples', key: 'sampleCount', align: 'end' as const },
  { title: 'Avg LT', key: 'averageLeadDays', align: 'end' as const },
  { title: 'P90 LT', key: 'p90LeadDays', align: 'end' as const },
  { title: 'Recommended', key: 'recommendedLeadDays', align: 'end' as const },
  { title: 'On-time', key: 'onTimeRate', align: 'end' as const },
  { title: 'Avg Late', key: 'averageLatenessDays', align: 'end' as const },
  { title: 'Confidence', key: 'confidence' }
]
const poHeaders = [
  { title: 'PO', key: 'poNo' },
  { title: 'Supplier', key: 'supplier' },
  { title: 'PO Due', key: 'dueDate' },
  { title: 'Confirmed', key: 'confirmedDeliveryDate' },
  { title: 'ASN ETA', key: 'asnExpectedArrivalDate' },
  { title: 'Planning Date', key: 'expectedDeliveryDate' },
  { title: 'Source', key: 'scheduleSource' },
  { title: 'n', key: 'reliabilitySampleCount', align: 'end' as const },
  { title: 'On-time', key: 'reliabilityOnTimeRate', align: 'end' as const }
]
const runHeaders = [
  { title: 'Window', key: 'window' },
  { title: 'Min samples', key: 'minSamples' },
  { title: 'Status', key: 'status' },
  { title: 'Generated by', key: 'generatedBy' },
  { title: 'Created', key: 'createdAt' },
  { title: 'Hash', key: 'resultHash' }
]

const openPOs = computed(() => pos.value.filter(p => p.status === 'OPEN' || p.status === 'PARTIALLY_RECEIVED'))
const latestRun = computed(() => runs.value[0])

async function load() {
  const [r, rr, p] = await Promise.all([
    SupplierSchedulingApi.reliability(), SupplierSchedulingApi.runs(), PurchaseOrdersApi.list()
  ])
  reliability.value = r || []
  runs.value = rr || []
  pos.value = p || []
}

async function refreshReliability() {
  busy.value = true
  try {
    const out = await SupplierSchedulingApi.refreshReliability(Number(windowDays.value), Number(minSamples.value))
    snackText.value = `Reliability run complete: ${out.results.length} profile(s)`
    snackColor.value = 'success'
    snack.value = true
    await load()
  } catch (e: any) {
    snackText.value = e?.response?.data?.message || e?.response?.data?.error || e?.message || String(e)
    snackColor.value = 'error'
    snack.value = true
  } finally {
    busy.value = false
  }
}

function fmt(d?: string) { return d ? new Date(d).toLocaleDateString('ja-JP') : '—' }
function fmtDateTime(d?: string) { return d ? new Date(d).toLocaleString('ja-JP') : '—' }
function n1(v?: number) { return Number(v ?? 0).toFixed(1) }
function pct(v?: number) { return `${(Number(v ?? 0) * 100).toFixed(1)}%` }
function confidenceColor(c: string) { return c === 'HIGH' ? 'success' : c === 'MEDIUM' ? 'primary' : 'warning' }
function sourceColor(s?: PurchaseOrder['scheduleSource']) {
  if (s === 'ASN') return 'success'
  if (s === 'SUPPLIER_CONFIRMATION') return 'primary'
  if (s === 'RELIABILITY') return 'warning'
  return 'grey'
}

onMounted(load)
</script>
