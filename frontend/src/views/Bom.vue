<template>
  <div>
  <v-card>
    <v-card-title>BOM (部品構成)</v-card-title>
    <v-card-text>
      <v-row>
        <v-col cols="12" md="4">
          <v-select v-model="parentId" :items="parentOptions" item-title="label"
            item-value="id" label="親品目を選択" />
        </v-col>
        <v-col cols="12" md="3">
          <v-text-field v-model.number="qty" type="number" label="製造数 (BOM展開用)" />
        </v-col>
        <v-col cols="12" md="5" class="d-flex align-center">
          <v-btn color="primary" prepend-icon="mdi-arrow-down-bold-circle"
                 :disabled="!parentId" @click="explode">BOM展開</v-btn>
          <v-btn class="ml-2" prepend-icon="mdi-plus" :disabled="!parentId" @click="addDialog = true">
            子部品追加
          </v-btn>
        </v-col>
      </v-row>

      <v-tabs v-model="tab" class="mt-3">
        <v-tab value="components">直接構成 (1階層)</v-tab>
        <v-tab value="explosion">多段展開</v-tab>
      </v-tabs>
      <v-window v-model="tab">
        <v-window-item value="components">
          <v-data-table :items="components" :headers="cHeaders" density="compact">
            <template #item.childCode="{ item }">
              {{ codeMap[item.childId] }}
            </template>
            <template #item.actions="{ item }">
              <v-btn icon="mdi-delete" variant="text" size="small" color="error"
                     @click="removeComponent(item)" />
            </template>
          </v-data-table>
        </v-window-item>
        <v-window-item value="explosion">
          <v-data-table :items="exploded" :headers="eHeaders" density="compact" />
        </v-window-item>
      </v-window>
    </v-card-text>
  </v-card>

  <v-dialog v-model="addDialog" max-width="500">
    <v-card title="子部品追加">
      <v-card-text>
        <v-select v-model="newComp.childId" :items="childOptions"
          item-title="label" item-value="id" label="子品目" />
        <v-text-field v-model.number="newComp.quantity" type="number" label="数量" />
        <v-text-field v-model.number="newComp.scrapPct" type="number" step="0.01"
                      label="スクラップ率 (0–1)" />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="addDialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="addComponent">追加</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { BomApi, ItemsApi, type Item, type BOMComponent, type ExplodedRow } from '@/api'

const items = ref<Item[]>([])
const components = ref<BOMComponent[]>([])
const exploded = ref<ExplodedRow[]>([])
const parentId = ref<string>('')
const qty = ref<number>(1)
const tab = ref<'components' | 'explosion'>('components')
const addDialog = ref(false)
const newComp = ref({ childId: '', quantity: 1, scrapPct: 0 })

const codeMap = computed(() => {
  const m: Record<string, string> = {}
  for (const i of (items.value ?? [])) if (i.id) m[i.id] = `${i.code}  ${i.name}`
  return m
})
const parentOptions = computed(() =>
  (items.value ?? []).filter(i => i.type === 'FG' || i.type === 'SA')
    .map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))
const childOptions = computed(() =>
  (items.value ?? []).filter(i => i.id !== parentId.value)
    .map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))

const cHeaders = [
  { title: '子品目',     key: 'childCode' },
  { title: '数量',       key: 'quantity', align: 'end' as const },
  { title: 'スクラップ', key: 'scrapPct', align: 'end' as const },
  { title: '',           key: 'actions',  sortable: false, align: 'end' as const }
]
const eHeaders = [
  { title: '階層', key: 'level' },
  { title: 'コード', key: 'childCode' },
  { title: '品名',   key: 'childName' },
  { title: '所要量', key: 'totalQuantity', align: 'end' as const }
]

onMounted(async () => { items.value = await ItemsApi.list() })

watch(parentId, async () => {
  if (!parentId.value) { components.value = []; exploded.value = []; return }
  components.value = (await BomApi.components(parentId.value)) ?? []
  exploded.value = []
})

async function explode() {
  if (!parentId.value) return
  exploded.value = (await BomApi.explode(parentId.value, qty.value)) ?? []
  tab.value = 'explosion'
}
async function addComponent() {
  if (!parentId.value || !newComp.value.childId) return
  await BomApi.add(parentId.value, newComp.value)
  components.value = (await BomApi.components(parentId.value)) ?? []
  addDialog.value = false
  newComp.value = { childId: '', quantity: 1, scrapPct: 0 }
}
async function removeComponent(c: BOMComponent) {
  if (!c.id) return
  await BomApi.remove(c.id)
  components.value = (await BomApi.components(parentId.value)) ?? []
}
</script>
