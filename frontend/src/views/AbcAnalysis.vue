<template>
  <v-card>
    <v-card-title class="d-flex align-center flex-wrap ga-2">
      ABC 分析（年間使用金額ベース）
      <v-spacer />
      <v-text-field
        v-model="asOf"
        type="date"
        label="分析基準日"
        density="compact"
        hide-details
        style="max-width: 180px"
      />
      <v-btn color="primary" prepend-icon="mdi-refresh" :loading="busy" @click="run">再計算</v-btn>
    </v-card-title>
    <v-card-text>
      <p class="text-body-2 text-medium-emphasis">
        CPIM型のABC分析として、直近12か月の <code>ISSUE数量 × 標準原価</code> を年間使用金額として降順ソートし、
        累積構成比でランク付けします。
        <strong>A: 累積 ≤ 70% / B: ≤ 90% / C: それ以外</strong>。
        棚卸差異、NCR返品・廃棄などの <code>ADJUST</code> は通常使用量には含めません。
      </p>
      <v-alert v-if="periodLabel" type="info" variant="tonal" density="compact" class="mt-2">
        使用量期間: {{ periodLabel }} / 使用量基準: ISSUE / 原価基準: Standard Cost
      </v-alert>

      <v-row class="mt-2">
        <v-col v-for="c in summary" :key="c.cls" cols="12" sm="4">
          <v-card variant="tonal" :color="classColor(c.cls)">
            <v-card-text>
              <div class="text-h6">クラス {{ c.cls }}</div>
              <div class="text-body-2 text-medium-emphasis">
                {{ c.count }} 品目 / 年間使用金額 ¥{{ c.value.toLocaleString() }}
              </div>
              <div class="text-caption">
                品目構成比 {{ c.itemPct.toFixed(0) }}% — 使用金額構成比 {{ c.valuePct.toFixed(0) }}%
              </div>
            </v-card-text>
          </v-card>
        </v-col>
      </v-row>

      <v-card v-if="rows.length" variant="tonal" class="mt-3">
        <v-card-title class="text-subtitle-1">
          年間使用金額 Pareto — ホバーで金額構成比・累積%表示
        </v-card-title>
        <v-card-text style="height: 380px">
          <BarChart :data="paretoData" :options="paretoOpts" />
        </v-card-text>
      </v-card>

      <v-text-field
        v-model="search"
        prepend-inner-icon="mdi-magnify"
        label="検索 (コード/品名)"
        clearable
        density="compact"
        class="mt-3"
      />
      <v-data-table
        :items="rows"
        :headers="headers"
        :search="search"
        density="compact"
        :items-per-page="-1"
      >
        <template #item.annualUsageQty="{ item }">{{ fmtQty(item.annualUsageQty) }}</template>
        <template #item.standardCost="{ item }">¥{{ Math.round(item.standardCost).toLocaleString() }}</template>
        <template #item.annualUsageValue="{ item }">¥{{ Math.round(item.annualUsageValue).toLocaleString() }}</template>
        <template #item.usageValuePct="{ item }">{{ item.usageValuePct.toFixed(1) }}%</template>
        <template #item.cumulativePct="{ item }">{{ item.cumulativePct.toFixed(1) }}%</template>
        <template #item.onHand="{ item }">{{ fmtQty(item.onHand) }}</template>
        <template #item.onHandValue="{ item }">¥{{ Math.round(item.onHandValue).toLocaleString() }}</template>
        <template #item.abcClass="{ item }">
          <v-chip size="small" :color="classColor(item.abcClass)">{{ item.abcClass }}</v-chip>
        </template>
      </v-data-table>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ABCApi, type ABCRow } from '@/api'
import BarChart from '@/components/BarChart.vue'

const rows = ref<ABCRow[]>([])
const busy = ref(false)
const search = ref('')
const asOf = ref('') // blank = backend business date (authoritative)

const headers = [
  { title: 'コード',       key: 'itemCode' },
  { title: '品名',         key: 'itemName' },
  { title: '年間使用数量', key: 'annualUsageQty',   align: 'end' as const },
  { title: '標準原価',     key: 'standardCost',     align: 'end' as const },
  { title: '年間使用金額', key: 'annualUsageValue', align: 'end' as const },
  { title: '金額構成比',   key: 'usageValuePct',    align: 'end' as const },
  { title: '累積%',        key: 'cumulativePct',    align: 'end' as const },
  { title: 'クラス',       key: 'abcClass' },
  { title: '現在庫',       key: 'onHand',           align: 'end' as const },
  { title: '現在庫価値',   key: 'onHandValue',      align: 'end' as const }
]

async function run() {
  busy.value = true
  try { rows.value = await ABCApi.run(asOf.value || undefined) }
  finally { busy.value = false }
}
onMounted(run)

function classColor(c: string) {
  const colors: Record<string, string> = { A: 'error', B: 'warning', C: 'success' }
  return colors[c] || 'grey'
}

function fmtQty(v: number) {
  return Number(v ?? 0).toLocaleString(undefined, { maximumFractionDigits: 3 })
}

const periodLabel = computed(() => {
  const r = rows.value?.[0]
  if (!r) return ''
  return `${r.usagePeriodStart.slice(0, 10)} ～ ${r.usagePeriodEnd.slice(0, 10)}`
})

const summary = computed(() => {
  const total = (rows.value ?? []).reduce((s, r) => s + r.annualUsageValue, 0) || 1
  const tot = (rows.value ?? []).length || 1
  return ['A', 'B', 'C'].map(cls => {
    const inCls = (rows.value ?? []).filter(r => r.abcClass === cls)
    const v = inCls.reduce((s, r) => s + r.annualUsageValue, 0)
    return { cls, count: inCls.length, value: Math.round(v),
             itemPct: inCls.length / tot * 100, valuePct: v / total * 100 }
  })
})

const paretoData = computed(() => ({
  labels: (rows.value ?? []).map(r => r.itemCode),
  datasets: [
    {
      label: '年間使用金額',
      data: (rows.value ?? []).map(r => Math.round(r.annualUsageValue)),
      backgroundColor: (rows.value ?? []).map(r =>
        r.abcClass === 'A' ? '#C62828' :
        r.abcClass === 'B' ? '#EF6C00' : '#2E7D32')
    }
  ]
}))
const paretoOpts = {
  responsive: true, maintainAspectRatio: false,
  plugins: {
    legend: { display: false },
    tooltip: {
      callbacks: {
        afterLabel: (ctx: any) => {
          const r = rows.value[ctx.dataIndex]
          return r ? `構成比 ${r.usageValuePct.toFixed(1)}% / クラス ${r.abcClass} / 累積 ${r.cumulativePct.toFixed(1)}%` : ''
        }
      }
    }
  },
  scales: { y: { beginAtZero: true, title: { display: true, text: '年間使用金額 (¥)' } } }
}
</script>
