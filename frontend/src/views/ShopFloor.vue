<template>
  <div>
  <v-card>
    <v-card-title class="d-flex align-center">
      Shop Floor — 工程実績
      <v-spacer />
      <v-chip size="small" variant="tonal" prepend-icon="mdi-account-check" class="mr-2">
        担当者はログインユーザーを自動記録
      </v-chip>
      <v-btn prepend-icon="mdi-refresh" @click="load">再読込</v-btn>
    </v-card-title>
    <v-card-text>
      <p class="text-body-2 text-medium-emphasis">
        工程は PENDING → READY → IN_PROGRESS ↔ PAUSED → COMPLETED の順に進みます。
        通常工程は前工程完了で、Lot Streaming工程は前工程のTransfer Batch数量が完成すると次工程がREADYになります。
        後工程の累計良品数量は前工程の累計完成数量を超えられません。
      </p>

      <v-data-table :items="ops" :headers="headers" density="comfortable">
        <template #item.status="{ item }">
          <v-chip size="small" :color="statusColor(item.status)">{{ item.status }}</v-chip>
        </template>
        <template #item.elapsed="{ item }">
          <span v-if="item.activeStartedAt && item.status === 'IN_PROGRESS'">
            {{ elapsed(item.activeStartedAt) }} 分（稼働中）
          </span>
          <span v-else-if="item.actualMinutes">{{ Math.round(item.actualMinutes) }} 分</span>
          <span v-else>—</span>
        </template>
        <template #item.flow="{ item }">
          <v-chip v-if="item.overlapEnabled" size="x-small" color="primary">LOT STREAM {{ item.transferBatchQty }}</v-chip>
          <v-chip v-else size="x-small" variant="tonal">SERIAL</v-chip>
        </template>
        <template #item.actions="{ item }">
          <v-btn v-if="item.status === 'READY'" size="small" color="primary"
                 variant="tonal" prepend-icon="mdi-play" class="mr-1"
                 @click="start(item)">開始</v-btn>
          <v-btn v-else-if="item.status === 'PAUSED'" size="small" color="primary"
                 variant="tonal" prepend-icon="mdi-play" class="mr-1"
                 @click="start(item)">再開</v-btn>
          <template v-else-if="item.status === 'IN_PROGRESS'">
            <v-btn size="small" color="warning" variant="tonal" prepend-icon="mdi-pause"
                   class="mr-1" @click="stop(item)">中断</v-btn>
            <v-btn size="small" color="success" variant="tonal" prepend-icon="mdi-check"
                   @click="openComplete(item)">良品実績</v-btn>
          </template>
          <span v-else-if="item.status === 'PENDING'" class="text-caption text-medium-emphasis">
            前工程 / Transfer待ち
          </span>
        </template>
      </v-data-table>
    </v-card-text>
  </v-card>

  <v-dialog v-model="completeDialog" max-width="500">
    <v-card title="工程良品実績入力">
      <v-card-text v-if="active">
        <p class="text-body-2 text-medium-emphasis">
          {{ active.orderNo }} / {{ active.itemCode }} / 工程#{{ active.seqNo }}
          ({{ active.workCenterCode }})
        </p>
        <p class="mt-2">
          工程累計良品: <strong>{{ active.completedQty ?? 0 }}</strong> / {{ active.woQuantity }}
        </p>
        <v-text-field v-model.number="completeForm.completedQty" type="number"
                      :min="active.completedQty" :max="active.woQuantity"
                      label="工程累計良品完成数量"
                      hint="今回分ではなく、この工程で実際に完了した良品の累計数量を入力します。"
                      persistent-hint />
        <p class="text-caption mt-1">
          今回追加: {{ Math.max(0, (completeForm.completedQty ?? 0) - (active.completedQty ?? 0)) }}
        </p>
        <p class="text-caption text-medium-emphasis mt-2">
          実績時間はサーバー側で START から現在までを自動計測します。
        </p>
        <v-textarea v-model="completeForm.notes" label="備考" rows="2" />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="completeDialog = false">キャンセル</v-btn>
        <v-btn color="success" :loading="busy"
               :disabled="!active || completeForm.completedQty <= (active.completedQty ?? 0)"
               @click="doComplete">実績確定</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { ShopFloorApi, type WOOperationDetail } from '@/api'

const ops = ref<WOOperationDetail[]>([])
const busy = ref(false)
const completeDialog = ref(false)
const active = ref<WOOperationDetail | null>(null)
const completeForm = ref({ completedQty: 0, notes: '' })

const headers = [
  { title: 'WO',     key: 'orderNo' },
  { title: '品目',   key: 'itemCode' },
  { title: '工程',   key: 'seqNo', align: 'end' as const },
  { title: '作業区', key: 'workCenterCode' },
  { title: 'Flow',   key: 'flow' },
  { title: '最終担当', key: 'operator' },
  { title: 'ステータス', key: 'status' },
  { title: '経過/実績', key: 'elapsed' },
  { title: '',       key: 'actions', sortable: false, align: 'end' as const }
]

async function load() {
  ops.value = (await ShopFloorApi.active()) ?? []
}
onMounted(load)

let tick: any
onMounted(() => { tick = setInterval(() => { ops.value = [...(ops.value ?? [])] }, 5000) })
onUnmounted(() => clearInterval(tick))

async function start(o: WOOperationDetail) {
  await ShopFloorApi.start(o.id)
  await load()
}

async function stop(o: WOOperationDetail) {
  await ShopFloorApi.stop(o.id)
  await load()
}

function openComplete(o: WOOperationDetail) {
  active.value = o
  completeForm.value = {
    completedQty: o.completedQty ?? 0,
    notes: ''
  }
  completeDialog.value = true
}

async function doComplete() {
  if (!active.value) return
  busy.value = true
  try {
    await ShopFloorApi.complete(
      active.value.id,
      completeForm.value.completedQty,
      completeForm.value.notes
    )
    completeDialog.value = false
    await load()
  } finally { busy.value = false }
}

function statusColor(s: string) {
  const colors: Record<string, string> = {
    PENDING: 'grey', READY: 'info', IN_PROGRESS: 'warning', PAUSED: 'orange', COMPLETED: 'success'
  }
  return colors[s] || 'grey'
}
function elapsedNum(start?: string): number {
  if (!start) return 0
  return Math.max(0, Math.round((Date.now() - new Date(start).getTime()) / 60000))
}
function elapsed(start: string): string {
  return String(elapsedNum(start))
}
</script>
