<template>
  <div>
  <v-card>
    <v-card-title class="d-flex align-center">
      製造指示 (Work Orders)
      <v-spacer />
      <v-btn color="primary" prepend-icon="mdi-plus" @click="dialog = true">新規</v-btn>
    </v-card-title>
    <v-card-text class="pb-0">
      <v-text-field v-model="search" prepend-inner-icon="mdi-magnify"
                    label="検索 (WO番号/ステータス)" clearable density="compact" />
    </v-card-text>
    <v-data-table :items="wos" :headers="headers" :search="search" density="comfortable">
      <template #item.itemCode="{ item }">{{ codeMap[item.itemId] || item.itemId }}</template>
      <template #item.startDate="{ item }">{{ fmt(item.startDate) }}</template>
      <template #item.dueDate="{ item }">{{ fmt(item.dueDate) }}</template>
      <template #item.status="{ item }">
        <v-chip :color="statusColor(item.status)" size="small">{{ item.status }}</v-chip>
        <v-chip v-if="item.producedLotId" size="x-small" color="success" class="ml-1"
                prepend-icon="mdi-package-variant">
          ロット作成済
        </v-chip>
      </template>
      <template #item.actions="{ item }">
        <!-- Release button -->
        <v-btn v-if="item.status === 'PLANNED'"
               size="small" variant="tonal" color="primary" prepend-icon="mdi-send"
               class="mr-1" @click="release(item)">リリース</v-btn>
        <!-- Progress + Complete buttons -->
        <v-btn v-if="item.status === 'RELEASED' || item.status === 'IN_PROGRESS'"
               size="small" variant="tonal" color="info" prepend-icon="mdi-progress-clock"
               class="mr-1" @click="openProgress(item)">進捗入力</v-btn>
        <v-btn v-if="item.status === 'RELEASED' || item.status === 'IN_PROGRESS'"
               size="small" variant="tonal" color="success" prepend-icon="mdi-check-circle"
               class="mr-1" @click="openComplete(item)">完成入力</v-btn>
        <v-btn v-if="item.status !== 'PLANNED'"
               size="small" variant="text" prepend-icon="mdi-file-tree"
               class="mr-1" @click="showSnapshot(item)">BOM固定</v-btn>
        <!-- Other status transitions -->
        <v-menu>
          <template #activator="{ props }">
            <v-btn icon="mdi-dots-vertical" variant="text" size="small" v-bind="props" />
          </template>
          <v-list>
            <v-list-item v-for="s in transitions(item.status)" :key="s"
                         @click="setStatus(item, s)">
              <v-list-item-title>→ {{ s }}</v-list-item-title>
            </v-list-item>
          </v-list>
        </v-menu>
      </template>
      <template #item.completedQty="{ item }">
        <div v-if="item.status !== 'PLANNED'" class="d-flex align-center" style="gap: 6px">
          <v-progress-linear
            :model-value="(item.completedQty ?? 0) / item.quantity * 100"
            color="primary" height="6" rounded style="min-width: 70px"
          />
          <span class="text-caption">
            {{ item.completedQty ?? 0 }} / {{ item.quantity }}
          </span>
        </div>
        <span v-else class="text-medium-emphasis">—</span>
      </template>
    </v-data-table>
  </v-card>

  <!-- Completion dialog -->
  <v-dialog v-model="completeDialog" max-width="540">
    <v-card title="WO 完成入力">
      <v-card-text v-if="activeWO">
        <p class="text-body-2 text-medium-emphasis">
          {{ activeWO.orderNo }} ({{ codeMap[activeWO.itemId] }} × {{ activeWO.quantity }})
        </p>
        <p class="mt-2">
          在庫計上済み完成: <strong>{{ activeWO.completedQty ?? 0 }}</strong> / {{ activeWO.quantity }}
          （残 {{ remainingQty(activeWO) }}）
        </p>
        <v-alert v-if="!finalOperation" type="error" variant="tonal" density="compact" class="mt-2">
          最終工程がありません。Shop Floor の最終工程実績なしでは完成品を入庫できません。
        </v-alert>
        <v-alert v-else type="info" variant="tonal" density="compact" class="mt-2">
          最終工程 #{{ finalOperation.seqNo }} 実績: {{ finalOperation.completedQty }} / {{ activeWO.quantity }}<br>
          現在入庫可能: <strong>{{ finalReceiptAvailable(activeWO) }}</strong>
        </v-alert>
        <v-text-field v-model.number="completeForm.quantity" type="number"
                      :max="finalReceiptAvailable(activeWO)" min="0.000001"
                      :disabled="!finalOperation || finalReceiptAvailable(activeWO) <= 0"
                      label="今回完成数量" class="mt-2" />
        <v-text-field v-model="completeForm.lotNo"
                      label="完成ロット番号"
                      hint="空欄なら部分完成ごとに一意なロットを自動採番。同じ番号を指定すると同一WOロットへ追加入庫します。"
                      persistent-hint />
        <v-alert type="info" variant="tonal" density="compact" class="mt-2">
          今回完成数量の分だけ、直下子部品を予約解除・出庫し、完成品を入庫します。
          最終数量に到達した時だけWOを COMPLETED にします。
        </v-alert>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="completeDialog = false">キャンセル</v-btn>
        <v-btn color="success" :loading="busy"
               :disabled="!finalOperation || !activeWO || finalReceiptAvailable(activeWO) <= 0"
               @click="doComplete">完成確定</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Progress dialog -->
  <v-dialog v-model="progressDialog" max-width="500">
    <v-card title="WO 進捗入力">
      <v-card-text v-if="activeWO">
        <p class="text-body-2 text-medium-emphasis">
          {{ activeWO.orderNo }} ({{ codeMap[activeWO.itemId] }} × {{ activeWO.quantity }})
        </p>
        <p>在庫計上済み完成: <strong>{{ activeWO.completedQty ?? 0 }}</strong> / {{ activeWO.quantity }}</p>
        <p>参考進捗: <strong>{{ activeWO.reportedProgressQty ?? activeWO.completedQty ?? 0 }}</strong></p>
        <v-text-field v-model.number="progressForm.completedQty" type="number"
                      :max="activeWO.quantity" min="0" label="参考進捗数量（在庫は動きません）" />
        <v-progress-linear
          :model-value="(progressForm.completedQty ?? 0) / activeWO.quantity * 100"
          color="primary" height="10" rounded class="mt-2"
        />
        <p class="text-caption text-center mt-1">
          {{ ((progressForm.completedQty ?? 0) / activeWO.quantity * 100).toFixed(1) }}% 完了
        </p>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="progressDialog = false">キャンセル</v-btn>
        <v-btn color="primary" :loading="busy" @click="doProgress">記録</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Result dialog (release or completion) -->
  <v-dialog v-model="resultDialog" max-width="640">
    <v-card title="処理結果">
      <v-card-text>
        <pre style="white-space: pre-wrap; font-size: 0.85em">{{ resultText }}</pre>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="resultDialog = false">閉じる</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <v-dialog v-model="dialog" max-width="500">
    <v-card title="製造指示登録">
      <v-card-text>
        <v-text-field v-model="form.orderNo" label="WO番号" />
        <v-select v-model="form.itemId" :items="itemOptions"
                  item-title="label" item-value="id" label="品目" />
        <v-text-field v-model.number="form.quantity" type="number" label="製造数量" />
        <v-text-field v-model="form.startDate" type="date" label="開始日" />
        <v-text-field v-model="form.dueDate" type="date" label="納期" />
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
import { ItemsApi, WorkOrdersApi, WorkflowApi, WIPApi, ShopFloorApi,
         type Item, type WorkOrder, type WOOperationDetail } from '@/api'

const items = ref<Item[]>([])
const wos = ref<WorkOrder[]>([])
const dialog = ref(false)
const search = ref('')
const busy = ref(false)
const completeDialog = ref(false)
const progressDialog = ref(false)
const activeWO = ref<WorkOrder | null>(null)
const finalOperation = ref<WOOperationDetail | null>(null)
const completeForm = ref<{ quantity: number; lotNo: string; completionId: string }>({ quantity: 0, lotNo: '', completionId: '' })
const progressForm = ref<{ completedQty: number }>({ completedQty: 0 })
const resultDialog = ref(false)
const resultText = ref('')
const form = ref<WorkOrder>({
  orderNo: '', itemId: '', quantity: 1, startDate: '', dueDate: '', status: 'PLANNED'
})

const codeMap = computed(() => {
  const m: Record<string, string> = {}
  for (const i of (items.value ?? [])) if (i.id) m[i.id] = `${i.code} – ${i.name}`
  return m
})
const itemOptions = computed(() =>
  (items.value ?? []).filter(i => i.type === 'FG' || i.type === 'SA')
    .map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))

const headers = [
  { title: 'WO番号', key: 'orderNo' },
  { title: '品目',   key: 'itemCode' },
  { title: '数量',   key: 'quantity', align: 'end' as const },
  { title: '開始',   key: 'startDate' },
  { title: '納期',   key: 'dueDate' },
  { title: 'ステータス', key: 'status' },
  { title: '進捗',   key: 'completedQty', sortable: false },
  { title: '',       key: 'actions',  sortable: false, align: 'end' as const }
]

async function load() {
  const [_p0, _p1] = await Promise.all([ItemsApi.list(), WorkOrdersApi.list()])
  items.value = _p0 ?? []
  wos.value = _p1 ?? []
}
onMounted(load)

async function save() {
  form.value.orderNo = form.value.orderNo || `WO-${Date.now()}`
  await WorkOrdersApi.create(form.value)
  dialog.value = false
  form.value = { orderNo: '', itemId: '', quantity: 1, startDate: '', dueDate: '', status: 'PLANNED' }
  await load()
}

function transitions(s: WorkOrder['status']): WorkOrder['status'][] {
  // Inventory-affecting transitions use dedicated workflow buttons only.
  const flow: Record<WorkOrder['status'], WorkOrder['status'][]> = {
    PLANNED: [],
    RELEASED: [],
    IN_PROGRESS: [],
    COMPLETED: ['CLOSED'],
    CLOSED: []
  }
  return flow[s] || []
}

async function setStatus(w: WorkOrder, s: WorkOrder['status']) {
  if (!w.id) return
  await WorkOrdersApi.setStatus(w.id, s)
  await load()
}

function statusColor(s: string) {
  const colors: Record<string, string> = {
    PLANNED: 'grey',
    RELEASED: 'success',
    IN_PROGRESS: 'warning',
    COMPLETED: 'info',
    CLOSED: 'primary'
  }
  return colors[s] || 'grey'
}
function fmt(d: string) { return d ? new Date(d).toLocaleDateString('ja-JP') : '' }

async function release(w: WorkOrder) {
  if (!w.id) return
  busy.value = true
  try {
    const res = await WorkflowApi.releaseWO(w.id)
    const lines = res.reservations.map(r =>
      `  ${r.childCode}: 必要 ${r.required} / 利用可能 ${r.available} ` +
      `[${r.sufficient ? 'OK' : '不足'}]`).join('\n')
    resultText.value =
      `✅ WO ${res.orderNo} をリリースしました\n` +
      `BOM Snapshot: ${res.bomSnapshotId}\n` +
      `Snapshot日時: ${new Date(res.bomSnapshotAt).toLocaleString('ja-JP')}\n\n` +
      `予約された部品:\n${lines}`
    resultDialog.value = true
    await load()
  } catch (e: any) {
    resultText.value = '❌ リリース失敗:\n' + (e?.response?.data?.message || e?.message || String(e))
    resultDialog.value = true
  } finally {
    busy.value = false
  }
}

async function showSnapshot(w: WorkOrder) {
  if (!w.id) return
  busy.value = true
  try {
    const res = await WorkflowApi.getBOMSnapshot(w.id)
    const lines = res.lines.map(l =>
      `  ${l.lineNo}. ${l.childCode} ${l.childName}: ` +
      `${l.quantityPer}/${l.childUom} scrap ${(l.scrapPct * 100).toFixed(2)}% ` +
      `(WO必要 ${l.requiredQty})`).join('\n')
    resultText.value =
      `🔒 WO ${w.orderNo} Release時BOM Snapshot\n\n` +
      `Snapshot ID: ${res.snapshot.id}\n` +
      `取得日時: ${new Date(res.snapshot.capturedAt).toLocaleString('ja-JP')}\n` +
      `Source: ${res.snapshot.source}\n\n` +
      `固定構成品:\n${lines || '  直下構成品なし'}`
    resultDialog.value = true
  } catch (e: any) {
    resultText.value = '❌ BOM Snapshot取得失敗:\n' +
      (e?.response?.data?.message || e?.message || String(e))
    resultDialog.value = true
  } finally {
    busy.value = false
  }
}

function remainingQty(w: WorkOrder) {
  return Math.max(0, w.quantity - (w.completedQty ?? 0))
}

function finalReceiptAvailable(w: WorkOrder) {
  if (!finalOperation.value) return 0
  return Math.max(0, Math.min(
    remainingQty(w),
    (finalOperation.value.completedQty ?? 0) - (w.completedQty ?? 0)
  ))
}

async function openComplete(w: WorkOrder) {
  activeWO.value = w
  finalOperation.value = null
  if (w.id) {
    const ops = await ShopFloorApi.forWO(w.id)
    finalOperation.value = (ops ?? []).slice().sort((a, b) => b.seqNo - a.seqNo)[0] ?? null
  }
  completeForm.value = {
    quantity: finalReceiptAvailable(w),
    lotNo: '',
    completionId: crypto.randomUUID()
  }
  completeDialog.value = true
}

function openProgress(w: WorkOrder) {
  activeWO.value = w
  progressForm.value = { completedQty: w.reportedProgressQty ?? w.completedQty ?? 0 }
  progressDialog.value = true
}

async function doProgress() {
  if (!activeWO.value?.id) return
  busy.value = true
  try {
    const r = await WIPApi.updateProgress(activeWO.value.id, progressForm.value.completedQty)
    resultText.value =
      `✅ WO ${activeWO.value.orderNo} 進捗更新\n\n` +
      `参考進捗: ${r.reportedProgressQty} / ${r.plannedQty} (${r.percentDone.toFixed(1)}%)\n` +
      `在庫計上済み完成: ${r.completedQty} / ${r.plannedQty}`
    progressDialog.value = false
    resultDialog.value = true
    await load()
  } catch (e: any) {
    resultText.value = '❌ 進捗更新失敗:\n' + (e?.response?.data?.message || e?.message || String(e))
    resultDialog.value = true
  } finally {
    busy.value = false
  }
}

async function doComplete() {
  if (!activeWO.value?.id) return
  busy.value = true
  try {
    const res = await WorkflowApi.completeWO(
      activeWO.value.id,
      completeForm.value.quantity,
      completeForm.value.lotNo,
      completeForm.value.completionId
    )
    const consumed = res.consumedLots.map(c =>
      `  ${c.childCode}: ${c.quantity} (ロット ${c.lotNo || '—'})`).join('\n')
    resultText.value =
      `✅ WO ${res.orderNo} 部分完成処理${res.idempotentHit ? '（再送・二重計上なし）' : ''}\n\n` +
      `BOM Snapshot: ${res.bomSnapshotId}\n` +
      `今回完成: ${res.completedNow}\n` +
      `累計完成: ${res.completedQty} / ${res.plannedQty}\n` +
      `残数量: ${res.remainingQty}\n` +
      `ステータス: ${res.status}\n` +
      `最終工程 #${res.finalOperationSeqNo}: 実績 ${res.finalOperationCompletedQty}, 追加入庫可能 ${res.finalOperationAvailableQty}\n\n` +
      `📦 完成ロット: ${res.producedLot.lotNo} (今回数量 ${res.producedLot.quantity})\n\n` +
      `🔻 今回消費した部品:\n${consumed || '  なし'}`
    completeDialog.value = false
    resultDialog.value = true
    await load()
  } catch (e: any) {
    resultText.value = '❌ 完成処理失敗:\n' + (e?.response?.data?.message || e?.message || String(e))
    resultDialog.value = true
  } finally {
    busy.value = false
  }
}
</script>
