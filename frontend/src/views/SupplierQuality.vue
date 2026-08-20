<template>
  <div>
    <v-row class="mb-2">
      <v-col cols="12">
        <v-card>
          <v-card-title class="d-flex align-center">
            Supplier Quality / NCR
            <v-spacer />
            <v-btn variant="text" prepend-icon="mdi-refresh" @click="loadAll">更新</v-btn>
            <v-btn v-if="auth.role !== 'viewer'" color="primary" prepend-icon="mdi-alert-plus" @click="openNcrCreate">NCR起票</v-btn>
          </v-card-title>
          <v-card-text>
            <v-alert type="info" variant="tonal" density="compact">
              Supplier由来LotのFAIL検査ではNCRが自動起票されます。Inspection RequiredのSupplier受入LotはHOLDとなり、合格検査までは通常出庫できません。
            </v-alert>
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>

    <v-tabs v-model="tab" color="primary">
      <v-tab value="scorecard">Scorecard</v-tab>
      <v-tab value="ncr">NCR</v-tab>
      <v-tab value="profiles">Supplier設定</v-tab>
    </v-tabs>

    <v-window v-model="tab">
      <v-window-item value="scorecard">
        <v-card class="mt-2">
          <v-card-title>Supplier Quality Scorecard</v-card-title>
          <v-data-table :items="scorecards" :headers="scoreHeaders" density="compact">
            <template #item.profileStatus="{ item }">
              <v-chip size="x-small" :color="supplierStatusColor(item.profileStatus)">{{ item.profileStatus }}</v-chip>
            </template>
            <template #item.defectPpm="{ item }">
              <span :class="item.targetPpm > 0 && item.defectPpm > item.targetPpm ? 'text-error font-weight-bold' : ''">
                {{ fmtNum(item.defectPpm, 0) }}
              </span>
            </template>
            <template #item.inspectionRequired="{ item }">{{ item.inspectionRequired ? '必須' : '任意' }}</template>
          </v-data-table>
        </v-card>
      </v-window-item>

      <v-window-item value="ncr">
        <v-card class="mt-2">
          <v-card-title class="d-flex align-center">
            NCR一覧
            <v-spacer />
            <v-select v-model="ncrStatusFilter" :items="['','OPEN','IN_REWORK','CLOSED','CANCELLED']"
                      label="Status" density="compact" hide-details style="max-width:180px" @update:model-value="loadNcrs" />
          </v-card-title>
          <v-data-table :items="ncrs" :headers="ncrHeaders" density="compact" @click:row="selectNcr">
            <template #item.status="{ item }"><v-chip size="x-small" :color="ncrStatusColor(item.status)">{{ item.status }}</v-chip></template>
            <template #item.severity="{ item }"><v-chip size="x-small" :color="severityColor(item.severity)">{{ item.severity }}</v-chip></template>
            <template #item.createdAt="{ item }">{{ fmtDate(item.createdAt) }}</template>
            <template #item.actions="{ item }">
              <v-btn v-if="canDisposition && item.status === 'OPEN'" size="x-small" variant="tonal" @click.stop="openDisposition(item)">Disposition</v-btn>
              <v-btn v-if="canDisposition && item.status === 'IN_REWORK'" size="x-small" variant="tonal" color="success" @click.stop="closeRework(item)">Rework完了</v-btn>
            </template>
          </v-data-table>
        </v-card>
      </v-window-item>

      <v-window-item value="profiles">
        <v-card class="mt-2">
          <v-card-title class="d-flex align-center">Supplier Qualification
            <v-spacer />
            <v-btn v-if="auth.canEdit" size="small" prepend-icon="mdi-plus" variant="tonal" @click="openNewProfile">Supplier設定追加</v-btn>
          </v-card-title>
          <v-data-table :items="profiles" :headers="profileHeaders" density="compact">
            <template #item.status="{ item }"><v-chip size="x-small" :color="supplierStatusColor(item.status)">{{ item.status }}</v-chip></template>
            <template #item.inspectionRequired="{ item }">{{ item.inspectionRequired ? '必須' : '任意' }}</template>
            <template #item.actions="{ item }">
              <v-btn v-if="auth.canEdit" size="x-small" variant="text" icon="mdi-pencil" @click="editProfile(item)" />
            </template>
          </v-data-table>
        </v-card>
      </v-window-item>
    </v-window>

    <v-dialog v-model="ncrDialog" max-width="620">
      <v-card title="Supplier NCR 起票">
        <v-card-text>
          <v-select v-model="ncrForm.lotId" :items="supplierLotOptions" item-title="label" item-value="id" label="Supplier Lot" />
          <v-text-field v-model.number="ncrForm.affectedQty" type="number" min="0" label="影響数量" />
          <v-select v-model="ncrForm.severity" :items="['MINOR','MAJOR','CRITICAL']" label="Severity" />
          <v-textarea v-model="ncrForm.description" label="不適合内容" rows="3" />
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn @click="ncrDialog=false">キャンセル</v-btn><v-btn color="primary" @click="createNcr">起票</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="dispositionDialog" max-width="620">
      <v-card :title="`Disposition - ${selectedNcr?.ncrNo || ''}`">
        <v-card-text>
          <v-select v-model="dispositionForm.disposition" :items="dispositionOptions" label="Disposition" />
          <v-text-field v-model.number="dispositionForm.quantity" type="number" min="0" :max="selectedNcr?.affectedQty || undefined" label="数量" />
          <v-alert v-if="dispositionForm.disposition==='USE_AS_IS'" type="warning" variant="tonal" density="compact" class="mb-2">
            USE_AS_IS（特採）はadminのみ実行可能です。LotはOKへ戻ります。
          </v-alert>
          <v-textarea v-model="dispositionForm.notes" label="判断理由 / 備考" rows="3" />
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn @click="dispositionDialog=false">キャンセル</v-btn><v-btn color="primary" @click="applyDisposition">確定</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="profileDialog" max-width="560">
      <v-card title="Supplier Quality設定">
        <v-card-text>
          <v-text-field v-model="profileForm.supplierName" label="Supplier" :disabled="!!editingSupplier" />
          <v-select v-model="profileForm.status" :items="['APPROVED','CONDITIONAL','BLOCKED']" label="Qualification Status" />
          <v-switch v-model="profileForm.inspectionRequired" label="Incoming Inspection Required" color="primary" />
          <v-text-field v-model.number="profileForm.targetPpm" type="number" min="0" label="Target PPM" />
          <v-textarea v-model="profileForm.notes" label="Notes" rows="3" />
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn @click="profileDialog=false">キャンセル</v-btn><v-btn color="primary" @click="saveProfile">保存</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="historyDialog" max-width="720">
      <v-card :title="`NCR履歴 - ${selectedNcr?.ncrNo || ''}`">
        <v-card-text>
          <v-list density="compact" lines="two">
            <v-list-item v-for="h in history" :key="h.id">
              <v-list-item-title>{{ h.eventType }}: {{ h.fromStatus || '—' }} → {{ h.toStatus }}</v-list-item-title>
              <v-list-item-subtitle>{{ fmtDate(h.occurredAt) }} / {{ h.actor || 'system' }} / {{ h.notes || '' }}</v-list-item-subtitle>
            </v-list-item>
          </v-list>
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn @click="historyDialog=false">閉じる</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snack.show" :color="snack.color">{{ snack.text }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { LotsApi, SupplierQualityApi, type Lot, type SupplierNCR, type SupplierNCRHistory,
  type SupplierQualityProfile, type SupplierQualityScorecard, type NCRDisposition, type NCRSeverity, type SupplierQualityStatus } from '@/api'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const tab = ref('scorecard')
const scorecards = ref<SupplierQualityScorecard[]>([])
const profiles = ref<SupplierQualityProfile[]>([])
const ncrs = ref<SupplierNCR[]>([])
const lots = ref<Lot[]>([])
const history = ref<SupplierNCRHistory[]>([])
const ncrStatusFilter = ref('')
const selectedNcr = ref<SupplierNCR | null>(null)
const ncrDialog = ref(false)
const dispositionDialog = ref(false)
const profileDialog = ref(false)
const historyDialog = ref(false)
const editingSupplier = ref('')
const snack = ref({ show: false, text: '', color: 'success' })

const ncrForm = ref<{ lotId:string; affectedQty:number; severity:NCRSeverity; description:string }>({ lotId: '', affectedQty: 0, severity: 'MAJOR', description: '' })
const dispositionForm = ref<{ disposition: NCRDisposition; quantity: number; notes: string }>({ disposition: 'RETURN_TO_SUPPLIER', quantity: 0, notes: '' })
const profileForm = ref<{ supplierName:string; status:SupplierQualityStatus; inspectionRequired:boolean; targetPpm:number; notes:string }>({ supplierName: '', status: 'APPROVED', inspectionRequired: false, targetPpm: 0, notes: '' })

const canDisposition = computed(() => auth.role === 'planner' || auth.role === 'admin')
const dispositionOptions = computed(() => auth.isAdmin
  ? ['RETURN_TO_SUPPLIER','SCRAP','REWORK','USE_AS_IS']
  : ['RETURN_TO_SUPPLIER','SCRAP','REWORK'])
const supplierLotOptions = computed(() => lots.value.filter(l => !!l.supplier).map(l => ({ id: l.id!, label: `${l.supplier} / ${l.itemCode || l.itemId} / ${l.lotNo} / ${l.qualityStatus || 'OK'} / bal ${l.balance ?? l.quantity}` })))

const scoreHeaders = [
  { title:'Supplier', key:'supplier' }, { title:'Status', key:'profileStatus' }, { title:'検査', key:'inspectionRequired' },
  { title:'受入Qty', key:'receivedQty' }, { title:'Fail検査', key:'failInspectionCount' }, { title:'Defect Qty', key:'defectQty' },
  { title:'PPM', key:'defectPpm' }, { title:'Target PPM', key:'targetPpm' }, { title:'NCR', key:'ncrCount' },
  { title:'Open NCR', key:'openNcrCount' }, { title:'返品', key:'returnedQty' }, { title:'廃棄', key:'scrappedQty' }
]
const ncrHeaders = [
  { title:'NCR', key:'ncrNo' }, { title:'Supplier', key:'supplier' }, { title:'PO', key:'poNo' }, { title:'Item', key:'itemCode' },
  { title:'Lot', key:'lotNo' }, { title:'Affected', key:'affectedQty' }, { title:'Severity', key:'severity' }, { title:'Status', key:'status' },
  { title:'Disposition', key:'disposition' }, { title:'起票日', key:'createdAt' }, { title:'', key:'actions', sortable:false }
]
const profileHeaders = [
  { title:'Supplier', key:'supplierName' }, { title:'Status', key:'status' }, { title:'検査', key:'inspectionRequired' },
  { title:'Target PPM', key:'targetPpm' }, { title:'更新者', key:'updatedBy' }, { title:'更新日', key:'updatedAt' }, { title:'', key:'actions', sortable:false }
]

function notify(text: string, color = 'success') { snack.value = { show: true, text, color } }
function fmtDate(v?: string) { return v ? new Date(v).toLocaleString('ja-JP') : '—' }
function fmtNum(v: number, digits=2) { return Number(v || 0).toLocaleString('ja-JP', { maximumFractionDigits: digits }) }
function supplierStatusColor(s: string) { return s==='APPROVED' ? 'success' : s==='CONDITIONAL' ? 'warning' : 'error' }
function ncrStatusColor(s: string) { return s==='CLOSED' ? 'success' : s==='IN_REWORK' ? 'info' : s==='OPEN' ? 'warning' : 'grey' }
function severityColor(s: string) { return s==='CRITICAL' ? 'error' : s==='MAJOR' ? 'warning' : 'info' }

async function loadAll() {
  try {
    const [sc, pr, nr, ls] = await Promise.all([SupplierQualityApi.scorecard(), SupplierQualityApi.profiles(), SupplierQualityApi.ncrs(ncrStatusFilter.value), LotsApi.list()])
    scorecards.value = sc; profiles.value = pr; ncrs.value = nr; lots.value = ls
  } catch (e:any) { notify(e?.response?.data?.message || e.message || '読み込み失敗', 'error') }
}
async function loadNcrs() { ncrs.value = await SupplierQualityApi.ncrs(ncrStatusFilter.value) }
function openNcrCreate() { ncrForm.value = { lotId:'', affectedQty:0, severity:'MAJOR', description:'' }; ncrDialog.value=true }
async function createNcr() {
  try { await SupplierQualityApi.createNcr(ncrForm.value); ncrDialog.value=false; await loadAll(); notify('NCRを起票しました') }
  catch(e:any){ notify(e?.response?.data?.message || e.message || 'NCR起票失敗','error') }
}
function openDisposition(n: SupplierNCR) { selectedNcr.value=n; dispositionForm.value={ disposition:'RETURN_TO_SUPPLIER', quantity:n.affectedQty, notes:'' }; dispositionDialog.value=true }
async function applyDisposition() {
  if (!selectedNcr.value) return
  try { await SupplierQualityApi.disposition(selectedNcr.value.id, dispositionForm.value); dispositionDialog.value=false; await loadAll(); notify('Dispositionを確定しました') }
  catch(e:any){ notify(e?.response?.data?.message || e.message || 'Disposition失敗','error') }
}
async function closeRework(n: SupplierNCR) {
  try { await SupplierQualityApi.closeRework(n.id); await loadAll(); notify('REWORK NCRを閉じました') }
  catch(e:any){ notify(e?.response?.data?.message || e.message || 'NCR Close失敗','error') }
}
function openNewProfile() {
  editingSupplier.value=''
  profileForm.value={ supplierName:'', status:'APPROVED', inspectionRequired:false, targetPpm:0, notes:'' }
  profileDialog.value=true
}
function editProfile(p: SupplierQualityProfile) {
  editingSupplier.value=p.supplierName
  profileForm.value={ supplierName:p.supplierName, status:p.status, inspectionRequired:p.inspectionRequired, targetPpm:p.targetPpm, notes:p.notes }
  profileDialog.value=true
}
async function saveProfile() {
  try { await SupplierQualityApi.upsertProfile(profileForm.value); profileDialog.value=false; await loadAll(); notify('Supplier設定を保存しました') }
  catch(e:any){ notify(e?.response?.data?.message || e.message || 'Supplier設定保存失敗','error') }
}
async function selectNcr(_e:any, { item }:any) {
  selectedNcr.value=item
  try { history.value=await SupplierQualityApi.history(item.id); historyDialog.value=true } catch(e:any){ notify(e?.message || '履歴取得失敗','error') }
}

onMounted(loadAll)
</script>
