<template>
  <div>
  <!-- Top: KPI metrics from /api/kpi/dashboard -->
  <v-row v-if="kpi" class="mb-2">
    <v-col cols="6" md="3">
      <v-card variant="tonal" :color="kpi.otifRate >= 90 ? 'success' : kpi.otifRate >= 70 ? 'warning' : 'error'">
        <v-card-text>
          <div class="text-caption">OTIF (納期遵守)</div>
          <div class="text-h4">{{ kpi.otifRate.toFixed(1) }}%</div>
          <div class="text-caption text-medium-emphasis">直近30日完成WO</div>
        </v-card-text>
      </v-card>
    </v-col>
    <v-col cols="6" md="3">
      <v-card variant="tonal" color="info">
        <v-card-text>
          <div class="text-caption">在庫回転 (年率)</div>
          <div class="text-h4">{{ kpi.inventoryTurnover.toFixed(2) }}</div>
          <div class="text-caption text-medium-emphasis">
            在庫額: ¥{{ Math.round(kpi.inventoryValue).toLocaleString() }}
          </div>
        </v-card-text>
      </v-card>
    </v-col>
    <v-col cols="6" md="3">
      <v-card variant="tonal" color="primary">
        <v-card-text>
          <div class="text-caption">スループット (30日)</div>
          <div class="text-h4">{{ Math.round(kpi.throughputUnits).toLocaleString() }}</div>
          <div class="text-caption text-medium-emphasis">
            仕掛 {{ Math.round(kpi.wipUnits) }} / 開放WO {{ kpi.openWoCount }}
          </div>
        </v-card-text>
      </v-card>
    </v-col>
    <v-col cols="6" md="3">
      <v-card variant="tonal"
              :color="kpi.criticalActions > 0 ? 'error' : kpi.warningActions > 0 ? 'warning' : 'success'">
        <v-card-text>
          <div class="text-caption">アクションメッセージ</div>
          <div class="text-h4">
            <span class="text-error">{{ kpi.criticalActions }}</span>
            <span class="text-caption mx-1">/</span>
            <span class="text-warning">{{ kpi.warningActions }}</span>
          </div>
          <div class="text-caption text-medium-emphasis">
            遅延WO {{ kpi.overdueWoCount }} / 遅延PO {{ kpi.overduePoCount }}
          </div>
        </v-card-text>
      </v-card>
    </v-col>
  </v-row>

  <!-- Throughput sparkline + Quality -->
  <v-row v-if="kpi" class="mb-2">
    <v-col cols="12" md="8">
      <v-card title="日次スループット (30日)" density="compact">
        <v-card-text style="height: 180px">
          <LineChart v-if="sparklineData" :data="sparklineData" :options="sparklineOpts" />
        </v-card-text>
      </v-card>
    </v-col>
    <v-col cols="12" md="4">
      <v-card title="品質" density="compact">
        <v-card-text>
          <div class="d-flex justify-space-between align-center">
            <span>合格率</span>
            <strong class="text-h6">{{ kpi.qualityPassRate.toFixed(1) }}%</strong>
          </div>
          <v-progress-linear :model-value="kpi.qualityPassRate"
                             :color="kpi.qualityPassRate >= 95 ? 'success' : 'warning'"
                             height="8" rounded class="my-2" />
          <div class="d-flex justify-space-between text-body-2">
            <span>保留 (HOLD)</span>
            <span class="text-warning">{{ kpi.qualityHoldCount }}</span>
          </div>
          <div class="d-flex justify-space-between text-body-2">
            <span>不適合 (REJECTED)</span>
            <span class="text-error">{{ kpi.qualityRejectCount }}</span>
          </div>
        </v-card-text>
      </v-card>
    </v-col>
  </v-row>

  <!-- KPI cards (legacy) -->
  <v-row>
    <v-col v-for="kpi in kpis" :key="kpi.label" cols="12" sm="6" md="3">
      <v-card variant="elevated">
        <v-card-text class="d-flex align-center">
          <v-icon :icon="kpi.icon" size="36" :color="kpi.color" class="mr-4" />
          <div>
            <div class="text-caption text-medium-emphasis">{{ kpi.label }}</div>
            <div class="text-h5 font-weight-bold">{{ kpi.value }}</div>
          </div>
        </v-card-text>
      </v-card>
    </v-col>
  </v-row>

  <!-- Inventory bar + WO doughnut -->
  <v-row class="mt-2">
    <v-col cols="12" md="7">
      <v-card title="在庫トップ10 (バーチャート)" subtitle="On-hand by item">
        <v-card-text style="height: 320px">
          <BarChart v-if="onHand.length" :data="onHandChartData" :options="barOpts" />
          <div v-else class="text-medium-emphasis">データを読み込み中...</div>
        </v-card-text>
      </v-card>
    </v-col>
    <v-col cols="12" md="5">
      <v-card title="製造指示ステータス内訳" subtitle="Work Orders by status">
        <v-card-text style="height: 320px">
          <DoughnutChart v-if="wos.length" :data="woChartData" :options="doughnutOpts" />
          <div v-else class="text-medium-emphasis">データを読み込み中...</div>
        </v-card-text>
      </v-card>
    </v-col>
  </v-row>

  <!-- MRP planned-order trend line -->
  <v-row class="mt-2">
    <v-col cols="12">
      <v-card title="MRP 計画オーダ推移" subtitle="Planned order release by date (next 28 days)">
        <v-card-text style="height: 320px">
          <LineChart v-if="mrpReady && mrpChartData.labels.length" :data="mrpChartData" :options="lineOpts" />
          <div v-else class="text-medium-emphasis">
            {{ mrpReady ? 'MRP 計画オーダなし — 需要を登録して再計算してください' : 'MRP 計算中...' }}
          </div>
        </v-card-text>
      </v-card>
    </v-col>
  </v-row>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  InventoryApi, ItemsApi, KPIApi, MrpApi, WorkOrdersApi,
  type Item, type KPIDashboard, type MrpResult, type OnHandRow, type WorkOrder
} from '@/api'
import BarChart from '@/components/BarChart.vue'
import LineChart from '@/components/LineChart.vue'
import DoughnutChart from '@/components/DoughnutChart.vue'

const items = ref<Item[]>([])
const onHand = ref<OnHandRow[]>([])
const wos = ref<WorkOrder[]>([])
const mrp = ref<MrpResult[]>([])
const mrpReady = ref(false)
const kpi = ref<KPIDashboard | null>(null)

onMounted(async () => {
  const [a, b, c] = await Promise.all([
    ItemsApi.list(), InventoryApi.onHand(), WorkOrdersApi.list()
  ])
  items.value = a ?? []; onHand.value = b ?? []; wos.value = c ?? []
  // KPI と MRP は遅延ロード (API 呼び出しが重い可能性)
  KPIApi.dashboard().then(r => { kpi.value = r }).catch(() => {})
  try {
    mrp.value = (await MrpApi.run({ horizonDays: 28 })) ?? []
  } catch (_) {
    mrp.value = []
  } finally {
    mrpReady.value = true
  }
})

// ---- Sparkline data ----
const sparklineData = computed(() => {
  if (!kpi.value || !kpi.value.dailyThroughput) return null
  return {
    labels: kpi.value.dailyThroughput.map(p => p.date.slice(5, 10)),
    datasets: [{
      label: '完成数',
      data: kpi.value.dailyThroughput.map(p => p.value),
      borderColor: '#1976D2',
      backgroundColor: 'rgba(25, 118, 210, 0.15)',
      fill: true,
      tension: 0.3
    }]
  }
})
const sparklineOpts = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: false } },
  scales: { y: { beginAtZero: true } }
}

// ---- KPIs ----
const kpis = computed(() => [
  { label: '品目数', value: (items.value ?? []).length, icon: 'mdi-package-variant-closed', color: 'primary' },
  { label: '在庫合計', value: (onHand.value ?? []).reduce((s, x) => s + x.onHand, 0).toLocaleString(),
    icon: 'mdi-warehouse', color: 'info' },
  { label: '進行中WO', value: (wos.value ?? []).filter(w => w.status === 'IN_PROGRESS').length,
    icon: 'mdi-cog', color: 'warning' },
  { label: '計画オーダ件数', value: (mrp.value ?? []).filter(m => m.plannedOrderRelease > 0).length,
    icon: 'mdi-calendar-multiple', color: 'success' }
])

// ---- On-hand bar ----
const onHandChartData = computed(() => {
  const top = [...(onHand.value ?? [])].sort((a, b) => b.onHand - a.onHand).slice(0, 10)
  return {
    labels: top.map(x => x.itemCode),
    datasets: [{
      label: 'On-Hand',
      data: top.map(x => x.onHand),
      backgroundColor: '#1565C0'
    }]
  }
})
const barOpts = {
  responsive: true, maintainAspectRatio: false,
  plugins: { legend: { display: false } }
}

// ---- WO doughnut ----
const woChartData = computed(() => {
  const acc: Record<string, number> = {}
  for (const w of (wos.value ?? [])) acc[w.status] = (acc[w.status] || 0) + 1
  const labels = Object.keys(acc)
  return {
    labels,
    datasets: [{
      data: labels.map(k => acc[k]),
      backgroundColor: ['#9E9E9E', '#2E7D32', '#EF6C00', '#1976D2', '#1565C0']
    }]
  }
})
const doughnutOpts = {
  responsive: true, maintainAspectRatio: false,
  plugins: { legend: { position: 'bottom' as const } }
}

// ---- MRP line: planned orders by date, summed across items ----
const mrpChartData = computed(() => {
  const byDate: Record<string, number> = {}
  for (const m of (mrp.value ?? [])) {
    if (m.plannedOrderRelease <= 0) continue
    const d = new Date(m.plannedOrderReleaseDate || m.period).toISOString().slice(0, 10)
    byDate[d] = (byDate[d] || 0) + m.plannedOrderRelease
  }
  const dates = Object.keys(byDate).sort()
  return {
    labels: dates,
    datasets: [{
      label: '計画オーダ合計',
      data: dates.map(d => byDate[d]),
      borderColor: '#1565C0',
      backgroundColor: 'rgba(21, 101, 192, 0.15)',
      fill: true,
      tension: 0.3
    }]
  }
})
const lineOpts = {
  responsive: true, maintainAspectRatio: false,
  scales: { y: { beginAtZero: true } }
}
</script>
