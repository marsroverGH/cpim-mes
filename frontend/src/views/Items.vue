<template>
  <div>
  <v-card>
    <v-card-title class="d-flex align-center flex-wrap" style="gap: 8px">
      品目マスタ
      <v-spacer />
      <v-btn variant="text" prepend-icon="mdi-refresh" @click="recomputeLLC">LLC再計算</v-btn>
      <v-btn variant="text" prepend-icon="mdi-download" @click="exportCsv">CSVエクスポート</v-btn>
      <v-btn variant="text" prepend-icon="mdi-upload" @click="fileInput?.click()">CSVインポート</v-btn>
      <input ref="fileInput" type="file" accept=".csv,text/csv" hidden @change="onFileChosen" />
      <v-btn color="primary" prepend-icon="mdi-plus" :disabled="!auth.canEdit" @click="openNew">
        新規
      </v-btn>
    </v-card-title>

    <v-card-text class="pb-0">
      <v-text-field
        v-model="search"
        prepend-inner-icon="mdi-magnify"
        label="検索 (コード/品名/タイプ等)"
        clearable
        density="compact"
      />
    </v-card-text>

    <v-data-table :items="items" :headers="headers" :search="search" density="comfortable">
      <template #item.groupId="{ item }">{{ groupLabel(item.groupId) }}</template>
      <template #item.actions="{ item }">
        <v-btn icon="mdi-pencil" size="small" variant="text"
               :disabled="!auth.canEdit" @click="openEdit(item)" />
        <v-btn icon="mdi-delete" size="small" variant="text" color="error"
               :disabled="!auth.canEdit" @click="remove(item)" />
      </template>
    </v-data-table>
  </v-card>

  <!-- Edit dialog -->
  <v-dialog v-model="dialog" max-width="600">
    <v-card>
      <v-card-title>{{ form.id ? '品目編集' : '新規品目' }}</v-card-title>
      <v-card-text>
        <v-row dense>
          <v-col cols="6"><v-text-field v-model="form.code" label="コード" /></v-col>
          <v-col cols="6"><v-text-field v-model="form.name" label="品名" /></v-col>
          <v-col cols="6">
            <v-select v-model="form.type" :items="['FG','SA','RM','PP']" label="品目タイプ" />
          </v-col>
          <v-col cols="6"><v-text-field v-model="form.uom" label="単位" /></v-col>
          <v-col cols="12"><v-select v-model="form.groupId" :items="groupOptions" item-title="label" item-value="id" clearable label="S&OP Family" /></v-col>
          <v-col cols="4"><v-text-field v-model.number="form.leadTimeDays" type="number" label="LT (日)" /></v-col>
          <v-col cols="4"><v-text-field v-model.number="form.lotSize" type="number" label="ロットサイズ" /></v-col>
          <v-col cols="4"><v-text-field v-model.number="form.safetyStock" type="number" label="安全在庫" /></v-col>
          <v-col cols="6"><v-text-field v-model.number="form.standardCost" type="number" label="標準原価" /></v-col>

          <v-col cols="12"><div class="text-caption text-medium-emphasis mt-2">MRP ロットサイジング</div></v-col>
          <v-col cols="6">
            <v-select v-model="form.lotSizeMethod"
                      :items="[
                        {title:'LFL — Lot-for-Lot', value:'LFL'},
                        {title:'FOQ — 固定ロット',  value:'FOQ'},
                        {title:'POQ — 期間まとめ発注', value:'POQ'},
                        {title:'EOQ — 経済発注量',  value:'EOQ'}
                      ]"
                      label="ロットサイジング方式" />
          </v-col>
          <v-col v-if="form.lotSizeMethod === 'POQ'" cols="6">
            <v-text-field v-model.number="form.poqPeriods" type="number"
                          label="POQ期間数" hint="N期間分を1回でまとめて発注" persistent-hint />
          </v-col>
          <v-col v-if="form.lotSizeMethod === 'EOQ'" cols="6">
            <v-text-field v-model.number="form.orderingCost" type="number"
                          label="1回あたり発注コスト (¥)" />
          </v-col>
          <v-col v-if="form.lotSizeMethod === 'EOQ'" cols="6">
            <v-text-field v-model.number="form.holdingCostPct" type="number" step="0.01"
                          label="年間在庫保管費率"
                          hint="標準原価に対する%。例: 0.20 = 20%/年" persistent-hint />
          </v-col>
        </v-row>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="dialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="save">保存</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Import result dialog -->
  <v-dialog v-model="importDialog" max-width="600">
    <v-card>
      <v-card-title>CSVインポート結果</v-card-title>
      <v-card-text v-if="importResult">
        <v-row dense>
          <v-col cols="4"><v-card variant="tonal" color="success">
            <v-card-text>追加: <strong>{{ importResult.inserted }}</strong></v-card-text></v-card></v-col>
          <v-col cols="4"><v-card variant="tonal" color="info">
            <v-card-text>更新: <strong>{{ importResult.updated }}</strong></v-card-text></v-card></v-col>
          <v-col cols="4"><v-card variant="tonal" color="error">
            <v-card-text>スキップ: <strong>{{ importResult.skipped }}</strong></v-card-text></v-card></v-col>
        </v-row>
        <v-alert v-if="importResult.errors.length" type="warning" variant="tonal" class="mt-3">
          <div class="text-subtitle-2">エラー一覧</div>
          <ul style="font-size: 0.85em">
            <li v-for="(e, i) in importResult.errors" :key="i">{{ e }}</li>
          </ul>
        </v-alert>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn color="primary" @click="importDialog = false">閉じる</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CsvApi, ItemsApi, SOPApi, type CsvImportResult, type Item, type ItemGroup } from '@/api'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const items = ref<Item[]>([])
const groups = ref<ItemGroup[]>([])
const groupOptions = computed(() => [{ id: null, label: '未設定' }, ...groups.value.map(g => ({ id: g.id!, label: `${g.code} — ${g.name}` }))])
const search = ref('')
const dialog = ref(false)
const form = ref<Item>(blank())

const fileInput = ref<HTMLInputElement | null>(null)
const importDialog = ref(false)
const importResult = ref<CsvImportResult | null>(null)

const headers = [
  { title: 'コード', key: 'code' },
  { title: '品名',   key: 'name' },
  { title: 'タイプ', key: 'type' },
  { title: 'Family', key: 'groupId' },
  { title: '単位',   key: 'uom' },
  { title: 'LLC',    key: 'lowLevelCode', align: 'end' as const },
  { title: 'LT(日)', key: 'leadTimeDays', align: 'end' as const },
  { title: 'ロット', key: 'lotSize',      align: 'end' as const },
  { title: '原価',   key: 'standardCost', align: 'end' as const },
  { title: '',       key: 'actions', sortable: false, align: 'end' as const }
]

function blank(): Item {
  return { code: '', name: '', type: 'FG', uom: 'EA',
           leadTimeDays: 0, safetyStock: 0, lotSize: 1, standardCost: 0,
           lotSizeMethod: 'LFL', poqPeriods: 1, orderingCost: 0, holdingCostPct: 0.20 }
}

async function load() { const [its, grps] = await Promise.all([ItemsApi.list(), SOPApi.groups()]); items.value = its; groups.value = grps }
onMounted(load)

async function recomputeLLC() {
  await ItemsApi.recomputeLLC()
  await load()
}

function groupLabel(id?: string | null) { const g = groups.value.find(x => x.id === id); return g ? g.code : '—' }
function openNew() { form.value = blank(); dialog.value = true }
function openEdit(it: Item) { form.value = { ...it }; dialog.value = true }

async function save() {
  if (form.value.id) await ItemsApi.update(form.value.id, form.value)
  else                await ItemsApi.create(form.value)
  dialog.value = false
  await load()
}
async function remove(it: Item) {
  if (!it.id) return
  if (!confirm(`${it.code} を削除します。よろしいですか？`)) return
  await ItemsApi.remove(it.id)
  await load()
}

async function exportCsv() {
  await CsvApi.downloadItems()
}
async function onFileChosen(e: Event) {
  const target = e.target as HTMLInputElement
  const f = target.files?.[0]
  if (!f) return
  try {
    importResult.value = await CsvApi.importItems(f)
    importDialog.value = true
    await load()
  } catch (err: any) {
    importResult.value = { inserted: 0, updated: 0, skipped: 0,
      errors: [err?.response?.data?.error || String(err)] }
    importDialog.value = true
  } finally {
    target.value = '' // allow re-selecting the same file
  }
}
</script>
