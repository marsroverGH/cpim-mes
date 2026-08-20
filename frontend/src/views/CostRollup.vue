<template>
  <v-card>
    <v-card-title class="d-flex align-center">
      原価積み上げ (Standard Cost Rollup)
      <v-spacer />
      <v-btn color="primary" prepend-icon="mdi-calculator" :loading="busy" @click="run">
        再計算
      </v-btn>
    </v-card-title>
    <v-card-text>
      <p class="text-body-2 text-medium-emphasis">
        BOM とルーティングを再帰的に展開し、各品目 (FG/SA) の標準原価を算出します。<br />
        <code>Material</code> = 子部品の総原価×数量×(1+スクラップ率) /
        <code>Labor</code> = 各工程 (段取+加工) × 作業区の労務費率 /
        <code>Overhead</code> = 各工程 × 作業区の間接費率 /
        <code>Total</code> = Material + Labor + Overhead
      </p>

      <v-card v-if="rows.length" variant="tonal" class="mt-3">
        <v-card-title class="text-subtitle-1">FG/SA 原価構成 (Material vs Labor)</v-card-title>
        <v-card-text style="height: 360px">
          <BarChart :data="chartData" :options="chartOpts" />
        </v-card-text>
      </v-card>

      <v-data-table v-if="rows.length" :items="rows" :headers="headers"
                    density="compact" class="mt-3" :items-per-page="-1">
        <template #item.materialCost="{ item }">¥{{ fmtMoney(item.materialCost) }}</template>
        <template #item.laborCost="{ item }">¥{{ fmtMoney(item.laborCost) }}</template>
        <template #item.overheadCost="{ item }">¥{{ fmtMoney(item.overheadCost) }}</template>
        <template #item.totalCost="{ item }">
          <strong>¥{{ fmtMoney(item.totalCost) }}</strong>
        </template>
        <template #item.itemType="{ item }">
          <v-chip size="x-small" :color="typeColor(item.itemType)">{{ item.itemType }}</v-chip>
        </template>
      </v-data-table>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CostRollupApi, type CostRollupRow } from '@/api'
import BarChart from '@/components/BarChart.vue'

const rows = ref<CostRollupRow[]>([])
const busy = ref(false)

const headers = [
  { title: 'コード',  key: 'itemCode' },
  { title: '品名',    key: 'itemName' },
  { title: 'タイプ',  key: 'itemType' },
  { title: 'Material',key: 'materialCost', align: 'end' as const },
  { title: 'Labor',   key: 'laborCost',    align: 'end' as const },
  { title: 'Overhead',key: 'overheadCost', align: 'end' as const },
  { title: 'Total',   key: 'totalCost',    align: 'end' as const }
]

async function run() {
  busy.value = true
  try { rows.value = await CostRollupApi.run() }
  finally { busy.value = false }
}
onMounted(run)

const chartData = computed(() => {
  // FG と SA だけグラフ化 (葉ノードは material のみで面白くない)
  const filtered = (rows.value ?? []).filter(r => r.itemType === 'FG' || r.itemType === 'SA')
  const labels = filtered.map(r => r.itemCode)
  return {
    labels,
    datasets: [
      { label: 'Material', backgroundColor: '#1565C0',
        data: filtered.map(r => Math.round(r.materialCost)) },
      { label: 'Labor',    backgroundColor: '#EF6C00',
        data: filtered.map(r => Math.round(r.laborCost)) },
      { label: 'Overhead', backgroundColor: '#6A1B9A',
        data: filtered.map(r => Math.round(r.overheadCost)) }
    ]
  }
})
const chartOpts = {
  responsive: true, maintainAspectRatio: false,
  scales: { x: { stacked: true }, y: { stacked: true, beginAtZero: true } },
  plugins: { legend: { position: 'bottom' as const } }
}

function fmtMoney(n: number) {
  return Math.round(n).toLocaleString('ja-JP')
}
function typeColor(t: string) {
  const colors: Record<string, string> = { FG: 'primary', SA: 'info', RM: 'grey', PP: 'grey-lighten-1' }
  return colors[t] || 'grey'
}
</script>
