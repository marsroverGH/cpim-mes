<template>
  <div>
  <v-card>
    <v-card-title class="d-flex align-center">
      作業区マスタ (Work Centers)
      <v-spacer />
      <v-btn color="primary" prepend-icon="mdi-plus" @click="openNew">新規</v-btn>
    </v-card-title>
    <v-data-table :items="rows" :headers="headers" density="comfortable">
      <template #item.effective="{ item }">
        {{ effectiveMin(item) }} 分/日
      </template>
      <template #item.shiftStartMinute="{ item }">
        {{ shiftTime(item.shiftStartMinute ?? 480) }}
      </template>
      <template #item.actions="{ item }">
        <v-btn icon="mdi-table-cog" size="small" variant="text" title="段取りマトリクス" @click="openMatrix(item)" />
        <v-btn icon="mdi-pencil" size="small" variant="text" @click="openEdit(item)" />
        <v-btn icon="mdi-delete" size="small" variant="text" color="error" @click="remove(item)" />
      </template>
    </v-data-table>
  </v-card>

  <v-dialog v-model="dialog" max-width="600">
    <v-card>
      <v-card-title>{{ form.id ? '作業区編集' : '新規作業区' }}</v-card-title>
      <v-card-text>
        <v-row dense>
          <v-col cols="6"><v-text-field v-model="form.code" label="コード" /></v-col>
          <v-col cols="6"><v-text-field v-model="form.name" label="名称" /></v-col>
          <v-col cols="6">
            <v-text-field v-model.number="form.capacityMinutesPerDay" type="number"
                          label="1設備あたり日次稼働 (分)" hint="例: 480 = 1設備8時間。総設備能力は設備台数を乗算" persistent-hint />
          </v-col>
          <v-col cols="6">
            <v-text-field v-model.number="form.laborRatePerMinute" type="number"
                          label="労務費 (¥/分)" />
          </v-col>
          <v-col cols="6">
            <v-text-field v-model.number="form.overheadRatePerMinute" type="number"
                          label="間接費 (¥/分)" hint="製造間接費の配賦レート" persistent-hint />
          </v-col>
          <v-col cols="6">
            <v-text-field v-model.number="form.efficiency" type="number" step="0.01"
                          label="効率 (0-1)" hint="標準時間に対する達成率" persistent-hint />
          </v-col>
          <v-col cols="6">
            <v-text-field v-model.number="form.utilization" type="number" step="0.01"
                          label="稼働率 (0-1)" hint="設備稼働率" persistent-hint />
          </v-col>
          <v-col cols="4">
            <v-text-field v-model.number="form.shiftStartMinute" type="number" min="0" max="1439"
                          label="シフト開始 (0時からの分)" hint="例: 480 = 08:00" persistent-hint />
          </v-col>
          <v-col cols="4"><v-text-field v-model.number="form.machineCount" type="number" min="1" label="設備台数" /></v-col>
          <v-col cols="4"><v-text-field v-model.number="form.workerCount" type="number" min="0" label="作業者数" /></v-col>
          <v-col cols="12">
            <v-select v-model="form.calendarId"
                      :items="calendarOptions" item-title="label" item-value="id"
                      label="作業カレンダー"
                      hint="未選択なら既定カレンダーを使用" persistent-hint clearable />
          </v-col>
        </v-row>
        <v-alert v-if="form.capacityMinutesPerDay && form.efficiency && form.utilization"
                 type="info" density="compact" variant="tonal" class="mt-2">
          総実効設備能力 ≒ {{ effectiveFromForm() }} 設備分/日
        </v-alert>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="dialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="save">保存</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <v-dialog v-model="matrixDialog" max-width="760">
    <v-card :title="`段取りマトリクス – ${matrixWc?.code ?? ''}`">
      <v-card-text>
        <v-row dense>
          <v-col cols="4"><v-text-field v-model="matrixForm.fromSetupFamily" label="From Family" hint="*=任意" /></v-col>
          <v-col cols="4"><v-text-field v-model="matrixForm.toSetupFamily" label="To Family" /></v-col>
          <v-col cols="3"><v-text-field v-model.number="matrixForm.setupMinutes" type="number" min="0" label="段取(分)" /></v-col>
          <v-col cols="1" class="d-flex align-center"><v-btn icon="mdi-plus" color="primary" @click="saveMatrix" /></v-col>
        </v-row>
        <v-data-table :items="matrixRows" :headers="matrixHeaders" density="compact">
          <template #item.actions="{ item }"><v-btn icon="mdi-delete" size="small" variant="text" color="error" @click="removeMatrix(item)" /></template>
        </v-data-table>
      </v-card-text>
      <v-card-actions><v-spacer/><v-btn @click="matrixDialog=false">閉じる</v-btn></v-card-actions>
    </v-card>
  </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CalendarApi, WorkCentersApi,
         type WorkCalendar, type WorkCenter, type WorkCenterSetupMatrixRow } from '@/api'

const rows = ref<WorkCenter[]>([])
const calendars = ref<WorkCalendar[]>([])
const dialog = ref(false)
const form = ref<WorkCenter>(blank())
const matrixDialog = ref(false)
const matrixWc = ref<WorkCenter | null>(null)
const matrixRows = ref<WorkCenterSetupMatrixRow[]>([])
const matrixForm = ref({ fromSetupFamily: '*', toSetupFamily: '', setupMinutes: 0 })

const calendarOptions = computed(() =>
  (calendars.value ?? []).map(c => ({ id: c.id, label: `${c.code} – ${c.name}${c.isDefault ? ' (既定)' : ''}` })))

const headers = [
  { title: 'コード',         key: 'code' },
  { title: '名称',           key: 'name' },
  { title: '1設備分/日',      key: 'capacityMinutesPerDay',   align: 'end' as const },
  { title: '効率',           key: 'efficiency',              align: 'end' as const },
  { title: '稼働率',         key: 'utilization',             align: 'end' as const },
  { title: '労務費(¥/分)',   key: 'laborRatePerMinute',      align: 'end' as const },
  { title: '間接費(¥/分)',   key: 'overheadRatePerMinute',   align: 'end' as const },
  { title: '開始時刻',       key: 'shiftStartMinute',        align: 'end' as const },
  { title: '設備',           key: 'machineCount',            align: 'end' as const },
  { title: '作業者',         key: 'workerCount',             align: 'end' as const },
  { title: '総実効設備分',   key: 'effective',               align: 'end' as const, sortable: false },
  { title: '',               key: 'actions',                 sortable: false, align: 'end' as const }
]

function blank(): WorkCenter {
  return { code: '', name: '', capacityMinutesPerDay: 480,
           efficiency: 1, utilization: 0.85, laborRatePerMinute: 50,
           overheadRatePerMinute: 30, shiftStartMinute: 480, machineCount: 1, workerCount: 1 }
}
function effectiveMin(w: WorkCenter) {
  return Math.round(w.capacityMinutesPerDay * Math.max(w.machineCount ?? 1, 1) * w.efficiency * w.utilization)
}
function effectiveFromForm() {
  return Math.round(form.value.capacityMinutesPerDay * Math.max(form.value.machineCount ?? 1, 1) * form.value.efficiency * form.value.utilization)
}
function shiftTime(min: number) {
  const h = Math.floor(min / 60) % 24
  const m = min % 60
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`
}

async function load() {
  const [_p0, _p1] = await Promise.all([WorkCentersApi.list(), CalendarApi.list()])
  rows.value = _p0 ?? []
  calendars.value = _p1 ?? []
}
onMounted(load)

function openNew() { form.value = blank(); dialog.value = true }
function openEdit(w: WorkCenter) { form.value = { ...w }; dialog.value = true }

async function save() {
  if (form.value.id) await WorkCentersApi.update(form.value.id, form.value)
  else                await WorkCentersApi.create(form.value)
  dialog.value = false
  await load()
}
const matrixHeaders = [
  { title: 'From', key: 'fromSetupFamily' }, { title: 'To', key: 'toSetupFamily' },
  { title: '段取(分)', key: 'setupMinutes', align: 'end' as const }, { title: '', key: 'actions', sortable: false }
]
async function openMatrix(w: WorkCenter) {
  if (!w.id) return
  matrixWc.value = w; matrixRows.value = (await WorkCentersApi.setupMatrix(w.id)) ?? []
  matrixForm.value = { fromSetupFamily: '*', toSetupFamily: '', setupMinutes: 0 }; matrixDialog.value = true
}
async function saveMatrix() {
  if (!matrixWc.value?.id || !matrixForm.value.toSetupFamily) return
  await WorkCentersApi.upsertSetupMatrix(matrixWc.value.id, matrixForm.value)
  matrixRows.value = (await WorkCentersApi.setupMatrix(matrixWc.value.id)) ?? []
}
async function removeMatrix(x: WorkCenterSetupMatrixRow) {
  if (!x.id || !matrixWc.value?.id) return
  await WorkCentersApi.removeSetupMatrix(x.id); matrixRows.value = (await WorkCentersApi.setupMatrix(matrixWc.value.id)) ?? []
}

async function remove(w: WorkCenter) {
  if (!w.id || !confirm(`${w.code} を削除します。よろしいですか？`)) return
  await WorkCentersApi.remove(w.id)
  await load()
}
</script>
