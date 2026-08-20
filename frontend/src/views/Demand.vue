<template>
  <div>
  <v-card>
    <v-card-title class="d-flex align-center">
      需要管理 (予測 / 受注)
      <v-spacer />
      <v-btn color="primary" prepend-icon="mdi-plus" @click="dialog = true">登録</v-btn>
    </v-card-title>
    <v-card-text class="pb-0">
      <v-text-field v-model="search" prepend-inner-icon="mdi-magnify"
                    label="検索 (品目/種別)" clearable density="compact" />
    </v-card-text>
    <v-data-table :items="demands" :headers="headers" :search="search" density="comfortable">
      <template #item.itemCode="{ item }">
        {{ codeMap[item.itemId] || item.itemId }}
      </template>
      <template #item.dueDate="{ item }">{{ fmt(item.dueDate) }}</template>
    </v-data-table>
  </v-card>

  <v-dialog v-model="dialog" max-width="500">
    <v-card title="需要登録">
      <v-card-text>
        <v-select v-model="form.itemId" :items="itemOptions"
                  item-title="label" item-value="id" label="品目" />
        <v-text-field v-model="form.dueDate" type="date" label="納期" />
        <v-text-field v-model.number="form.quantity" type="number" label="数量" />
        <v-text-field model-value="ORDER" label="種別" readonly />
        <v-alert type="info" variant="tonal" density="compact">ForecastはForecast画面のVersion管理から作成します。</v-alert>
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
import { DemandApi, ItemsApi, type Demand, type Item } from '@/api'

const items = ref<Item[]>([])
const demands = ref<Demand[]>([])
const dialog = ref(false)
const search = ref('')
const form = ref<Demand>({ itemId: '', dueDate: '', quantity: 1, source: 'ORDER' })

const codeMap = computed(() => {
  const m: Record<string, string> = {}
  for (const i of (items.value ?? [])) if (i.id) m[i.id] = `${i.code} – ${i.name}`
  return m
})
const itemOptions = computed(() => (items.value ?? []).map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))

const headers = [
  { title: '品目', key: 'itemCode' },
  { title: '納期', key: 'dueDate' },
  { title: '数量', key: 'quantity', align: 'end' as const },
  { title: '種別', key: 'source' }
]

async function load() {
  const [_p0, _p1] = await Promise.all([ItemsApi.list(), DemandApi.list()])
  items.value = _p0 ?? []
  demands.value = _p1 ?? []
}
onMounted(load)

async function save() {
  await DemandApi.create(form.value)
  dialog.value = false
  form.value = { itemId: '', dueDate: '', quantity: 1, source: 'ORDER' }
  await load()
}

function fmt(d: string) { return d ? new Date(d).toLocaleDateString('ja-JP') : '' }
</script>
