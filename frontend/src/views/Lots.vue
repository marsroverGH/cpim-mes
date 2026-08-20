<template>
  <div>
  <v-row>
    <v-col cols="12" md="7">
      <v-card>
        <v-card-title class="d-flex align-center">
          ロット一覧 (Traceability)
          <v-spacer />
          <v-btn variant="text" prepend-icon="mdi-barcode-scan" @click="scanOpen = true">
            スキャン
          </v-btn>
          <v-btn color="primary" prepend-icon="mdi-plus" @click="openNew">ロット登録</v-btn>
        </v-card-title>
        <v-card-text class="pb-0">
          <v-text-field v-model="search" prepend-inner-icon="mdi-magnify"
                        label="検索 (品目/ロット番号/サプライヤ)"
                        clearable density="compact" />
        </v-card-text>
        <v-data-table
          :items="lots"
          :headers="headers"
          :search="search"
          density="compact"
          hover
          @click:row="(_e: any, { item }: any) => select(item)"
        >
          <template #item.receivedAt="{ item }">{{ fmt(item.receivedAt) }}</template>
          <template #item.expiryDate="{ item }">
            <span v-if="item.expiryDate" :class="isExpired(item.expiryDate) ? 'text-error' : ''">
              {{ fmt(item.expiryDate) }}
            </span>
            <span v-else>—</span>
          </template>
          <template #item.balance="{ item }">
            <v-chip :color="(item.balance ?? 0) <= 0 ? 'grey' : 'primary'" size="small">
              {{ item.balance }}
            </v-chip>
          </template>
          <template #item.qualityStatus="{ item }">
            <v-chip size="x-small" :color="qualityColor(item.qualityStatus)">
              {{ item.qualityStatus || 'OK' }}
            </v-chip>
          </template>
        </v-data-table>
      </v-card>
    </v-col>

    <v-col cols="12" md="5">
      <v-card>
        <v-card-title>
          {{ selected ? `${selected.itemCode} – ${selected.lotNo}` : 'ロット詳細' }}
        </v-card-title>
        <v-card-text v-if="!selected" class="text-medium-emphasis">
          左の表でロットをクリックしてください
        </v-card-text>
        <v-card-text v-else>
          <v-list density="compact">
            <v-list-item>
              <v-list-item-title>受入日</v-list-item-title>
              <v-list-item-subtitle>{{ fmt(selected.receivedAt) }}</v-list-item-subtitle>
            </v-list-item>
            <v-list-item>
              <v-list-item-title>サプライヤ</v-list-item-title>
              <v-list-item-subtitle>{{ selected.supplier || '—' }}</v-list-item-subtitle>
            </v-list-item>
            <v-list-item>
              <v-list-item-title>参照伝票</v-list-item-title>
              <v-list-item-subtitle>{{ selected.sourceDoc || '—' }}</v-list-item-subtitle>
            </v-list-item>
            <v-list-item>
              <v-list-item-title>受入数 / 残数</v-list-item-title>
              <v-list-item-subtitle>{{ selected.quantity }} / {{ selected.balance }}</v-list-item-subtitle>
            </v-list-item>
          </v-list>

          <v-divider class="my-2" />
          <div class="text-subtitle-2 mt-2">移動履歴</div>
          <v-data-table :items="movements" :headers="mvHeaders" density="compact"
                        hide-default-footer :items-per-page="50">
            <template #item.occurredAt="{ item }">{{ fmt(item.occurredAt) }}</template>
            <template #item.quantity="{ item }">
              <span :class="(item.quantity ?? 0) >= 0 ? 'text-success' : 'text-error'">
                {{ item.quantity }}
              </span>
            </template>
          </v-data-table>

          <div class="text-subtitle-2 mt-3">使用先 (Where-used)</div>
          <v-list v-if="whereUsed.length" density="compact" lines="two">
            <v-list-item v-for="m in whereUsed" :key="m.id">
              <v-list-item-title>{{ m.refDoc || '(参照伝票なし)' }}</v-list-item-title>
              <v-list-item-subtitle>
                {{ m.movementType }} / {{ m.quantity }} / {{ fmt(m.occurredAt) }}
              </v-list-item-subtitle>
            </v-list-item>
          </v-list>
          <p v-else class="text-medium-emphasis text-body-2">使用記録なし</p>

          <v-btn class="mt-2" prepend-icon="mdi-plus" size="small"
                 @click="openMove">入出庫を記録</v-btn>

          <!-- Quality inspection panel -->
          <v-divider class="my-3" />
          <div class="text-subtitle-2 d-flex align-center">
            品質検査
            <v-spacer />
            <v-btn size="small" prepend-icon="mdi-magnify" variant="tonal"
                   @click="openInspection">検査記録</v-btn>
          </div>
          <v-list v-if="inspections.length" density="compact" lines="two">
            <v-list-item v-for="ins in inspections" :key="ins.id">
              <v-list-item-title>
                <v-chip size="x-small" :color="resultColor(ins.result)" class="mr-1">
                  {{ ins.result }}
                </v-chip>
                {{ ins.inspector || '(未記名)' }}
                <span v-if="ins.defectQty > 0" class="text-error">不良 {{ ins.defectQty }}</span>
              </v-list-item-title>
              <v-list-item-subtitle>
                {{ fmt(ins.inspectedAt) }} — {{ ins.previousStatus || '?' }} → {{ ins.resultingStatus }} — {{ ins.notes || '備考なし' }}
              </v-list-item-subtitle>
            </v-list-item>
          </v-list>
          <p v-else class="text-medium-emphasis text-body-2">検査記録なし</p>
        </v-card-text>
      </v-card>
    </v-col>
  </v-row>

  <!-- Lot create dialog -->
  <v-dialog v-model="dialog" max-width="540">
    <v-card title="ロット登録">
      <v-card-text>
        <v-select v-model="form.itemId" :items="itemOptions"
                  item-title="label" item-value="id" label="品目" />
        <v-text-field v-model="form.lotNo" label="ロット番号" />
        <v-text-field v-model.number="form.quantity" type="number" label="受入数量" />
        <v-text-field v-model="form.expiryDate" type="date" label="有効期限 (任意)" />
        <v-text-field v-model="form.supplier" label="サプライヤ" />
        <v-text-field v-model="form.sourceDoc" label="参照伝票 (PO/WO番号など)" />
        <v-textarea v-model="form.notes" label="備考" rows="2" />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="dialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="save">保存</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Movement dialog -->
  <v-dialog v-model="moveDialog" max-width="500">
    <v-card title="ロット入出庫">
      <v-card-text v-if="selected">
        <p class="text-body-2 text-medium-emphasis">
          {{ selected.itemCode }} – ロット {{ selected.lotNo }} (残: {{ selected.balance }})
        </p>
        <v-select v-model="mvForm.movementType" label="区分"
                  :items="['ISSUE','CONSUMED','PRODUCED','ADJUST','RECEIPT']" />
        <v-text-field v-model.number="mvForm.quantity" type="number"
                      label="数量 (出庫はマイナス値)" />
        <v-text-field v-model="mvForm.refDoc" label="参照伝票 (WO番号など)" />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="moveDialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="saveMove">記録</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Quality inspection dialog -->
  <v-dialog v-model="inspectionDialog" max-width="500">
    <v-card title="品質検査記録">
      <v-card-text v-if="selected">
        <p class="text-body-2 text-medium-emphasis">
          {{ selected.itemCode }} – ロット {{ selected.lotNo }}
        </p>
        <v-alert type="info" variant="tonal" density="compact" class="mb-3">
          検査者は現在ログイン中のユーザーとして自動記録されます。
        </v-alert>
        <v-select v-model="insForm.result"
                  :items="[
                    {title:'PASS — 合格 (在庫消費可)', value:'PASS'},
                    {title:'HOLD — 保留 (検査中)',     value:'HOLD'},
                    {title:'FAIL — 不合格 (廃棄候補)', value:'FAIL'}
                  ]" label="判定" />
        <v-text-field v-model.number="insForm.defectQty" type="number"
                      label="不良数 (任意)" />
        <v-textarea v-model="insForm.notes" label="検査コメント" rows="2" />
        <v-alert v-if="insForm.result !== 'PASS'" type="warning" variant="tonal" density="compact">
          {{ insForm.result === 'FAIL' ? 'REJECTED' : 'HOLD' }} 状態のロットは
          自動的に FIFO 消費から除外されます。
        </v-alert>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="inspectionDialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="saveInspection">記録</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <BarcodeScanner v-model="scanOpen" @detected="onScanned" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ItemsApi, LotsApi, QualityApi,
         type Item, type Lot, type LotMovement, type QualityInspection } from '@/api'
import BarcodeScanner from '@/components/BarcodeScanner.vue'

const items = ref<Item[]>([])
const lots = ref<Lot[]>([])
const selected = ref<Lot | null>(null)
const movements = ref<LotMovement[]>([])
const whereUsed = ref<LotMovement[]>([])
const search = ref('')

const dialog = ref(false)
const form = ref<Lot>(blank())

const moveDialog = ref(false)
const scanOpen = ref(false)
const inspectionDialog = ref(false)
const inspections = ref<QualityInspection[]>([])
const insForm = ref({ result: 'PASS' as 'PASS'|'FAIL'|'HOLD', defectQty: 0, notes: '' })
const mvForm = ref<Omit<LotMovement, 'lotId'>>({
  quantity: 0, movementType: 'ISSUE', refDoc: ''
})

const headers = [
  { title: '品目',   key: 'itemCode' },
  { title: '品名',   key: 'itemName' },
  { title: 'ロット', key: 'lotNo' },
  { title: '受入',   key: 'quantity', align: 'end' as const },
  { title: '残',     key: 'balance',  align: 'end' as const },
  { title: '品質',   key: 'qualityStatus' },
  { title: 'サプライヤ', key: 'supplier' },
  { title: '受入日', key: 'receivedAt' },
  { title: '期限',   key: 'expiryDate' }
]
const mvHeaders = [
  { title: '日時',   key: 'occurredAt' },
  { title: '区分',   key: 'movementType' },
  { title: '数量',   key: 'quantity', align: 'end' as const },
  { title: '伝票',   key: 'refDoc' }
]

const itemOptions = computed(() =>
  (items.value ?? []).map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))

function blank(): Lot {
  return { itemId: '', lotNo: '', quantity: 0, supplier: '',
           sourceDoc: '', notes: '', expiryDate: null }
}

async function load() {
  const [_p0, _p1] = await Promise.all([ItemsApi.list(), LotsApi.list()])
  items.value = _p0 ?? []
  lots.value = _p1 ?? []
}
onMounted(load)

async function select(l: Lot) {
  selected.value = l
  if (l.id) {
    const [_p0, _p1, _p2] = await Promise.all([LotsApi.movements(l.id), LotsApi.whereUsed(l.id), QualityApi.byLot(l.id)])
    movements.value = _p0 ?? []
    whereUsed.value = _p1 ?? []
    inspections.value = _p2 ?? []
  }
}

function openNew() { form.value = blank(); dialog.value = true }
async function save() {
  if (!form.value.expiryDate) form.value.expiryDate = null
  await LotsApi.create(form.value)
  dialog.value = false
  await load()
}

function openMove() {
  mvForm.value = { quantity: 0, movementType: 'ISSUE', refDoc: '' }
  moveDialog.value = true
}
async function saveMove() {
  if (!selected.value?.id) return
  await LotsApi.addMovement(selected.value.id, mvForm.value)
  moveDialog.value = false
  await load()
  // refresh selected
  const fresh = (lots.value ?? []).find(l => l.id === selected.value?.id)
  if (fresh) await select(fresh)
}

function isExpired(d?: string | null) {
  if (!d) return false
  return new Date(d) < new Date()
}
function fmt(d?: string | null) {
  return d ? new Date(d).toLocaleDateString('ja-JP') : ''
}

// バーコード/QR スキャン: 検索ボックスにロット番号を流し込む
function onScanned(code: string) {
  search.value = code.trim()
  // ロット番号 / 品目コードに完全一致するものがあれば自動選択
  const hit = (lots.value ?? []).find(l => l.lotNo === code.trim() || l.itemCode === code.trim())
  if (hit) select(hit)
}

// Quality
function openInspection() {
  insForm.value = { result: 'PASS', defectQty: 0, notes: '' }
  inspectionDialog.value = true
}
async function saveInspection() {
  if (!selected.value?.id) return
  await QualityApi.record(selected.value.id, insForm.value)
  inspectionDialog.value = false
  await load()
  // refresh selected
  const fresh = (lots.value ?? []).find(l => l.id === selected.value?.id)
  if (fresh) await select(fresh)
}
function qualityColor(s?: string) {
  const colors: Record<string, string> = { OK: 'success', HOLD: 'warning', REJECTED: 'error' }
  return colors[s || 'OK'] || 'grey'
}
function resultColor(r: string) {
  const colors: Record<string, string> = { PASS: 'success', HOLD: 'warning', FAIL: 'error' }
  return colors[r] || 'grey'
}
</script>
