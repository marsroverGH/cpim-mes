<template>
  <div>
    <v-card>
      <v-card-title>需要予測 — Version / Consumption</v-card-title>
      <v-card-text>
        <v-row>
          <v-col cols="12" md="4">
            <v-select v-model="form.itemId" :items="itemOptions" item-title="label" item-value="id" label="品目" />
          </v-col>
          <v-col cols="12" md="2">
            <v-select v-model="form.method" :items="['SMA','EXPO','HW']" label="方式" />
          </v-col>
          <v-col cols="6" md="2">
            <v-text-field v-model.number="form.bucketDays" type="number" min="1" label="Bucket日数" />
          </v-col>
          <v-col cols="6" md="2">
            <v-text-field v-model.number="form.horizonPeriods" type="number" min="1" label="予測期間数" />
          </v-col>
          <v-col cols="12" md="2">
            <v-text-field v-model="form.scenario" label="Scenario" placeholder="BASE" />
          </v-col>
          <v-col cols="12" md="3">
            <v-text-field v-model="form.asOfDate" type="date" label="Forecast As-of Date" />
          </v-col>
        </v-row>

        <v-row>
          <v-col cols="6" md="2"><v-text-field v-model.number="form.window" type="number" min="1" label="SMA Window" /></v-col>
          <v-col cols="6" md="2"><v-text-field v-model.number="form.alpha" type="number" step="0.05" label="Alpha" /></v-col>
          <v-col cols="6" md="2"><v-text-field v-model.number="form.beta" type="number" step="0.05" label="Beta" /></v-col>
          <v-col cols="6" md="2"><v-text-field v-model.number="form.gamma" type="number" step="0.05" label="Gamma" /></v-col>
          <v-col cols="6" md="2"><v-text-field v-model.number="form.seasonLength" type="number" min="2" label="Season" /></v-col>
          <v-col cols="12" md="2" class="d-flex align-center">
            <v-checkbox v-model="form.saveAsVersion" hide-details label="Version保存" />
          </v-col>
        </v-row>

        <div class="d-flex ga-2 mb-3">
          <v-btn color="primary" :loading="busy" :disabled="!form.itemId" @click="run">予測実行</v-btn>
          <v-btn variant="tonal" :disabled="!form.itemId" @click="loadRuns">Version再読込</v-btn>
        </div>

        <v-alert v-if="result?.runId" type="success" variant="tonal" class="mb-3">
          Forecast Version v{{ result.version }} / {{ result.scenario }} をDRAFTとして保存しました。
        </v-alert>

        <div v-if="result">
          <v-row>
            <v-col cols="12" md="4"><v-card variant="tonal"><v-card-text><div class="text-caption">方式</div><div class="text-h6">{{ result.method }}</div></v-card-text></v-card></v-col>
            <v-col cols="12" md="4"><v-card variant="tonal"><v-card-text><div class="text-caption">MAE</div><div class="text-h6">{{ result.mae.toFixed(1) }}</div></v-card-text></v-card></v-col>
            <v-col cols="12" md="4"><v-card variant="tonal"><v-card-text><div class="text-caption">MAPE</div><div class="text-h6">{{ result.mape.toFixed(1) }}%</div></v-card-text></v-card></v-col>
          </v-row>
          <v-card variant="tonal" class="mt-3">
            <v-card-title class="text-subtitle-1">実績 vs 予測 — {{ result.itemCode }}</v-card-title>
            <v-card-text style="height: 340px"><LineChart :data="chartData" :options="chartOpts" /></v-card-text>
          </v-card>
        </div>
      </v-card-text>
    </v-card>

    <v-card class="mt-4">
      <v-card-title>Forecast Versions</v-card-title>
      <v-data-table :items="runs" :headers="runHeaders" density="compact" :items-per-page="10">
        <template #item.version="{ item }">v{{ item.version }}</template>
        <template #item.status="{ item }">
          <v-chip size="small" :color="item.status === 'ACTIVE' ? 'success' : item.status === 'DRAFT' ? 'warning' : undefined">{{ item.status }}</v-chip>
        </template>
        <template #item.generatedAt="{ item }">{{ fmt(item.generatedAt) }}</template>
        <template #item.actions="{ item }">
          <div class="d-flex ga-1">
            <v-btn size="x-small" variant="tonal" @click="showConsumption(item)">Consumption</v-btn>
            <v-btn v-if="item.status === 'DRAFT'" size="x-small" color="success" @click="activate(item)">ACTIVE化</v-btn>
            <v-btn v-if="item.status === 'ACTIVE'" size="x-small" color="primary" @click="applyMps(item)">MPS反映</v-btn>
          </div>
        </template>
      </v-data-table>
    </v-card>

    <v-card v-if="consumption" class="mt-4">
      <v-card-title>
        Forecast Consumption — {{ consumption.itemCode }} / {{ consumption.scenario }} v{{ consumption.version }}
      </v-card-title>
      <v-card-text>
        <v-alert type="info" variant="tonal" class="mb-3">
          Total Demand = Customer Orders + Remaining Forecast。受注がForecastを消費するため二重需要になりません。
        </v-alert>
        <v-data-table :items="consumption.buckets" :headers="consumptionHeaders" density="compact" :items-per-page="-1">
          <template #item.period="{ item }">{{ fmtDate(item.period) }}</template>
          <template #item.forecastQty="{ item }">{{ n(item.forecastQty) }}</template>
          <template #item.orderQty="{ item }">{{ n(item.orderQty) }}</template>
          <template #item.consumedForecast="{ item }">{{ n(item.consumedForecast) }}</template>
          <template #item.remainingForecast="{ item }">{{ n(item.remainingForecast) }}</template>
          <template #item.orderAboveForecast="{ item }">{{ n(item.orderAboveForecast) }}</template>
          <template #item.totalDemand="{ item }"><strong>{{ n(item.totalDemand) }}</strong></template>
        </v-data-table>
      </v-card-text>
    </v-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  ForecastApi, ItemsApi,
  type ForecastRequest, type ForecastResult, type ForecastRun,
  type ForecastConsumptionResult, type Item
} from '@/api'
import LineChart from '@/components/LineChart.vue'

const items = ref<Item[]>([])
const result = ref<ForecastResult | null>(null)
const runs = ref<ForecastRun[]>([])
const consumption = ref<ForecastConsumptionResult | null>(null)
const busy = ref(false)

const form = ref<ForecastRequest>({
  itemId: '', method: 'SMA', window: 4, alpha: 0.3, beta: 0.1, gamma: 0.3,
  seasonLength: 4, bucketDays: 7, horizonPeriods: 4, scenario: 'BASE', asOfDate: new Date().toISOString().slice(0, 10), saveAsVersion: true
})

const itemOptions = computed(() => (items.value ?? []).map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))
const runHeaders = [
  { title: 'Version', key: 'version' }, { title: 'Scenario', key: 'scenario' },
  { title: '方式', key: 'method' }, { title: 'As-of', key: 'asOfDate' }, { title: 'Bucket', key: 'bucketDays' },
  { title: 'Status', key: 'status' }, { title: '生成者', key: 'generatedBy' },
  { title: '生成日時', key: 'generatedAt' }, { title: '', key: 'actions', sortable: false }
]
const consumptionHeaders = [
  { title: '期間', key: 'period' },
  { title: 'Forecast', key: 'forecastQty', align: 'end' as const },
  { title: 'Orders', key: 'orderQty', align: 'end' as const },
  { title: 'Consumed', key: 'consumedForecast', align: 'end' as const },
  { title: 'Remaining Forecast', key: 'remainingForecast', align: 'end' as const },
  { title: 'Orders > Forecast', key: 'orderAboveForecast', align: 'end' as const },
  { title: 'Total Demand', key: 'totalDemand', align: 'end' as const }
]

onMounted(async () => { items.value = await ItemsApi.list() })
watch(() => form.value.itemId, async () => { consumption.value = null; await loadRuns() })

async function run() {
  busy.value = true
  try {
    result.value = await ForecastApi.run(form.value)
    await loadRuns()
    if (result.value.runId) consumption.value = await ForecastApi.consumption(result.value.runId)
  } catch (e: any) {
    alert(e?.response?.data?.message || e?.response?.data?.error || '予測に失敗しました')
  } finally { busy.value = false }
}
async function loadRuns() {
  if (!form.value.itemId) { runs.value = []; return }
  runs.value = await ForecastApi.runs(form.value.itemId)
}
async function activate(r: ForecastRun) {
  try { await ForecastApi.activate(r.id); await loadRuns(); await showConsumption(r) }
  catch (e: any) { alert(e?.response?.data?.message || 'ACTIVE化に失敗しました') }
}
async function showConsumption(r: ForecastRun) { consumption.value = await ForecastApi.consumption(r.id) }
async function applyMps(r: ForecastRun) {
  if (!confirm(`${r.scenario} v${r.version} のConsumed DemandでMPS計画数を更新します。よろしいですか？`)) return
  try {
    const x = await ForecastApi.applyToMps(r.id)
    alert(`${x.updatedMpsEntries} 件のMPSを更新しました`)
  } catch (e: any) { alert(e?.response?.data?.message || 'MPS反映に失敗しました') }
}

const chartData = computed(() => {
  if (!result.value) return { labels: [], datasets: [] }
  return {
    labels: result.value.points.map(p => fmtDate(p.period)),
    datasets: [
      { label: '実績', data: result.value.points.map(p => p.actual ?? null), spanGaps: false },
      { label: '予測', data: result.value.points.map(p => p.forecast ?? null), borderDash: [6, 4], fill: true, spanGaps: true }
    ]
  }
})
const chartOpts = { responsive: true, maintainAspectRatio: false, scales: { y: { beginAtZero: true } }, plugins: { legend: { position: 'bottom' as const } } }
function fmt(v: string) { return v ? new Date(v).toLocaleString('ja-JP') : '' }
function fmtDate(v: string) { return v ? new Date(v).toLocaleDateString('ja-JP') : '' }
function n(v: number) { return Math.round(v * 100) / 100 }
</script>
