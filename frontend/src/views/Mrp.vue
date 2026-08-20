<template>
  <v-card>
    <v-card-title>MRP 実行 (Material Requirements Planning)</v-card-title>
    <v-card-text>
      <v-row>
        <v-col cols="12" md="3"><v-text-field v-model="startDate" type="date" label="開始日" /></v-col>
        <v-col cols="12" md="3"><v-text-field v-model.number="horizon" type="number" label="期間 (日)" /></v-col>
        <v-col cols="12" md="6" class="d-flex align-center">
          <v-btn color="primary" prepend-icon="mdi-cogs" :loading="busy" @click="run">MRP実行</v-btn>
          <span v-if="results.length" class="ml-4 text-medium-emphasis">
            {{ plannedCount }} 件の計画指示が生成されました
          </span>
        </v-col>
      </v-row>

      <v-data-table v-if="results.length" :items="results" :headers="headers" density="compact" class="mt-3">
        <template #item.period="{ item }">{{ fmt(item.period) }}</template>
        <template #item.plannedOrderReleaseDate="{ item }">
          {{ item.plannedOrderReleaseDate ? fmt(item.plannedOrderReleaseDate) : '—' }}
        </template>
        <template #item.lotMethod="{ item }">
          <v-chip size="x-small" :color="methodColor(item.lotMethod)">{{ item.lotMethod }}</v-chip>
        </template>
        <template #item.plannedOrderRelease="{ item }">
          <v-chip v-if="item.plannedOrderRelease > 0" color="primary" size="small">
            {{ item.plannedOrderRelease }}
          </v-chip>
          <span v-else>—</span>
        </template>
        <template #item.pegging="{ item }">
          <span v-if="item.pegging && item.pegging.length"
                class="text-caption text-medium-emphasis">
            {{ item.pegging.join(', ') }}
          </span>
          <span v-else>—</span>
        </template>
      </v-data-table>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { MrpApi, type MrpResult } from '@/api'

const today = new Date().toISOString().slice(0, 10)
const startDate = ref(today)
const horizon = ref(28)
const busy = ref(false)
const results = ref<MrpResult[]>([])
const plannedCount = computed(() => results.value.filter(r => r.plannedOrderRelease > 0).length)

const headers = [
  { title: '期間',       key: 'period' },
  { title: '品目',       key: 'itemCode' },
  { title: '総所要',       key: 'grossRequirement',       align: 'end' as const },
  { title: '予定入荷',     key: 'scheduledReceipts',      align: 'end' as const },
  { title: '予測在庫',     key: 'projectedOnHand',        align: 'end' as const },
  { title: '正味所要',     key: 'netRequirement',         align: 'end' as const },
  { title: '方式',         key: 'lotMethod' },
  { title: '計画入荷',     key: 'plannedOrderReceipt',    align: 'end' as const },
  { title: '計画発行日',   key: 'plannedOrderReleaseDate' },
  { title: '計画発行数量', key: 'plannedOrderRelease',    align: 'end' as const },
  { title: '起源 (Pegging)', key: 'pegging',         sortable: false }
]

async function run() {
  busy.value = true
  try {
    results.value = (await MrpApi.run({ horizonDays: horizon.value, startDate: startDate.value })) ?? []
  } finally { busy.value = false }
}

function fmt(d: string) { return d ? new Date(d).toLocaleDateString('ja-JP') : '' }

function methodColor(m: string) {
  const colors: Record<string, string> = { LFL: 'grey', FOQ: 'info', POQ: 'warning', EOQ: 'success' }
  return colors[m] || 'grey'
}
</script>
