<template>
  <div>
  <v-card>
    <v-card-title class="d-flex align-center">
      Engineering Change Orders (ECO/ECN)
      <v-spacer />
      <v-btn variant="text" prepend-icon="mdi-plus" @click="openNew">ECO作成</v-btn>
    </v-card-title>
    <v-card-text>
      <p class="text-body-2 text-medium-emphasis">
        BOM 変更要求 (ECO) のライフサイクル: <strong>DRAFT → APPROVED → APPLIED</strong>。
        承認後は内容が固定され、有効日より前の適用は Backend / DB の両方で拒否されます。
      </p>
      <v-data-table :items="ecos" :headers="headers" density="comfortable">
        <template #item.effectiveDate="{ item }">{{ fmt(item.effectiveDate) }}</template>
        <template #item.status="{ item }">
          <v-chip size="small" :color="statusColor(item.status)">{{ item.status }}</v-chip>
        </template>
        <template #item.actions="{ item }">
          <v-btn size="small" variant="text" @click="openDetail(item)">詳細</v-btn>
          <v-btn v-if="item.status === 'DRAFT'" size="small" variant="tonal" color="primary"
                 class="mr-1" @click="approve(item)">承認</v-btn>
          <v-btn v-if="item.status === 'APPROVED'" size="small" variant="tonal" color="success"
                 class="mr-1" :disabled="!isEffective(item)" @click="apply(item)">適用</v-btn>
          <v-btn v-if="item.status !== 'APPLIED' && item.status !== 'CANCELLED'"
                 size="small" variant="text" color="error" @click="cancel(item)">取消</v-btn>
        </template>
      </v-data-table>
    </v-card-text>
  </v-card>

  <!-- New ECO dialog -->
  <v-dialog v-model="dialog" max-width="540">
    <v-card title="新規 ECO">
      <v-card-text>
        <v-text-field v-model="form.ecoNo" label="ECO番号" />
        <v-text-field v-model="form.title" label="タイトル" />
        <v-textarea v-model="form.description" label="説明" rows="2" />
        <v-text-field v-model="form.effectiveDate" type="date" label="有効日" />
        <div class="text-caption text-medium-emphasis">申請者はログインユーザーから自動記録されます。</div>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="dialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="create">作成</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- Detail / components dialog -->
  <v-dialog v-model="detailDialog" max-width="720">
    <v-card v-if="active" :title="`ECO ${active.ecoNo} — ${active.title}`">
      <v-card-text>
        <v-alert v-if="active.status === 'APPROVED' && !isEffective(active)" type="warning" variant="tonal" class="mb-3">
          有効日 {{ fmt(active.effectiveDate) }} までは適用できません。
        </v-alert>
        <v-row dense class="mb-2">
          <v-col cols="6"><strong>申請:</strong> {{ active.requestedBy || '-' }}</v-col>
          <v-col cols="6"><strong>有効日:</strong> {{ fmt(active.effectiveDate) }}</v-col>
          <v-col cols="6"><strong>承認:</strong> {{ active.approvedBy || '-' }} <span v-if="active.approvedAt">({{ fmtDateTime(active.approvedAt) }})</span></v-col>
          <v-col cols="6"><strong>適用:</strong> {{ active.appliedBy || '-' }} <span v-if="active.appliedAt">({{ fmtDateTime(active.appliedAt) }})</span></v-col>
        </v-row>
        <v-data-table :items="components" :headers="compHeaders" density="compact" :items-per-page="-1">
          <template #item.parentId="{ item }">{{ codeMap[item.parentId] || item.parentId.slice(0,8) }}</template>
          <template #item.childId="{ item }">{{ codeMap[item.childId] || item.childId.slice(0,8) }}</template>
          <template #item.action="{ item }">
            <v-chip size="x-small" :color="actionColor(item.action)">{{ item.action }}</v-chip>
          </template>
        </v-data-table>

        <template v-if="active.status === 'DRAFT'">
        <v-divider class="my-3" />
        <div class="text-subtitle-2 mb-2">変更行を追加</div>
        <v-row dense>
          <v-col cols="3">
            <v-select v-model="compForm.action"
                      :items="['ADD','REMOVE','MODIFY']" label="操作" />
          </v-col>
          <v-col cols="4">
            <v-select v-model="compForm.parentId" :items="itemOptions"
                      item-title="label" item-value="id" label="親品目" />
          </v-col>
          <v-col cols="4">
            <v-select v-model="compForm.childId" :items="itemOptions"
                      item-title="label" item-value="id" label="子品目" />
          </v-col>
          <v-col cols="2">
            <v-text-field v-model.number="compForm.newQuantity" type="number" label="数量" />
          </v-col>
        </v-row>
        <v-btn size="small" prepend-icon="mdi-plus" @click="addComp">行追加</v-btn>
        </template>
        <v-divider class="my-3" />
        <div class="text-subtitle-2 mb-2">ステータス履歴</div>
        <v-table density="compact">
          <thead><tr><th>遷移</th><th>実行者</th><th>日時</th><th>有効日</th></tr></thead>
          <tbody><tr v-for="h in history" :key="h.id">
            <td>{{ h.fromStatus || 'NEW' }} → {{ h.toStatus }}</td>
            <td>{{ h.actorUsername || '-' }}</td>
            <td>{{ fmtDateTime(h.occurredAt) }}</td>
            <td>{{ fmt(h.effectiveDateSnapshot) }}</td>
          </tr></tbody>
        </v-table>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="detailDialog = false">閉じる</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ECOApi, ItemsApi,
         type ECOComponent, type ECOStatusHistory, type EngineeringChange, type Item } from '@/api'

const ecos = ref<EngineeringChange[]>([])
const items = ref<Item[]>([])
const dialog = ref(false)
const detailDialog = ref(false)
const active = ref<EngineeringChange | null>(null)
const components = ref<ECOComponent[]>([])
const history = ref<ECOStatusHistory[]>([])

const form = ref<EngineeringChange>(blank())
const compForm = ref<ECOComponent>({
  ecoId: '', action: 'ADD', parentId: '', childId: '',
  newQuantity: 1, newScrapPct: 0, notes: ''
})

function blank(): EngineeringChange {
  return {
    ecoNo: '', title: '', description: '',
    status: 'DRAFT', effectiveDate: new Date().toISOString().slice(0, 10),
    requestedBy: '', approvedBy: ''
  }
}

const codeMap = computed(() => {
  const m: Record<string, string> = {}
  for (const i of (items.value ?? [])) if (i.id) m[i.id] = i.code
  return m
})
const itemOptions = computed(() =>
  (items.value ?? []).map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))

const headers = [
  { title: 'ECO番号',  key: 'ecoNo' },
  { title: 'タイトル', key: 'title' },
  { title: '有効日',   key: 'effectiveDate' },
  { title: 'ステータス', key: 'status' },
  { title: '申請',     key: 'requestedBy' },
  { title: '',         key: 'actions', sortable: false, align: 'end' as const }
]
const compHeaders = [
  { title: '操作',     key: 'action' },
  { title: '親',       key: 'parentId' },
  { title: '子',       key: 'childId' },
  { title: '新数量',   key: 'newQuantity', align: 'end' as const },
  { title: '備考',     key: 'notes' }
]

async function load() {
  const [_p0, _p1] = await Promise.all([ECOApi.list(), ItemsApi.list()])
  ecos.value = _p0 ?? []
  items.value = _p1 ?? []
}
onMounted(load)

async function openDetail(e: EngineeringChange) {
  active.value = e
  if (e.id) {
    const [cs, hs] = await Promise.all([ECOApi.components(e.id), ECOApi.history(e.id)])
    components.value = cs ?? []
    history.value = hs ?? []
    compForm.value = { ecoId: e.id, action: 'ADD', parentId: '', childId: '',
                        newQuantity: 1, newScrapPct: 0, notes: '' }
  }
  detailDialog.value = true
}

async function addComp() {
  if (!active.value?.id) return
  await ECOApi.addComponent(active.value.id, compForm.value)
  components.value = (await ECOApi.components(active.value.id)) ?? []
}

function openNew() { form.value = blank(); dialog.value = true }

async function create() {
  await ECOApi.create({ ...form.value, ecoNo: form.value.ecoNo || `ECO-${Date.now()}` })
  dialog.value = false
  await load()
}

async function approve(e: EngineeringChange) {
  if (!e.id) return
  if (!confirm(`ECO ${e.ecoNo} を承認しますか？承認後は内容を変更できません。`)) return
  await ECOApi.approve(e.id)
  await load()
}

async function apply(e: EngineeringChange) {
  if (!e.id) return
  if (!confirm(`ECO ${e.ecoNo} を BOM に適用しますか？取り消し不可です。`)) return
  try {
    await ECOApi.apply(e.id)
    alert('適用しました')
  } catch (err: any) {
    alert('失敗: ' + (err?.response?.data?.message || err?.message))
  }
  await load()
}

async function cancel(e: EngineeringChange) {
  if (!e.id) return
  if (!confirm('取消しますか？')) return
  await ECOApi.cancel(e.id)
  await load()
}

function statusColor(s: string) {
  const colors: Record<string, string> = { DRAFT: 'grey', APPROVED: 'primary', APPLIED: 'success', CANCELLED: 'error' }
  return colors[s] || 'grey'
}
function actionColor(a: string) {
  const colors: Record<string, string> = { ADD: 'success', REMOVE: 'error', MODIFY: 'primary' }
  return colors[a] || 'grey'
}
function dateKey(d: string) { return String(d).slice(0, 10) }
function todayKey() {
  const f = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Tokyo', year: 'numeric', month: '2-digit', day: '2-digit' })
  const parts = Object.fromEntries(f.formatToParts(new Date()).map(p => [p.type, p.value]))
  return `${parts.year}-${parts.month}-${parts.day}`
}
function isEffective(e: EngineeringChange) { return dateKey(e.effectiveDate) <= todayKey() }
function fmt(d: string) { return new Date(d).toLocaleDateString('ja-JP') }
function fmtDateTime(d: string) { return new Date(d).toLocaleString('ja-JP') }
</script>
