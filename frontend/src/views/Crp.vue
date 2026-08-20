<template>
  <v-card>
    <v-card-title>CRP — 有限能力スケジューリング</v-card-title>
    <v-card-text>
      <v-row>
        <v-col cols="12" md="3">
          <v-text-field v-model="startDate" type="date" label="開始日" />
        </v-col>
        <v-col cols="12" md="3">
          <v-text-field v-model.number="horizon" type="number" min="1" max="366" label="期間 (日)" />
        </v-col>
        <v-col cols="12" md="6" class="d-flex align-center flex-wrap" style="gap:8px">
          <v-btn color="primary" prepend-icon="mdi-calendar-clock" :loading="busy" @click="schedule">
            有限能力日程を作成
          </v-btn>
          <v-btn variant="outlined" prepend-icon="mdi-chart-timeline-variant" :loading="roughBusy" @click="runRough">
            従来CRP比較
          </v-btn>
        </v-col>
      </v-row>

      <v-alert type="info" variant="tonal" density="compact" class="mb-3">
        RELEASED / IN_PROGRESS のWOをFirm Loadとして先に能力予約し、その残能力へMRP Planned Orderを納期順に配置します。
        休日・Work Centerカレンダー・効率・稼働率を反映し、工程は複数日に分割される場合があります。
      </v-alert>

      <v-row v-if="result">
        <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">Firm WO</div><div class="text-h5">{{ result.summary.firmOrders }}</div></v-card-text></v-card></v-col>
        <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">MRP計画</div><div class="text-h5">{{ result.summary.plannedOrders }}</div></v-card-text></v-card></v-col>
        <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">日程化完了</div><div class="text-h5">{{ result.summary.scheduledOrders }}</div></v-card-text></v-card></v-col>
        <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">納期遅延</div><div class="text-h5 text-warning">{{ result.summary.lateOrders }}</div></v-card-text></v-card></v-col>
        <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">未日程化</div><div class="text-h5 text-error">{{ result.summary.unscheduledOrders }}</div></v-card-text></v-card></v-col>
        <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">負荷(時間)</div><div class="text-h5">{{ (result.summary.totalLoadMinutes / 60).toFixed(1) }}</div></v-card-text></v-card></v-col>
      </v-row>

      <v-card v-if="result?.loads?.length" variant="tonal" class="mt-3">
        <v-card-title class="text-subtitle-1">有限能力配置後の作業区別 負荷率</v-card-title>
        <v-card-text style="height: 300px">
          <BarChart :data="byWcChartData" :options="byWcOpts" />
        </v-card-text>
      </v-card>

      <v-tabs v-if="result" v-model="tab" class="mt-4">
        <v-tab value="orders">オーダ日程</v-tab>
        <v-tab value="segments">工程セグメント</v-tab>
        <v-tab value="loads">日次能力</v-tab>
        <v-tab value="history">履歴</v-tab>
      </v-tabs>

      <v-window v-if="result" v-model="tab">
        <v-window-item value="orders">
          <v-data-table :items="result.orders" :headers="orderHeaders" density="compact" class="mt-2">
            <template #item.sourceType="{ item }">
              <v-chip size="x-small" :color="item.sourceType === 'FIRM_WO' ? 'primary' : 'secondary'">{{ item.sourceType }}</v-chip>
            </template>
            <template #item.scheduledStart="{ item }">{{ dt(item.scheduledStart) }}</template>
            <template #item.scheduledEnd="{ item }">{{ dt(item.scheduledEnd) }}</template>
            <template #item.dueAt="{ item }">{{ dt(item.dueAt) }}</template>
            <template #item.scheduleStatus="{ item }">
              <v-chip size="small" :color="statusColor(item.scheduleStatus)">{{ item.scheduleStatus }}</v-chip>
            </template>
            <template #item.tardyMinutes="{ item }">{{ item.tardyMinutes ? (item.tardyMinutes / 60).toFixed(1) + 'h' : '—' }}</template>
            <template #item.unscheduledMinutes="{ item }">{{ item.unscheduledMinutes ? Math.round(item.unscheduledMinutes) : 0 }}</template>
          </v-data-table>
        </v-window-item>

        <v-window-item value="segments">
          <v-data-table :items="result.segments" :headers="segmentHeaders" density="compact" class="mt-2">
            <template #item.firm="{ item }"><v-chip size="x-small" :color="item.firm ? 'primary' : 'secondary'">{{ item.firm ? 'FIRM' : 'PLANNED' }}</v-chip></template>
            <template #item.startAt="{ item }">{{ dt(item.startAt) }}</template>
            <template #item.endAt="{ item }">{{ dt(item.endAt) }}</template>
            <template #item.loadMinutes="{ item }">{{ Math.round(item.loadMinutes) }}</template>
          </v-data-table>
        </v-window-item>

        <v-window-item value="loads">
          <v-data-table :items="result.loads" :headers="loadHeaders" density="compact" class="mt-2">
            <template #item.date="{ item }">{{ d(item.date) }}</template>
            <template #item.requiredMinutes="{ item }">{{ Math.round(item.requiredMinutes) }}</template>
            <template #item.availableMinutes="{ item }">{{ Math.round(item.availableMinutes) }}</template>
            <template #item.loadPct="{ item }"><v-chip size="small" :color="item.loadPct > 95 ? 'warning' : 'success'">{{ item.loadPct.toFixed(0) }}%</v-chip></template>
          </v-data-table>
        </v-window-item>

        <v-window-item value="history">
          <v-data-table :items="runs" :headers="runHeaders" density="compact" class="mt-2" @click:row="openRun">
            <template #item.startDate="{ item }">{{ d(item.startDate) }}</template>
            <template #item.endDate="{ item }">{{ d(item.endDate) }}</template>
            <template #item.generatedAt="{ item }">{{ dt(item.generatedAt) }}</template>
          </v-data-table>
        </v-window-item>
      </v-window>

      <v-expansion-panels v-if="roughRows.length" class="mt-4">
        <v-expansion-panel>
          <v-expansion-panel-title>従来の無限能力CRP比較（過負荷を許す負荷集計）</v-expansion-panel-title>
          <v-expansion-panel-text>
            <v-data-table :items="roughRows" :headers="loadHeaders" density="compact">
              <template #item.date="{ item }">{{ d(item.date) }}</template>
              <template #item.loadPct="{ item }"><v-chip size="small" :color="item.loadPct > 100 ? 'error' : item.loadPct > 80 ? 'warning' : 'success'">{{ item.loadPct.toFixed(0) }}%</v-chip></template>
            </v-data-table>
          </v-expansion-panel-text>
        </v-expansion-panel>
      </v-expansion-panels>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CrpApi, type CapacityLoadRow, type CrpFiniteScheduleResult, type CrpScheduleRun } from '@/api'
import BarChart from '@/components/BarChart.vue'

const today = new Date().toISOString().slice(0, 10)
const startDate = ref(today)
const horizon = ref(28)
const busy = ref(false)
const roughBusy = ref(false)
const result = ref<CrpFiniteScheduleResult | null>(null)
const roughRows = ref<CapacityLoadRow[]>([])
const runs = ref<CrpScheduleRun[]>([])
const tab = ref('orders')

const orderHeaders = [
  { title: '区分', key: 'sourceType' }, { title: '参照', key: 'sourceRef' }, { title: '品目', key: 'itemCode' },
  { title: '数量', key: 'quantity', align: 'end' as const }, { title: '開始', key: 'scheduledStart' }, { title: '完了', key: 'scheduledEnd' },
  { title: '納期', key: 'dueAt' }, { title: '状態', key: 'scheduleStatus' }, { title: '遅延', key: 'tardyMinutes', align: 'end' as const },
  { title: '未配置(分)', key: 'unscheduledMinutes', align: 'end' as const }
]
const segmentHeaders = [
  { title: '区分', key: 'firm' }, { title: '参照', key: 'sourceRef' }, { title: '品目', key: 'itemCode' },
  { title: '工程', key: 'operationSeq' }, { title: '工程名', key: 'operationDescription' }, { title: '作業区', key: 'workCenterCode' },
  { title: '開始', key: 'startAt' }, { title: '終了', key: 'endAt' }, { title: '負荷(分)', key: 'loadMinutes', align: 'end' as const }
]
const loadHeaders = [
  { title: '日付', key: 'date' }, { title: '作業区', key: 'workCenterCode' }, { title: '名称', key: 'workCenterName' },
  { title: '配置負荷(分)', key: 'requiredMinutes', align: 'end' as const }, { title: '実効能力(分)', key: 'availableMinutes', align: 'end' as const },
  { title: '負荷率', key: 'loadPct', align: 'end' as const }
]
const runHeaders = [
  { title: '開始日', key: 'startDate' }, { title: '終了日', key: 'endDate' }, { title: '期間', key: 'horizonDays' },
  { title: '作成者', key: 'generatedBy' }, { title: '作成日時', key: 'generatedAt' }
]

async function loadRuns() { runs.value = (await CrpApi.scheduleRuns()) ?? [] }
onMounted(loadRuns)

async function schedule() {
  busy.value = true
  try {
    result.value = await CrpApi.schedule({ horizonDays: horizon.value, startDate: startDate.value })
    await loadRuns()
  } finally { busy.value = false }
}
async function runRough() {
  roughBusy.value = true
  try { roughRows.value = (await CrpApi.run({ horizonDays: horizon.value, startDate: startDate.value })) ?? [] }
  finally { roughBusy.value = false }
}
async function openRun(_event: Event, row: any) {
  const id = row?.item?.id ?? row?.id
  if (!id) return
  result.value = await CrpApi.scheduleRun(id)
  startDate.value = result.value.run.startDate.slice(0, 10)
  horizon.value = result.value.run.horizonDays
  tab.value = 'orders'
}

const byWcChartData = computed(() => {
  const acc: Record<string, { req: number; cap: number }> = {}
  for (const r of result.value?.loads ?? []) {
    acc[r.workCenterCode] ??= { req: 0, cap: 0 }
    acc[r.workCenterCode].req += r.requiredMinutes
    acc[r.workCenterCode].cap += r.availableMinutes
  }
  const codes = Object.keys(acc).sort()
  return { labels: codes, datasets: [{ label: '期間負荷率 (%)', data: codes.map(c => acc[c].cap > 0 ? Math.round(acc[c].req / acc[c].cap * 100) : 0) }] }
})
const byWcOpts = { responsive: true, maintainAspectRatio: false, plugins: { legend: { display: false } }, scales: { y: { beginAtZero: true, suggestedMax: 100 } } }
function statusColor(s: string) { return s === 'ON_TIME' ? 'success' : s === 'LATE' ? 'warning' : 'error' }
function dt(v?: string) { return v ? new Date(v).toLocaleString('ja-JP') : '—' }
function d(v?: string) { return v ? new Date(v).toLocaleDateString('ja-JP') : '—' }
</script>
