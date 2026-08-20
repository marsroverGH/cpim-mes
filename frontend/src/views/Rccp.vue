<template>
  <v-card>
    <v-card-title class="d-flex align-center">
      RCCP — Rough-Cut Capacity Planning
      <v-spacer />
      <v-text-field v-model.number="workingDays" type="number"
                    style="max-width: 140px" density="compact" hide-details
                    label="稼働日/月" />
      <v-btn class="ml-2" color="primary" prepend-icon="mdi-calculator-variant"
             :loading="busy" @click="run">RCCP実行</v-btn>
    </v-card-title>
    <v-card-text>
      <p class="text-body-2 text-medium-emphasis">
        MPS と RCCP プロファイル (品目×作業区の単位負荷分数) を掛け合わせて、
        月次の作業区負荷を粗算します。MRP/CRP より高速で、ボトルネック検出に有効。
      </p>
      <v-data-table :items="rows" :headers="headers" density="comfortable">
        <template #item.month="{ item }">{{ fmtMonth(item.month) }}</template>
        <template #item.requiredMinutes="{ item }">{{ Math.round(item.requiredMinutes) }}</template>
        <template #item.availableMinutes="{ item }">{{ Math.round(item.availableMinutes) }}</template>
        <template #item.loadPct="{ item }">
          <v-chip :color="loadColor(item.loadPct)" size="small">
            {{ item.loadPct.toFixed(0) }}%
          </v-chip>
        </template>
      </v-data-table>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { RCCPApi, type RCCPLoadRow } from '@/api'

const rows = ref<RCCPLoadRow[]>([])
const workingDays = ref(22)
const busy = ref(false)

const headers = [
  { title: '対象月',       key: 'month' },
  { title: '作業区',       key: 'workCenterCode' },
  { title: '名称',         key: 'workCenterName' },
  { title: '所要(分)',     key: 'requiredMinutes',  align: 'end' as const },
  { title: '能力(分)',     key: 'availableMinutes', align: 'end' as const },
  { title: '負荷率',       key: 'loadPct',          align: 'end' as const }
]

async function run() {
  busy.value = true
  try {
    rows.value = (await RCCPApi.run(workingDays.value)) ?? []
  } finally { busy.value = false }
}

function loadColor(pct: number) {
  if (pct > 100) return 'error'
  if (pct > 80)  return 'warning'
  return 'success'
}
function fmtMonth(d: string) {
  return new Date(d).toLocaleDateString('ja-JP', { year: 'numeric', month: '2-digit' })
}
</script>
