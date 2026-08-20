<template>
  <div>
  <v-row>
    <!-- LEFT: routing list -->
    <v-col cols="12" md="5">
      <v-card>
        <v-card-title class="d-flex align-center">
          ルーティング
          <v-spacer />
          <v-btn color="primary" prepend-icon="mdi-plus" size="small" @click="openNewRouting">新規</v-btn>
        </v-card-title>
        <v-list density="compact" nav>
          <v-list-item
            v-for="r in routings" :key="r.id"
            :active="selected?.id === r.id"
            @click="select(r)"
          >
            <v-list-item-title>{{ codeMap[r.itemId] || r.itemId }}</v-list-item-title>
            <v-list-item-subtitle>
              {{ r.description }}
              <v-chip v-if="r.isActive" color="success" size="x-small" class="ml-2">ACTIVE</v-chip>
            </v-list-item-subtitle>
          </v-list-item>
        </v-list>
      </v-card>
    </v-col>

    <!-- RIGHT: operations of selected routing -->
    <v-col cols="12" md="7">
      <v-card>
        <v-card-title class="d-flex align-center">
          工程一覧
          <v-spacer />
          <v-btn v-if="selected" color="primary" prepend-icon="mdi-plus" size="small"
                 @click="openNewOp">工程追加</v-btn>
        </v-card-title>
        <v-card-text v-if="!selected" class="text-medium-emphasis">
          左でルーティングを選んでください
        </v-card-text>
        <v-data-table v-else :items="ops" :headers="opHeaders" density="compact">
          <template #item.workCenter="{ item }">
            {{ wcMap[item.workCenterId] || item.workCenterId }}
          </template>
          <template #item.overlapEnabled="{ item }"><v-chip size="x-small" :color="item.overlapEnabled ? 'primary' : 'default'">{{ item.overlapEnabled ? 'LOT STREAM' : 'SERIAL' }}</v-chip></template>
          <template #item.actions="{ item }">
            <v-btn icon="mdi-pencil" size="small" variant="text" title="工程編集" @click="openEditOp(item)" />
            <v-btn icon="mdi-swap-horizontal" size="small" variant="text" title="代替Work Center" @click="openAlternatives(item)" />
            <v-btn icon="mdi-delete" size="small" variant="text" color="error" @click="removeOp(item)" />
          </template>
        </v-data-table>
      </v-card>
    </v-col>
  </v-row>

  <!-- New routing dialog -->
  <v-dialog v-model="rtDialog" max-width="500">
    <v-card title="新規ルーティング">
      <v-card-text>
        <v-select v-model="rtForm.itemId" :items="itemOptions"
                  item-title="label" item-value="id" label="対象品目 (FG/SA)" />
        <v-text-field v-model="rtForm.description" label="説明" />
        <v-switch v-model="rtForm.isActive" label="有効化" color="primary" />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="rtDialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="saveRouting">保存</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <!-- New operation dialog -->
  <v-dialog v-model="opDialog" max-width="500">
    <v-card :title="editingOpId ? '工程編集' : '工程追加'">
      <v-card-text>
        <v-text-field v-model.number="opForm.seqNo" type="number" label="工程番号 (10, 20, 30…)" />
        <v-select v-model="opForm.workCenterId" :items="wcOptions"
                  item-title="label" item-value="id" label="作業区" />
        <v-text-field v-model="opForm.description" label="工程名" />
        <v-text-field v-model.number="opForm.setupMinutes" type="number" label="段取り時間 (分)" />
        <v-text-field v-model.number="opForm.runMinutesPerUnit" type="number" step="0.1" label="単位あたり加工時間 (分)" />
        <v-text-field v-model="opForm.setupFamily" label="Setup Family" hint="品種切替段取りの分類" persistent-hint />
        <v-switch v-model="opForm.overlapEnabled" label="工程間Overlap / Lot Streaming" color="primary" />
        <v-text-field v-model.number="opForm.transferBatchQty" type="number" min="0" label="Transfer Batch数量" :disabled="!opForm.overlapEnabled" />
        <v-row dense>
          <v-col cols="6"><v-text-field v-model.number="opForm.machinesRequired" type="number" min="1" label="必要設備台数" /></v-col>
          <v-col cols="6"><v-text-field v-model.number="opForm.workersRequired" type="number" min="0" label="必要作業者数" /></v-col>
        </v-row>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="opDialog = false">キャンセル</v-btn>
        <v-btn color="primary" @click="saveOp">{{ editingOpId ? '保存' : '追加' }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>

  <v-dialog v-model="altDialog" max-width="760">
    <v-card :title="`代替Work Center – 工程 ${altOp?.seqNo ?? ''}`">
      <v-card-text>
        <v-row dense>
          <v-col cols="4"><v-select v-model="altForm.workCenterId" :items="wcOptions" item-title="label" item-value="id" label="代替作業区" /></v-col>
          <v-col cols="2"><v-text-field v-model.number="altForm.priority" type="number" min="0" label="優先度" /></v-col>
          <v-col cols="2"><v-text-field v-model.number="altForm.runTimeMultiplier" type="number" step="0.05" min="0.01" label="加工倍率" /></v-col>
          <v-col cols="2"><v-text-field v-model.number="altForm.setupTimeMultiplier" type="number" step="0.05" min="0.01" label="段取倍率" /></v-col>
          <v-col cols="2" class="d-flex align-center"><v-btn color="primary" prepend-icon="mdi-plus" @click="saveAlternative">追加</v-btn></v-col>
        </v-row>
        <v-data-table :items="alternatives" :headers="altHeaders" density="compact">
          <template #item.workCenterId="{ item }">{{ wcMap[item.workCenterId] || item.workCenterId }}</template>
          <template #item.actions="{ item }"><v-btn icon="mdi-delete" size="small" variant="text" color="error" @click="removeAlternative(item)" /></template>
        </v-data-table>
      </v-card-text>
      <v-card-actions><v-spacer/><v-btn @click="altDialog=false">閉じる</v-btn></v-card-actions>
    </v-card>
  </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  ItemsApi, RoutingsApi, WorkCentersApi,
  type Item, type Routing, type RoutingOperation, type RoutingOperationAlternative, type WorkCenter
} from '@/api'

const items = ref<Item[]>([])
const wcs = ref<WorkCenter[]>([])
const routings = ref<Routing[]>([])
const ops = ref<RoutingOperation[]>([])
const selected = ref<Routing | null>(null)

const rtDialog = ref(false)
const rtForm = ref<Routing>({ itemId: '', description: '', isActive: true })

const opDialog = ref(false)
const editingOpId = ref<string | null>(null)
const opForm = ref<Omit<RoutingOperation, 'routingId'>>({
  seqNo: 10, workCenterId: '', description: '', setupMinutes: 0, runMinutesPerUnit: 0,
  setupFamily: '', overlapEnabled: false, transferBatchQty: 0, machinesRequired: 1, workersRequired: 1
})
const altDialog = ref(false)
const altOp = ref<RoutingOperation | null>(null)
const alternatives = ref<RoutingOperationAlternative[]>([])
const altForm = ref({ workCenterId: '', priority: 100, runTimeMultiplier: 1, setupTimeMultiplier: 1, isActive: true })

const codeMap = computed(() => {
  const m: Record<string, string> = {}
  for (const i of (items.value ?? [])) if (i.id) m[i.id] = `${i.code} – ${i.name}`
  return m
})
const wcMap = computed(() => {
  const m: Record<string, string> = {}
  for (const w of (wcs.value ?? [])) if (w.id) m[w.id] = `${w.code} – ${w.name}`
  return m
})
const itemOptions = computed(() =>
  (items.value ?? []).filter(i => i.type === 'FG' || i.type === 'SA')
    .map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))
const wcOptions = computed(() =>
  (wcs.value ?? []).map(w => ({ id: w.id!, label: `${w.code} – ${w.name}` })))

const opHeaders = [
  { title: 'No.',        key: 'seqNo' },
  { title: '作業区',     key: 'workCenter' },
  { title: '工程',       key: 'description' },
  { title: '段取(分)',   key: 'setupMinutes',      align: 'end' as const },
  { title: '加工(分/個)',key: 'runMinutesPerUnit', align: 'end' as const },
  { title: 'Setup Family',key: 'setupFamily' },
  { title: 'Overlap', key: 'overlapEnabled' },
  { title: 'Transfer', key: 'transferBatchQty', align: 'end' as const },
  { title: '設備', key: 'machinesRequired', align: 'end' as const },
  { title: '作業者', key: 'workersRequired', align: 'end' as const },
  { title: '',           key: 'actions',           sortable: false, align: 'end' as const }
]

async function load() {
  const [a, b, c] = await Promise.all([ItemsApi.list(), WorkCentersApi.list(), RoutingsApi.list()])
  items.value = a ?? []; wcs.value = b ?? []; routings.value = c ?? []
}
onMounted(load)

async function select(r: Routing) {
  selected.value = r
  if (r.id) ops.value = (await RoutingsApi.operations(r.id)) ?? []
}

function openNewRouting() {
  rtForm.value = { itemId: '', description: '', isActive: true }
  rtDialog.value = true
}
async function saveRouting() {
  await RoutingsApi.create(rtForm.value)
  rtDialog.value = false
  await load()
}

function openNewOp() {
  editingOpId.value = null
  const nextSeq = (ops.value ?? []).length ? Math.max(...(ops.value ?? []).map(o => o.seqNo)) + 10 : 10
  opForm.value = { seqNo: nextSeq, workCenterId: '', description: '', setupMinutes: 0, runMinutesPerUnit: 0,
                   setupFamily: '', overlapEnabled: false, transferBatchQty: 0, machinesRequired: 1, workersRequired: 1 }
  opDialog.value = true
}
function openEditOp(op: RoutingOperation) {
  if (!op.id) return
  editingOpId.value = op.id
  opForm.value = {
    id: op.id, seqNo: op.seqNo, workCenterId: op.workCenterId, description: op.description,
    setupMinutes: op.setupMinutes, runMinutesPerUnit: op.runMinutesPerUnit, setupFamily: op.setupFamily ?? '',
    overlapEnabled: op.overlapEnabled ?? false, transferBatchQty: op.transferBatchQty ?? 0,
    machinesRequired: op.machinesRequired ?? 1, workersRequired: op.workersRequired ?? 1
  }
  opDialog.value = true
}
async function saveOp() {
  if (!selected.value?.id) return
  if (editingOpId.value) {
    const { id: _id, ...payload } = opForm.value as any
    await RoutingsApi.updateOperation(editingOpId.value, payload)
  } else {
    await RoutingsApi.addOperation(selected.value.id, opForm.value)
  }
  opDialog.value = false
  editingOpId.value = null
  ops.value = (await RoutingsApi.operations(selected.value.id)) ?? []
}
const altHeaders = [
  { title: '代替作業区', key: 'workCenterId' }, { title: '優先度', key: 'priority' },
  { title: '加工倍率', key: 'runTimeMultiplier' }, { title: '段取倍率', key: 'setupTimeMultiplier' }, { title: '', key: 'actions', sortable: false }
]
async function openAlternatives(op: RoutingOperation) {
  if (!op.id) return
  altOp.value = op; alternatives.value = (await RoutingsApi.alternatives(op.id)) ?? []
  altForm.value = { workCenterId: '', priority: 100, runTimeMultiplier: 1, setupTimeMultiplier: 1, isActive: true }; altDialog.value = true
}
async function saveAlternative() {
  if (!altOp.value?.id || !altForm.value.workCenterId) return
  await RoutingsApi.addAlternative(altOp.value.id, altForm.value)
  alternatives.value = (await RoutingsApi.alternatives(altOp.value.id)) ?? []
}
async function removeAlternative(x: RoutingOperationAlternative) {
  if (!x.id || !altOp.value?.id) return
  await RoutingsApi.removeAlternative(x.id); alternatives.value = (await RoutingsApi.alternatives(altOp.value.id)) ?? []
}

async function removeOp(op: RoutingOperation) {
  if (!op.id) return
  await RoutingsApi.removeOperation(op.id)
  if (selected.value?.id) ops.value = (await RoutingsApi.operations(selected.value.id)) ?? []
}
</script>
