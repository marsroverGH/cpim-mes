<template>
  <div>
  <v-card>
    <v-card-title class="d-flex align-center">
      購買発注 (Purchase Orders)
      <v-spacer />
      <v-btn color="primary" prepend-icon="mdi-plus" @click="dialog = true">新規</v-btn>
    </v-card-title>
    <v-card-text class="pb-0">
      <v-text-field v-model="search" prepend-inner-icon="mdi-magnify"
                    label="検索 (PO番号/サプライヤ)" clearable density="compact" />
    </v-card-text>
    <v-data-table :items="pos" :headers="headers" :search="search" density="comfortable">
      <template #item.itemCode="{ item }">{{ codeMap[item.itemId] || item.itemId }}</template>
      <template #item.orderDate="{ item }">{{ fmt(item.orderDate) }}</template>
      <template #item.dueDate="{ item }">{{ fmt(item.dueDate) }}</template>
      <template #item.expectedDeliveryDate="{ item }">{{ fmt(item.expectedDeliveryDate || item.dueDate) }}</template>
      <template #item.scheduleStatus="{ item }">
        <v-chip size="small" :color="scheduleColor(item.scheduleStatus)">{{ item.scheduleStatus || 'UNCONFIRMED' }}</v-chip>
      </template>
      <template #item.receivedQty="{ item }">{{ n(item.receivedQty) }}</template>
      <template #item.remainingQty="{ item }">{{ n(remaining(item)) }}</template>
      <template #item.supplierQualityStatus="{ item }">
        <v-chip size="small" :color="supplierQualityColor(item.supplierQualityStatus)">
          {{ item.supplierQualityStatus || 'APPROVED' }}
        </v-chip>
      </template>
      <template #item.status="{ item }">
        <v-chip size="small" :color="statusColor(item.status)">{{ item.status }}</v-chip>
      </template>
      <template #item.actions="{ item }">
        <div class="d-flex ga-1 justify-end">
          <v-btn v-if="item.status === 'OPEN' || item.status === 'PARTIALLY_RECEIVED'"
                 size="small" variant="tonal" color="primary" prepend-icon="mdi-truck-delivery"
                 :disabled="item.supplierQualityStatus === 'BLOCKED'"
                 @click="openReceive(item)">入荷</v-btn>
          <v-btn v-if="item.status === 'OPEN' || item.status === 'PARTIALLY_RECEIVED'"
                 size="small" variant="text" prepend-icon="mdi-calendar-check"
                 @click="openSchedule(item)">Schedule</v-btn>
          <v-btn v-if="n(item.receivedQty) > 0"
                 size="small" variant="text" prepend-icon="mdi-history"
                 @click="openHistory(item)">履歴</v-btn>
        </div>
      </template>
    </v-data-table>
  </v-card>

  <!-- New PO dialog -->
  <v-dialog v-model="dialog" max-width="500">
    <v-card title="購買発注">
      <v-card-text>
        <v-text-field v-model="form.poNo" label="PO番号" />
        <v-select v-model="form.itemId" :items="itemOptions"
                  item-title="label" item-value="id" label="品目 (RM/PP)" />
        <v-text-field v-model="form.supplier" label="サプライヤ" />
        <v-text-field v-model.number="form.quantity" type="number" min="0.000001" label="発注数量" />
        <v-text-field v-model="form.dueDate" type="date" label="納期" />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="dialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="save">保存</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Partial receipt dialog -->
  <v-dialog v-model="receiveDialog" max-width="560">
    <v-card title="PO 部分入荷">
      <v-card-text v-if="activePO">
        <p class="text-body-2 text-medium-emphasis mb-3">
          {{ activePO.poNo }} / {{ codeMap[activePO.itemId] }} / {{ activePO.supplier }}
        </p>
        <div class="d-flex ga-4 mb-3">
          <v-chip>発注 {{ n(activePO.quantity) }}</v-chip>
          <v-chip color="success">入荷済 {{ n(activePO.receivedQty) }}</v-chip>
          <v-chip color="warning">残 {{ n(remaining(activePO)) }}</v-chip>
          <v-chip :color="supplierQualityColor(activePO.supplierQualityStatus)">
            Supplier Q: {{ activePO.supplierQualityStatus || 'APPROVED' }}
          </v-chip>
        </div>
        <v-alert v-if="activePO.supplierQualityStatus === 'BLOCKED'" type="error" variant="tonal" density="compact" class="mb-3">
          このSupplierはSupplier QualityでBLOCKEDです。入荷処理はBackendでも拒否されます。
        </v-alert>
        <v-text-field v-model.number="receiveForm.quantity" type="number"
                      :min="0.000001" :max="remaining(activePO)"
                      label="今回入荷数量" />
        <v-text-field v-model="receiveForm.lotNo"
                      label="入荷ロット番号"
                      hint="空欄なら receiptId を含む一意ロット番号を自動採番" persistent-hint />
        <v-alert type="info" variant="tonal" density="compact" class="mt-3">
          receiptId: {{ receiveForm.receiptId }}<br>
          同じ入荷要求を再送しても、このIDにより二重入庫されません。
        </v-alert>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="receiveDialog = false">キャンセル</v-btn>
        <v-btn color="primary" :loading="busy" :disabled="!canReceive" @click="doReceive">入荷確定</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Receipt history -->
  <v-dialog v-model="historyDialog" max-width="900">
    <v-card :title="`入荷履歴${historyPO ? ' — ' + historyPO.poNo : ''}`">
      <v-card-text>
        <v-data-table :items="receiptHistory" :headers="historyHeaders" density="compact">
          <template #item.receivedAt="{ item }">{{ fmtDateTime(item.receivedAt) }}</template>
          <template #item.quantity="{ item }">{{ n(item.quantity) }}</template>
          <template #item.receiptId="{ item }"><code>{{ item.receiptId }}</code></template>
        </v-data-table>
      </v-card-text>
      <v-card-actions><v-spacer /><v-btn @click="historyDialog=false">閉じる</v-btn></v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Supplier schedule / confirmation -->
  <v-dialog v-model="scheduleDialog" max-width="980">
    <v-card :title="`Supplier Schedule${schedulePO ? ' — ' + schedulePO.poNo : ''}`">
      <v-card-text v-if="schedulePO">
        <v-alert type="info" variant="tonal" density="compact" class="mb-3">
          Planning date precedence: ASN → Supplier Confirmation → Lead-Time Reliability → PO Due Date。0035ではSchedule数量は現在のPO残数量全体を表します。
        </v-alert>
        <div class="d-flex flex-wrap ga-2 mb-4">
          <v-chip>PO Due: {{ fmt(schedulePO.dueDate) }}</v-chip>
          <v-chip color="primary">Expected: {{ fmt(schedulePO.expectedDeliveryDate || schedulePO.dueDate) }}</v-chip>
          <v-chip :color="scheduleColor(schedulePO.scheduleStatus)">{{ schedulePO.scheduleSource || 'PO_DUE_DATE' }}</v-chip>
          <v-chip v-if="schedulePO.reliabilitySampleCount">Reliability n={{ schedulePO.reliabilitySampleCount }} / P90 {{ n(schedulePO.reliabilityP90Days) }}d</v-chip>
        </div>
        <v-row>
          <v-col cols="12" md="3">
            <v-select v-model="scheduleForm.eventType" :items="['CONFIRM','REVISE','ASN','CANCEL']" label="Event" />
          </v-col>
          <v-col cols="12" md="3" v-if="scheduleForm.eventType !== 'CANCEL'">
            <v-text-field v-model.number="scheduleForm.quantity" type="number" min="0.000001" label="Quantity" />
          </v-col>
          <v-col cols="12" md="3" v-if="scheduleForm.eventType === 'CONFIRM' || scheduleForm.eventType === 'REVISE'">
            <v-text-field v-model="scheduleForm.confirmedDeliveryDate" type="date" label="Confirmed delivery" />
          </v-col>
          <v-col cols="12" md="3" v-if="scheduleForm.eventType === 'ASN'">
            <v-text-field v-model="scheduleForm.asnNo" label="ASN No." />
          </v-col>
          <v-col cols="12" md="3" v-if="scheduleForm.eventType === 'ASN'">
            <v-text-field v-model="scheduleForm.expectedArrivalDate" type="date" label="Expected arrival" />
          </v-col>
          <v-col cols="12" md="4">
            <v-text-field v-model="scheduleForm.supplierReference" label="Supplier reference" />
          </v-col>
          <v-col cols="12" md="8">
            <v-text-field v-model="scheduleForm.notes" label="Notes" />
          </v-col>
        </v-row>
        <div class="d-flex justify-end mb-4">
          <v-btn color="primary" :loading="busy" @click="saveScheduleEvent">Append event</v-btn>
        </div>
        <v-data-table :items="scheduleHistory" :headers="scheduleHeaders" density="compact">
          <template #item.occurredAt="{ item }">{{ fmtDateTime(item.occurredAt) }}</template>
          <template #item.confirmedDeliveryDate="{ item }">{{ fmt(item.confirmedDeliveryDate) }}</template>
          <template #item.expectedArrivalDate="{ item }">{{ fmt(item.expectedArrivalDate) }}</template>
        </v-data-table>
      </v-card-text>
      <v-card-actions><v-spacer /><v-btn @click="scheduleDialog=false">閉じる</v-btn></v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Result dialog -->
  <v-dialog v-model="resultDialog" max-width="680">
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
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ItemsApi, PurchaseOrdersApi, WorkflowApi,
         type Item, type PurchaseOrder, type PurchaseReceipt, type SupplierScheduleEvent } from '@/api'

const items = ref<Item[]>([])
const pos = ref<PurchaseOrder[]>([])
const dialog = ref(false)
const search = ref('')
const busy = ref(false)
const form = ref<PurchaseOrder>({
  poNo: '', itemId: '', supplier: '', quantity: 1, dueDate: '', status: 'OPEN'
})

const receiveDialog = ref(false)
const activePO = ref<PurchaseOrder | null>(null)
const receiveForm = ref<{ receiptId: string; quantity: number; lotNo: string }>({
  receiptId: '', quantity: 0, lotNo: ''
})
const resultDialog = ref(false)
const resultText = ref('')

const historyDialog = ref(false)
const historyPO = ref<PurchaseOrder | null>(null)
const receiptHistory = ref<PurchaseReceipt[]>([])
const scheduleDialog = ref(false)
const schedulePO = ref<PurchaseOrder | null>(null)
const scheduleHistory = ref<SupplierScheduleEvent[]>([])
const scheduleForm = ref({
  eventType: 'CONFIRM' as SupplierScheduleEvent['eventType'], quantity: 0,
  confirmedDeliveryDate: '', asnNo: '', expectedArrivalDate: '', supplierReference: '', notes: ''
})

const codeMap = computed(() => {
  const m: Record<string, string> = {}
  for (const i of (items.value ?? [])) if (i.id) m[i.id] = `${i.code} – ${i.name}`
  return m
})
const itemOptions = computed(() =>
  (items.value ?? []).filter(i => i.type === 'RM' || i.type === 'PP')
    .map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))

const headers = [
  { title: 'PO番号',     key: 'poNo' },
  { title: '品目',       key: 'itemCode' },
  { title: 'サプライヤ', key: 'supplier' },
  { title: 'Supplier Q', key: 'supplierQualityStatus' },
  { title: '発注',       key: 'quantity', align: 'end' as const },
  { title: '入荷済',     key: 'receivedQty', align: 'end' as const },
  { title: '残数量',     key: 'remainingQty', align: 'end' as const },
  { title: '発注日',     key: 'orderDate' },
  { title: 'PO納期',     key: 'dueDate' },
  { title: '予定入荷',   key: 'expectedDeliveryDate' },
  { title: 'Schedule',   key: 'scheduleStatus' },
  { title: 'ステータス', key: 'status' },
  { title: '',           key: 'actions', sortable: false, align: 'end' as const }
]
const scheduleHeaders = [
  { title: 'Rev', key: 'revisionNo' },
  { title: 'Event', key: 'eventType' },
  { title: '数量', key: 'quantity' },
  { title: 'Confirmed', key: 'confirmedDeliveryDate' },
  { title: 'ASN', key: 'asnNo' },
  { title: 'Expected arrival', key: 'expectedArrivalDate' },
  { title: 'Actor', key: 'actorUsername' },
  { title: '時刻', key: 'occurredAt' }
]
const historyHeaders = [
  { title: '入荷日時', key: 'receivedAt' },
  { title: '数量', key: 'quantity', align: 'end' as const },
  { title: 'ロット', key: 'lotNo' },
  { title: '受入者', key: 'receivedByUsername' },
  { title: 'receiptId', key: 'receiptId' }
]

function n(v?: number) { return Number(v ?? 0) }
function remaining(p: PurchaseOrder) {
  if (p.remainingQty != null) return Math.max(0, Number(p.remainingQty))
  return Math.max(0, Number(p.quantity) - Number(p.receivedQty ?? 0))
}
const canReceive = computed(() => {
  if (!activePO.value || activePO.value.supplierQualityStatus === 'BLOCKED') return false
  const q = Number(receiveForm.value.quantity)
  return q > 0 && q <= remaining(activePO.value) + 0.000001
})

function supplierQualityColor(status?: PurchaseOrder['supplierQualityStatus']) {
  if (status === 'BLOCKED') return 'error'
  if (status === 'CONDITIONAL') return 'warning'
  return 'success'
}

async function load() {
  const [_p0, _p1] = await Promise.all([ItemsApi.list(), PurchaseOrdersApi.list()])
  items.value = _p0 ?? []
  pos.value = _p1 ?? []
}
onMounted(load)

async function save() {
  form.value.poNo = form.value.poNo || `PO-${Date.now()}`
  await PurchaseOrdersApi.create(form.value)
  dialog.value = false
  form.value = { poNo: '', itemId: '', supplier: '', quantity: 1, dueDate: '', status: 'OPEN' }
  await load()
}

function openReceive(p: PurchaseOrder) {
  if (p.supplierQualityStatus === 'BLOCKED') return
  activePO.value = p
  receiveForm.value = {
    receiptId: crypto.randomUUID(),
    quantity: remaining(p),
    lotNo: ''
  }
  receiveDialog.value = true
}

async function doReceive() {
  if (!activePO.value?.id || !canReceive.value) return
  busy.value = true
  try {
    const f = receiveForm.value
    const res = await WorkflowApi.receivePO(activePO.value.id, f.quantity, f.lotNo, f.receiptId)
    resultText.value =
      `✅ PO ${res.poNo} 入荷完了${res.idempotentHit ? '（再送: 二重入庫なし）' : ''}\n\n` +
      `receiptId: ${res.receiptId}\n` +
      `📦 入荷ロット: ${res.lotNo}\n` +
      `今回入荷: ${res.quantity}\n` +
      `累計入荷: ${res.receivedQty} / ${res.orderedQty}\n` +
      `残数量: ${res.remainingQty}\n` +
      `Status: ${res.status}\n` +
      `受入者: ${res.receivedBy}`
    receiveDialog.value = false
    resultDialog.value = true
    await load()
  } catch (e: any) {
    resultText.value = '❌ 入荷処理失敗:\n' + (e?.response?.data?.message || e?.response?.data?.error || e?.message || String(e))
    resultDialog.value = true
  } finally {
    busy.value = false
  }
}


async function openSchedule(p: PurchaseOrder) {
  if (!p.id) return
  schedulePO.value = p
  scheduleHistory.value = await PurchaseOrdersApi.supplierSchedule(p.id)
  scheduleForm.value = {
    eventType: p.scheduleStatus === 'UNCONFIRMED' ? 'CONFIRM' : 'REVISE',
    quantity: remaining(p),
    confirmedDeliveryDate: p.confirmedDeliveryDate?.slice(0, 10) || p.dueDate?.slice(0, 10) || '',
    asnNo: '', expectedArrivalDate: p.asnExpectedArrivalDate?.slice(0, 10) || '', supplierReference: '', notes: ''
  }
  scheduleDialog.value = true
}

async function saveScheduleEvent() {
  if (!schedulePO.value?.id) return
  busy.value = true
  try {
    const f = scheduleForm.value
    await PurchaseOrdersApi.addSupplierScheduleEvent(schedulePO.value.id, {
      eventType: f.eventType,
      quantity: f.eventType === 'CANCEL' ? undefined : Number(f.quantity),
      confirmedDeliveryDate: ['CONFIRM','REVISE'].includes(f.eventType) ? f.confirmedDeliveryDate : undefined,
      asnNo: f.eventType === 'ASN' ? f.asnNo : undefined,
      expectedArrivalDate: f.eventType === 'ASN' ? f.expectedArrivalDate : undefined,
      supplierReference: f.supplierReference,
      notes: f.notes
    })
    scheduleHistory.value = await PurchaseOrdersApi.supplierSchedule(schedulePO.value.id)
    await load()
    schedulePO.value = pos.value.find(x => x.id === schedulePO.value?.id) || schedulePO.value
  } catch (e: any) {
    resultText.value = '❌ Supplier Schedule更新失敗:\n' + (e?.response?.data?.message || e?.response?.data?.error || e?.message || String(e))
    resultDialog.value = true
  } finally {
    busy.value = false
  }
}

function scheduleColor(s?: PurchaseOrder['scheduleStatus']) {
  if (s === 'ASN') return 'success'
  if (s === 'CONFIRMED') return 'primary'
  return 'warning'
}

async function openHistory(p: PurchaseOrder) {
  if (!p.id) return
  historyPO.value = p
  receiptHistory.value = await PurchaseOrdersApi.receipts(p.id)
  historyDialog.value = true
}

function statusColor(s: string) {
  const colors: Record<string, string> = {
    OPEN: 'warning', PARTIALLY_RECEIVED: 'info', RECEIVED: 'success', CLOSED: 'grey'
  }
  return colors[s] || 'grey'
}
function fmt(d?: string) { return d ? new Date(d).toLocaleDateString('ja-JP') : '' }
function fmtDateTime(d?: string) { return d ? new Date(d).toLocaleString('ja-JP') : '' }
</script>
