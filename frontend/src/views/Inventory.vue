<template>
  <div>
  <v-row>
    <v-col cols="12" md="6">
      <v-card>
        <v-card-title class="d-flex align-center">
          在庫一覧
          <v-spacer />
          <v-chip class="mr-2" size="small" :color="reconciliationOK ? 'success' : 'error'">
            Lot整合: {{ reconciliationOK ? 'OK' : 'NG' }}
          </v-chip>
          <v-btn color="primary" prepend-icon="mdi-plus" @click="dialog = true">取引登録</v-btn>
        </v-card-title>
        <v-card-text class="pb-0">
          <v-text-field v-model="search" prepend-inner-icon="mdi-magnify"
                        label="検索" clearable density="compact" />
        </v-card-text>
        <v-data-table :items="onHand" :headers="ohHeaders" :search="search" density="compact" hover
                      @click:row="(_e: any, { item }: any) => selectItem(item.itemId)" />
      </v-card>
    </v-col>
    <v-col cols="12" md="6">
      <v-card>
        <v-card-title>取引履歴</v-card-title>
        <v-card-text v-if="!selectedItemId" class="text-medium-emphasis">
          左の表で品目をクリックしてください
        </v-card-text>
        <v-data-table v-else :items="txns" :headers="txnHeaders" density="compact">
          <template #item.occurredAt="{ item }">{{ fmt(item.occurredAt) }}</template>
          <template #item.quantity="{ item }">
            <span :class="(item.quantity ?? 0) >= 0 ? 'text-success' : 'text-error'">
              {{ item.quantity }}
            </span>
          </template>
        </v-data-table>
      </v-card>
    </v-col>
  </v-row>

  <v-dialog v-model="dialog" max-width="500">
    <v-card title="在庫取引登録">
      <v-card-text>
        <div class="d-flex align-center" style="gap: 8px">
          <v-select v-model="form.itemId" :items="itemOptions"
                    item-title="label" item-value="id" label="品目" class="flex-grow-1" />
          <v-btn icon="mdi-barcode-scan" variant="tonal" size="small"
                 title="バーコード/QRをスキャン" @click="scanOpen = true" />
        </div>
        <v-select v-model="form.txnType" :items="['RECEIPT','ISSUE','ADJUST']" label="取引区分" />
        <v-text-field v-model.number="form.quantity" type="number"
                      label="数量 (RECEIPTは正、ISSUEは負)" />
        <v-text-field v-if="form.txnType === 'RECEIPT' || form.quantity > 0"
                      v-model="form.lotNo" label="ロット番号 (空欄は自動採番)" />
        <v-select v-else v-model="form.lotId" :items="selectedLotOptions"
                  item-title="label" item-value="id" clearable
                  label="出庫ロット (空欄はFIFO自動)" />
        <v-text-field v-model="form.refDoc" label="参照伝票" />
        <v-alert type="info" variant="tonal" density="compact" class="mt-2">
          物理在庫取引は必ずロットへ同時配賦されます。総在庫だけを直接変更することはできません。
        </v-alert>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="dialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="save">保存</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <BarcodeScanner v-model="scanOpen" @detected="onScanned" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { InventoryApi, ItemsApi, LotsApi,
         type InventoryTxn, type Item, type OnHandRow, type Lot,
         type InventoryLotReconciliation } from '@/api'
import BarcodeScanner from '@/components/BarcodeScanner.vue'

const items = ref<Item[]>([])
const onHand = ref<OnHandRow[]>([])
const txns = ref<InventoryTxn[]>([])
const lots = ref<Lot[]>([])
const reconciliation = ref<InventoryLotReconciliation[]>([])
const selectedItemId = ref<string>('')
const dialog = ref(false)
const search = ref('')
const scanOpen = ref(false)
const form = ref<InventoryTxn>({ itemId: '', quantity: 0, txnType: 'RECEIPT', refDoc: '' })

const ohHeaders = [
  { title: 'コード', key: 'itemCode' },
  { title: '品名', key: 'itemName' },
  { title: '在庫', key: 'onHand', align: 'end' as const }
]
const txnHeaders = [
  { title: '日時', key: 'occurredAt' },
  { title: '区分', key: 'txnType' },
  { title: '数量', key: 'quantity', align: 'end' as const },
  { title: '伝票', key: 'refDoc' }
]

const itemOptions = computed(() => (items.value ?? []).map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))
const reconciliationOK = computed(() => (reconciliation.value ?? []).every(r => Math.abs(r.difference ?? 0) < 0.000001))
const selectedLotOptions = computed(() => (lots.value ?? [])
  .filter(l => l.itemId === form.value.itemId && (l.balance ?? 0) > 0 && (l.qualityStatus ?? 'OK') === 'OK')
  .map(l => ({ id: l.id!, label: `${l.lotNo} – 残 ${l.balance ?? 0}` })))

async function load() {
  const [_p0, _p1, _p2, _p3] = await Promise.all([
    ItemsApi.list(), InventoryApi.onHand(), LotsApi.list(), InventoryApi.reconciliation()
  ])
  items.value = _p0 ?? []
  onHand.value = _p1 ?? []
  lots.value = _p2 ?? []
  reconciliation.value = _p3 ?? []
}
onMounted(load)

async function selectItem(itemId: string) {
  selectedItemId.value = itemId
  txns.value = (await InventoryApi.txns(itemId)) ?? []
}

async function save() {
  await InventoryApi.post(form.value)
  dialog.value = false
  form.value = { itemId: '', quantity: 0, txnType: 'RECEIPT', refDoc: '' }
  await load()
  if (selectedItemId.value) await selectItem(selectedItemId.value)
}

function fmt(d?: string) { return d ? new Date(d).toLocaleString('ja-JP') : '' }

// バーコード/QR をスキャンして該当品目を選択
function onScanned(code: string) {
  // コード文字列が品目コードに一致 → 即適用
  const match = (items.value ?? []).find(i => i.code === code.trim())
  if (match) {
    form.value.itemId = match.id!
    return
  }
  // フォールバック: id (UUID) として扱う
  const byId = (items.value ?? []).find(i => i.id === code.trim())
  if (byId) {
    form.value.itemId = byId.id!
    return
  }
  alert(`スキャンしたコード "${code}" に一致する品目が見つかりませんでした`)
}
</script>
