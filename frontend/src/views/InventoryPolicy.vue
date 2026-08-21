<template>
  <div>
    <div class="d-flex align-center mb-4">
      <div>
        <h2>Statistical Safety Stock / Inventory Policy</h2>
        <div class="text-body-2 text-medium-emphasis">
          Service Level と需要・Lead Time の変動から Safety Stock / Reorder Point / Min-Max をVersion管理します。
        </div>
      </div>
      <v-spacer />
      <v-btn color="primary" prepend-icon="mdi-calculator-variant" :loading="busy" @click="refreshPolicies">再計算</v-btn>
    </div>

    <v-card class="mb-4">
      <v-card-title>Current Effective Policy</v-card-title>
      <v-card-text>
        <v-text-field v-model="search" label="品目検索" density="compact" clearable class="mb-2" />
        <v-data-table :headers="currentHeaders" :items="filteredCurrent" density="compact" :items-per-page="15">
          <template #item.serviceLevel="{ item }">{{ pct(item.serviceLevel) }}</template>
          <template #item.safetyStock="{ item }">{{ n2(item.safetyStock) }}</template>
          <template #item.reorderPoint="{ item }">{{ n2(item.reorderPoint) }}</template>
          <template #item.minQty="{ item }">{{ n2(item.minQty) }}</template>
          <template #item.maxQty="{ item }">{{ n2(item.maxQty) }}</template>
          <template #item.demand="{ item }">{{ n2(item.averageDailyDemand) }} ± {{ n2(item.stddevDailyDemand) }}</template>
          <template #item.lead="{ item }">{{ n2(item.leadTimeMeanDays) }} ± {{ n2(item.leadTimeStddevDays) }}</template>
          <template #item.status="{ item }">
            <v-chip size="small" :color="statusColor(item.calculationStatus)">{{ item.calculationStatus }}</v-chip>
          </template>
          <template #item.confidence="{ item }"><v-chip size="small">{{ item.confidence }}</v-chip></template>
        </v-data-table>
      </v-card-text>
    </v-card>

    <v-card class="mb-4">
      <v-card-title>Create Policy Version</v-card-title>
      <v-card-text>
        <v-row dense>
          <v-col cols="12" md="3"><v-select v-model="form.itemId" :items="itemOptions" item-title="label" item-value="id" label="品目" /></v-col>
          <v-col cols="6" md="2"><v-select v-model="form.policyMethod" :items="['STATISTICAL','FIXED']" label="Policy" /></v-col>
          <v-col cols="6" md="2"><v-select v-model="form.replenishmentMethod" :items="['MIN_MAX','SAFETY_STOCK']" label="Replenishment" /></v-col>
          <v-col cols="6" md="2"><v-text-field v-model.number="form.serviceLevel" type="number" step="0.001" label="Service Level" /></v-col>
          <v-col cols="6" md="3"><v-text-field v-model="form.effectiveFrom" type="date" label="Effective From" /></v-col>
          <v-col cols="6" md="2"><v-text-field v-model.number="form.demandWindowDays" type="number" label="需要窓(日)" /></v-col>
          <v-col cols="6" md="2"><v-text-field v-model.number="form.minHistoryDays" type="number" label="最低履歴(日)" /></v-col>
          <v-col cols="6" md="2"><v-text-field v-model.number="form.orderCycleDays" type="number" label="補充Cycle(日)" /></v-col>
          <v-col v-if="form.policyMethod === 'FIXED'" cols="6" md="2"><v-text-field v-model.number="form.fixedSafetyStock" type="number" label="固定Safety Stock" /></v-col>
          <v-col cols="12" md="4"><v-text-field v-model="form.notes" label="Notes" /></v-col>
          <v-col cols="12" md="2" class="d-flex align-center"><v-btn color="secondary" :disabled="!form.itemId" :loading="busy" @click="createVersion">Version作成</v-btn></v-col>
        </v-row>
      </v-card-text>
    </v-card>

    <v-card class="mb-4">
      <v-card-title>Policy Version History</v-card-title>
      <v-data-table :headers="versionHeaders" :items="versions" density="compact" :items-per-page="15">
        <template #item.serviceLevel="{ item }">{{ pct(item.serviceLevel) }}</template>
        <template #item.effectiveFrom="{ item }">{{ fmt(item.effectiveFrom) }}</template>
        <template #item.fixedSafetyStock="{ item }">{{ item.fixedSafetyStock == null ? '—' : n2(item.fixedSafetyStock) }}</template>
        <template #item.status="{ item }"><v-chip size="small" :color="versionColor(item.status)">{{ item.status }}</v-chip></template>
        <template #item.actions="{ item }">
          <v-btn v-if="item.status === 'DRAFT'" size="small" color="primary" variant="text" @click="activate(item.id)">Activate</v-btn>
          <v-btn v-if="item.status === 'ACTIVE'" size="small" color="warning" variant="text" @click="archive(item.id)">Archive</v-btn>
        </template>
      </v-data-table>
    </v-card>

    <v-card>
      <v-card-title>Calculation Run History</v-card-title>
      <v-data-table :headers="runHeaders" :items="runs" density="compact">
        <template #item.asOfDate="{ item }">{{ fmt(item.asOfDate) }}</template>
        <template #item.createdAt="{ item }">{{ fmtDT(item.createdAt) }}</template>
        <template #item.resultHash="{ item }"><code>{{ item.resultHash?.slice(0, 12) || '' }}</code></template>
      </v-data-table>
    </v-card>
    <v-snackbar v-model="snack" :color="snackColor">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { InventoryPolicyApi, ItemsApi, type EffectiveInventoryPolicy, type InventoryPolicyRun, type InventoryPolicyVersion, type Item } from '@/api'

const items = ref<Item[]>([])
const current = ref<EffectiveInventoryPolicy[]>([])
const versions = ref<InventoryPolicyVersion[]>([])
const runs = ref<InventoryPolicyRun[]>([])
const busy = ref(false)
const search = ref('')
const snack = ref(false); const snackText = ref(''); const snackColor = ref('success')
const today = new Date().toISOString().slice(0,10)
const form = ref({ itemId:'', policyMethod:'STATISTICAL', replenishmentMethod:'MIN_MAX', serviceLevel:0.95, demandWindowDays:90, minHistoryDays:30, orderCycleDays:14, fixedSafetyStock:0, effectiveFrom:today, notes:'' })

const itemOptions = computed(() => items.value.map(i => ({ id:i.id!, label:`${i.code} – ${i.name}` })))
const filteredCurrent = computed(() => {
  const q=search.value.trim().toLowerCase(); if(!q) return current.value
  return current.value.filter(x => (x.itemCode||'').toLowerCase().includes(q))
})
const currentHeaders = [
  {title:'Item',key:'itemCode'}, {title:'Ver',key:'versionNo'}, {title:'Method',key:'replenishmentMethod'}, {title:'Service',key:'serviceLevel'},
  {title:'SS',key:'safetyStock',align:'end' as const}, {title:'ROP',key:'reorderPoint',align:'end' as const}, {title:'Min',key:'minQty',align:'end' as const}, {title:'Max',key:'maxQty',align:'end' as const},
  {title:'Demand μ±σ',key:'demand'}, {title:'Lead μ±σ',key:'lead'}, {title:'Confidence',key:'confidence'}, {title:'Calc',key:'status'}
]
const versionHeaders = [
  {title:'Item',key:'itemCode'}, {title:'Ver',key:'versionNo'}, {title:'Status',key:'status'}, {title:'Policy',key:'policyMethod'}, {title:'Replenishment',key:'replenishmentMethod'},
  {title:'Service',key:'serviceLevel'}, {title:'Window',key:'demandWindowDays'}, {title:'MinHist',key:'minHistoryDays'}, {title:'Cycle',key:'orderCycleDays'}, {title:'Fixed SS',key:'fixedSafetyStock'}, {title:'Effective',key:'effectiveFrom'}, {title:'',key:'actions',sortable:false}
]
const runHeaders = [{title:'As Of',key:'asOfDate'},{title:'Status',key:'status'},{title:'By',key:'generatedBy'},{title:'Created',key:'createdAt'},{title:'Hash',key:'resultHash'}]

async function load(){ [items.value,current.value,versions.value,runs.value]=await Promise.all([ItemsApi.list(),InventoryPolicyApi.current(),InventoryPolicyApi.versions(),InventoryPolicyApi.runs()]) }
async function act(fn:()=>Promise<any>, ok:string){ busy.value=true; try{await fn(); snackText.value=ok; snackColor.value='success'; snack.value=true; await load()}catch(e:any){snackText.value=e?.response?.data?.message||String(e);snackColor.value='error';snack.value=true}finally{busy.value=false} }
async function createVersion(){ await act(()=>InventoryPolicyApi.createVersion({...form.value, fixedSafetyStock: form.value.policyMethod==='FIXED'?form.value.fixedSafetyStock:undefined}), 'Policy versionを作成しました') }
async function activate(id:string){ await act(()=>InventoryPolicyApi.activate(id),'PolicyをActivateしました') }
async function archive(id:string){ await act(()=>InventoryPolicyApi.archive(id),'PolicyをArchiveしました') }
async function refreshPolicies(){ await act(()=>InventoryPolicyApi.refresh(),'Inventory Policyを再計算しました') }
function pct(v:number){return `${((v||0)*100).toFixed(1)}%`}
function n2(v:number){return Number(v||0).toFixed(2)}
function fmt(v:string){return v?new Date(v).toLocaleDateString('ja-JP'):'—'}
function fmtDT(v:string){return v?new Date(v).toLocaleString('ja-JP'):'—'}
function statusColor(s:string){return s==='CALCULATED'?'success':'warning'}
function versionColor(s:string){return s==='ACTIVE'?'success':s==='DRAFT'?'info':'grey'}
onMounted(load)
</script>
