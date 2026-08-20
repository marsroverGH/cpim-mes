<template>
  <div>
  <v-card>
    <v-card-title class="d-flex align-center flex-wrap" style="gap: 8px">
      サイクルカウント (Cycle Count)
      <v-spacer />
      <v-btn variant="text" prepend-icon="mdi-refresh" @click="load">更新</v-btn>
      <v-btn color="primary" prepend-icon="mdi-calendar-plus" :loading="busy" @click="generate">
        スケジュール生成
      </v-btn>
    </v-card-title>
    <v-card-text>
      <p class="text-body-2 text-medium-emphasis">
        年間使用金額ベースのABC分析と連動し、A=週次・B=月次・C=四半期の頻度で棚卸を計画します。
        実測値を入力すると、差異がある場合は自動で在庫調整トランザクションが起票されます。
      </p>

      <v-row dense class="mt-2">
        <v-col v-for="s in summary" :key="s.label" cols="12" sm="3">
          <v-card variant="tonal" :color="s.color">
            <v-card-text>
              <div class="text-caption">{{ s.label }}</div>
              <div class="text-h6">{{ s.count }}</div>
            </v-card-text>
          </v-card>
        </v-col>
      </v-row>

      <v-tabs v-model="filterStatus" class="mt-3">
        <v-tab value="">全件</v-tab>
        <v-tab value="PENDING">未実施</v-tab>
        <v-tab value="COUNTED">計上済 (差異未調整)</v-tab>
        <v-tab value="RECONCILED">調整済</v-tab>
      </v-tabs>

      <v-data-table :items="filteredRows" :headers="headers" density="compact" :items-per-page="50">
        <template #item.scheduledDate="{ item }">{{ fmt(item.scheduledDate) }}</template>
        <template #item.countedDate="{ item }">{{ fmt(item.countedDate) }}</template>
        <template #item.abcClass="{ item }">
          <v-chip size="x-small" :color="abcColor(item.abcClass)">{{ item.abcClass }}</v-chip>
        </template>
        <template #item.status="{ item }">
          <v-chip size="small" :color="statusColor(item.status)">{{ item.status }}</v-chip>
        </template>
        <template #item.variance="{ item }">
          <span v-if="item.variance !== undefined && item.variance !== null"
                :class="(item.variance ?? 0) === 0 ? 'text-success' :
                        (item.variance ?? 0) > 0 ? 'text-info' : 'text-error'">
            {{ item.variance > 0 ? '+' : '' }}{{ item.variance }}
          </span>
          <span v-else>—</span>
        </template>
        <template #item.actions="{ item }">
          <v-btn v-if="item.status === 'PENDING'"
                 size="small" variant="tonal" prepend-icon="mdi-counter"
                 @click="openRecord(item)">実測入力</v-btn>
        </template>
      </v-data-table>
    </v-card-text>
  </v-card>

  <!-- Record dialog -->
  <v-dialog v-model="dialog" max-width="500">
    <v-card title="実測入力">
      <v-card-text v-if="active">
        <p class="text-body-2 text-medium-emphasis mb-2">
          {{ active.itemCode }} – {{ active.itemName }}<br />
          帳簿在庫 (期待値): <strong>{{ active.expectedQty }}</strong>
        </p>
        <v-text-field v-model.number="recordForm.countedQty" type="number"
                      label="実測数量" autofocus />
        <v-textarea v-model="recordForm.notes" label="備考" rows="2" />
        <v-alert v-if="diff !== 0" type="warning" variant="tonal" density="compact">
          差異 <strong>{{ diff > 0 ? '+' : '' }}{{ diff }}</strong> が発生します。
          保存時に在庫調整トランザクション (ADJUST) が自動起票されます。
        </v-alert>
        <v-alert v-else type="success" variant="tonal" density="compact">
          差異なし — 帳簿と実測が一致しています。
        </v-alert>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="dialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="saveRecord">保存</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CycleCountApi, type CycleCount } from '@/api'

const rows = ref<CycleCount[]>([])
const filterStatus = ref<string>('')
const busy = ref(false)

const dialog = ref(false)
const active = ref<CycleCount | null>(null)
const recordForm = ref({ countedQty: 0, notes: '' })

const headers = [
  { title: '予定日',   key: 'scheduledDate' },
  { title: 'クラス',   key: 'abcClass' },
  { title: 'コード',   key: 'itemCode' },
  { title: '品名',     key: 'itemName' },
  { title: '帳簿',     key: 'expectedQty', align: 'end' as const },
  { title: '実測',     key: 'countedQty',  align: 'end' as const },
  { title: '差異',     key: 'variance',    align: 'end' as const },
  { title: '実施日',   key: 'countedDate' },
  { title: 'ステータス', key: 'status' },
  { title: '',         key: 'actions',     sortable: false, align: 'end' as const }
]

async function load() {
  rows.value = (await CycleCountApi.list()) ?? []
}
onMounted(load)

const filteredRows = computed(() =>
  filterStatus.value ? (rows.value ?? []).filter(r => r.status === filterStatus.value) : (rows.value ?? [])
)

const summary = computed(() => [
  { label: '総件数',   count: (rows.value ?? []).length, color: 'primary' },
  { label: '未実施',   count: (rows.value ?? []).filter(r => r.status === 'PENDING').length, color: 'warning' },
  { label: '計上済',   count: (rows.value ?? []).filter(r => r.status === 'COUNTED').length, color: 'info' },
  { label: '調整済',   count: (rows.value ?? []).filter(r => r.status === 'RECONCILED').length, color: 'success' }
])

async function generate() {
  busy.value = true
  try {
    const r = await CycleCountApi.generate()
    if (r.created === 0) {
      alert('新規スケジュールはありませんでした (既存の予定が有効です)')
    }
    await load()
  } finally {
    busy.value = false
  }
}

function openRecord(c: CycleCount) {
  active.value = c
  recordForm.value = { countedQty: c.expectedQty ?? 0, notes: '' }
  dialog.value = true
}

const diff = computed(() => {
  if (!active.value) return 0
  return recordForm.value.countedQty - (active.value.expectedQty ?? 0)
})

async function saveRecord() {
  if (!active.value) return
  await CycleCountApi.record(active.value.id, recordForm.value.countedQty, recordForm.value.notes)
  dialog.value = false
  await load()
}

function abcColor(c: string) {
  const colors: Record<string, string> = { A: 'error', B: 'warning', C: 'success' }
  return colors[c] || 'grey'
}
function statusColor(s: string) {
  const colors: Record<string, string> = { PENDING: 'warning', COUNTED: 'info', RECONCILED: 'success' }
  return colors[s] || 'grey'
}
function fmt(d?: string) { return d ? new Date(d).toLocaleDateString('ja-JP') : '' }
</script>
