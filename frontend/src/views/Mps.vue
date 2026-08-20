<template>
  <div>
  <v-card>
    <v-card-title class="d-flex align-center">
      MPS — 基準生産計画
      <v-spacer />
      <v-btn color="primary" prepend-icon="mdi-plus" @click="dialog = true">エントリ追加</v-btn>
    </v-card-title>
    <v-data-table :items="mps" :headers="headers" density="comfortable">
      <template #item.itemCode="{ item }">
        {{ codeMap[item.itemId] || item.itemId }}
      </template>
      <template #item.period="{ item }">{{ fmt(item.period) }}</template>
    </v-data-table>
  </v-card>

  <v-dialog v-model="dialog" max-width="500">
    <v-card title="MPS エントリ">
      <v-card-text>
        <v-select v-model="form.itemId" :items="itemOptions"
                  item-title="label" item-value="id" label="品目 (FG/SA)" />
        <v-text-field v-model="form.period" type="date" label="期間 (週初)" />
        <v-text-field v-model.number="form.planned" type="number" label="計画数" />
        <v-text-field v-model.number="form.released" type="number" label="リリース済" />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="dialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="save">保存</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ItemsApi, MpsApi, type Item, type MpsEntry } from '@/api'

const items = ref<Item[]>([])
const mps = ref<MpsEntry[]>([])
const dialog = ref(false)
const form = ref<MpsEntry>({ itemId: '', period: '', planned: 0, released: 0 })

const codeMap = computed(() => {
  const m: Record<string, string> = {}
  for (const i of (items.value ?? [])) if (i.id) m[i.id] = `${i.code} – ${i.name}`
  return m
})
const itemOptions = computed(() =>
  (items.value ?? []).filter(i => i.type === 'FG' || i.type === 'SA')
    .map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))

const headers = [
  { title: '品目',       key: 'itemCode' },
  { title: '期間',       key: 'period' },
  { title: '計画',       key: 'planned',  align: 'end' as const },
  { title: 'リリース済', key: 'released', align: 'end' as const },
  { title: '需要根拠', key: 'demandBasis' },
  { title: 'Forecast Run', key: 'sourceForecastRunId' },
  { title: 'S&OP Plan', key: 'sourceSopPlanId' },
  { title: 'S&OP Run', key: 'sourceSopDisaggregationRunId' },
  { title: 'Mix Version', key: 'sourceProductMixVersionId' }
]

async function load() {
  const [_p0, _p1] = await Promise.all([ItemsApi.list(), MpsApi.list()])
  items.value = _p0 ?? []
  mps.value = _p1 ?? []
}
onMounted(load)

async function save() {
  await MpsApi.upsert(form.value)
  dialog.value = false
  form.value = { itemId: '', period: '', planned: 0, released: 0 }
  await load()
}
function fmt(d: string) { return d ? new Date(d).toLocaleDateString('ja-JP') : '' }
</script>
