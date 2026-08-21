<template>
  <div>
    <div class="d-flex align-center mb-4">
      <div>
        <h1 class="text-h5">Full Pegging / Exception Management</h1>
        <div class="text-body-2 text-medium-emphasis">Sales Orderから在庫・WO・BOM・PO・Supplier/Quality・Detailed Schedulingまで原因連鎖を追跡します。</div>
      </div>
      <v-spacer />
      <v-btn prepend-icon="mdi-refresh" variant="text" @click="loadBase">更新</v-btn>
    </div>

    <v-tabs v-model="tab" class="mb-3">
      <v-tab value="exceptions">Exception Dashboard</v-tab>
      <v-tab value="pegging">Sales Order Pegging</v-tab>
    </v-tabs>

    <v-window v-model="tab">
      <v-window-item value="exceptions">
        <v-card class="mb-4">
          <v-card-text>
            <v-row align="center">
              <v-col cols="12" md="2"><v-text-field v-model.number="scanHorizon" type="number" min="1" max="366" label="Horizon days" /></v-col>
              <v-col cols="12" md="2"><v-select v-model="statusFilter" :items="['OPEN','ACKNOWLEDGED','RESOLVED']" clearable label="Status" /></v-col>
              <v-col cols="12" md="2"><v-select v-model="severityFilter" :items="['CRITICAL','WARNING','INFO']" clearable label="Severity" /></v-col>
              <v-col cols="12" md="3"><v-select v-model="typeFilter" :items="exceptionTypes" clearable label="Exception type" /></v-col>
              <v-col cols="12" md="3" class="d-flex ga-2">
                <v-btn color="primary" :loading="loading" :disabled="!canManage" prepend-icon="mdi-radar" @click="scan">全受注Scan</v-btn>
                <v-btn variant="outlined" @click="loadExceptions">Filter</v-btn>
              </v-col>
            </v-row>
            <v-alert type="info" variant="tonal" density="compact">Scanは各Open Sales Orderのimmutable Pegging snapshotを作成します。過去Runは監査用に残り、Dashboardは各受注の最新成功Runだけを表示します。</v-alert>
          </v-card-text>
        </v-card>

        <v-card>
          <v-card-title class="d-flex align-center">Planning Exceptions<v-spacer/><v-chip size="small" :color="criticalCount ? 'error' : 'success'">Critical {{ criticalCount }}</v-chip></v-card-title>
          <v-data-table :headers="exceptionHeaders" :items="exceptions" density="compact" :items-per-page="25">
            <template #item.severity="{ item }"><v-chip size="x-small" :color="severityColor(item.severity)">{{ item.severity }}</v-chip></template>
            <template #item.order="{ item }"><div>{{ item.salesOrderNo || item.salesOrderId.slice(0,8) }}</div><div class="text-caption">{{ item.customerNo }} {{ item.customerName }}</div></template>
            <template #item.item="{ item }"><div>{{ item.itemCode || '-' }}</div><div class="text-caption">{{ item.itemName || '' }}</div></template>
            <template #item.impact="{ item }"><div>{{ item.impactDays }} day(s)</div><div class="text-caption">{{ fmtDate(item.impactDate) }}</div></template>
            <template #item.currentStatus="{ item }"><v-chip size="x-small" :color="statusColor(item.currentStatus || 'OPEN')">{{ item.currentStatus || 'OPEN' }}</v-chip></template>
            <template #item.rootCausePath="{ item }"><div class="root-path">{{ pathText(item.rootCausePath) }}</div></template>
            <template #item.actions="{ item }">
              <v-btn v-if="canManage && (item.currentStatus || 'OPEN') === 'OPEN'" size="x-small" variant="text" @click="exceptionAction(item.id,'ACKNOWLEDGE')">Ack</v-btn>
              <v-btn v-if="canManage && (item.currentStatus || 'OPEN') !== 'RESOLVED'" size="x-small" variant="text" color="success" @click="exceptionAction(item.id,'RESOLVE')">Resolve</v-btn>
              <v-btn v-if="canManage && item.currentStatus === 'RESOLVED'" size="x-small" variant="text" color="warning" @click="exceptionAction(item.id,'REOPEN')">Reopen</v-btn>
            </template>
          </v-data-table>
        </v-card>
      </v-window-item>

      <v-window-item value="pegging">
        <v-card class="mb-4">
          <v-card-text>
            <v-row align="center">
              <v-col cols="12" md="6"><v-select v-model="selectedOrderId" :items="orderOptions" item-title="label" item-value="id" label="Sales Order" /></v-col>
              <v-col cols="12" md="2"><v-text-field v-model.number="peggingHorizon" type="number" min="1" max="366" label="Horizon days" /></v-col>
              <v-col cols="12" md="4" class="d-flex ga-2">
                <v-btn color="primary" :loading="loading" :disabled="!canManage || !selectedOrderId" prepend-icon="mdi-family-tree" @click="runPegging">Pegging Run</v-btn>
                <v-btn variant="outlined" :disabled="!selectedOrderId" @click="loadRuns">履歴</v-btn>
              </v-col>
            </v-row>
          </v-card-text>
        </v-card>

        <v-card v-if="runs.length" class="mb-4">
          <v-card-title>Pegging Run History</v-card-title>
          <v-data-table :headers="runHeaders" :items="runs" density="compact" :items-per-page="5">
            <template #item.createdAt="{ item }">{{ fmtDateTime(item.createdAt) }}</template>
            <template #item.status="{ item }"><v-chip size="x-small" :color="item.status==='SUCCEEDED'?'success':item.status==='FAILED'?'error':'info'">{{ item.status }}</v-chip></template>
            <template #item.actions="{ item }"><v-btn size="x-small" variant="text" @click="openRun(item.id)">Open</v-btn></template>
          </v-data-table>
        </v-card>

        <template v-if="result">
          <v-row>
            <v-col cols="12" lg="7">
              <v-card class="mb-4">
                <v-card-title class="d-flex align-center">Pegging Nodes<v-spacer/><v-chip size="small">{{ result.nodes.length }} nodes</v-chip></v-card-title>
                <v-data-table :headers="nodeHeaders" :items="result.nodes" density="compact" :items-per-page="20">
                  <template #item.nodeType="{ item }"><v-chip size="x-small" variant="outlined" :color="nodeColor(item.nodeType)">{{ item.nodeType }}</v-chip></template>
                  <template #item.quantity="{ item }">{{ item.quantity == null ? '-' : num(item.quantity) }}</template>
                  <template #item.dueDate="{ item }">{{ fmtDate(item.dueDate) }}</template>
                </v-data-table>
              </v-card>
            </v-col>
            <v-col cols="12" lg="5">
              <v-card class="mb-4">
                <v-card-title>Root Cause Exceptions</v-card-title>
                <v-list lines="three">
                  <v-list-item v-for="x in result.exceptions" :key="x.id">
                    <template #prepend><v-icon :color="severityColor(x.severity)">mdi-alert-circle</v-icon></template>
                    <v-list-item-title>{{ x.exceptionType }} / {{ x.severity }}</v-list-item-title>
                    <v-list-item-subtitle>{{ x.message }}</v-list-item-subtitle>
                    <div class="text-caption mt-1">{{ pathText(x.rootCausePath) }}</div>
                  </v-list-item>
                  <v-list-item v-if="!result.exceptions.length" title="Exceptionなし" subtitle="このSnapshotでは重大な遅延・不足原因は検出されませんでした。" />
                </v-list>
              </v-card>
            </v-col>
          </v-row>

          <v-card>
            <v-card-title class="d-flex align-center">Pegging Relationships<v-spacer/><v-chip size="small">{{ result.edges.length }} edges</v-chip></v-card-title>
            <v-data-table :headers="edgeHeaders" :items="edgeRows" density="compact" :items-per-page="20">
              <template #item.quantity="{ item }">{{ item.quantity == null ? '-' : num(item.quantity) }}</template>
            </v-data-table>
            <v-card-text class="text-caption">Run {{ result.run.id }} / Hash {{ result.run.resultHash || '-' }} / As-of {{ fmtDateTime(result.run.asOf) }}</v-card-text>
          </v-card>
        </template>
      </v-window-item>
    </v-window>

    <v-dialog v-model="commentDialog" max-width="520">
      <v-card :title="`${pendingAction?.action || ''} Exception`">
        <v-card-text><v-textarea v-model="actionComment" label="Comment" rows="3" /></v-card-text>
        <v-card-actions><v-spacer/><v-btn @click="commentDialog=false">Cancel</v-btn><v-btn color="primary" @click="submitAction">実行</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snackbar" :color="errorMessage ? 'error' : 'success'">{{ errorMessage || '完了しました' }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { PeggingApi, SalesOrdersApi, type PeggingResult, type PeggingRun, type PlanningException, type SalesOrder } from '@/api'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const tab = ref<'exceptions'|'pegging'>('exceptions')
const loading = ref(false)
const orders = ref<SalesOrder[]>([])
const selectedOrderId = ref('')
const peggingHorizon = ref(180)
const scanHorizon = ref(180)
const runs = ref<PeggingRun[]>([])
const result = ref<PeggingResult | null>(null)
const exceptions = ref<PlanningException[]>([])
const statusFilter = ref<string>()
const severityFilter = ref<string>()
const typeFilter = ref<string>()
const snackbar = ref(false)
const errorMessage = ref('')
const commentDialog = ref(false)
const actionComment = ref('')
const pendingAction = ref<{id:string,action:'ACKNOWLEDGE'|'RESOLVE'|'REOPEN'}>()
const canManage = computed(() => auth.role === 'admin' || auth.role === 'planner')

const exceptionTypes = ['LATE_PROMISE','BACKORDER','UNCONVERTED_CTP','MATERIAL_SHORTAGE','LATE_PURCHASE_ORDER','SUPPLIER_BLOCKED','QUALITY_HOLD','LATE_WORK_ORDER','CAPACITY_LATE','CAPACITY_UNSCHEDULED']
const orderOptions = computed(() => orders.value.filter(o => !['SHIPPED','CANCELLED'].includes(o.status)).map(o => ({id:o.id,label:`${o.orderNo} – ${o.customerName} – ${num(o.openQty)}`})))
const criticalCount = computed(() => exceptions.value.filter(x => x.severity==='CRITICAL' && (x.currentStatus||'OPEN')!=='RESOLVED').length)
const edgeRows = computed(() => {
  if (!result.value) return []
  const map = Object.fromEntries(result.value.nodes.map(n => [n.id,n]))
  return result.value.edges.map(e => ({...e,from:map[e.fromNodeId]?.label||e.fromNodeId,to:map[e.toNodeId]?.label||e.toNodeId}))
})

const exceptionHeaders = [
  {title:'Severity',key:'severity'},{title:'Order / Customer',key:'order'},{title:'Item',key:'item'},{title:'Type',key:'exceptionType'},
  {title:'Message',key:'message'},{title:'Impact',key:'impact'},{title:'Root Cause Path',key:'rootCausePath'},{title:'Status',key:'currentStatus'},{title:'',key:'actions',sortable:false}
]
const runHeaders = [{title:'Created',key:'createdAt'},{title:'Status',key:'status'},{title:'Horizon',key:'horizonDays'},{title:'Generated by',key:'generatedBy'},{title:'',key:'actions',sortable:false}]
const nodeHeaders = [{title:'Type',key:'nodeType'},{title:'Label',key:'label'},{title:'Item',key:'itemCode'},{title:'Qty',key:'quantity'},{title:'Due',key:'dueDate'},{title:'Status',key:'status'}]
const edgeHeaders = [{title:'From',key:'from'},{title:'Relationship',key:'edgeType'},{title:'To',key:'to'},{title:'Qty',key:'quantity'}]

function num(v:number){ return Number(v||0).toLocaleString('ja-JP',{maximumFractionDigits:3}) }
function fmtDate(v?:string){ return v ? new Date(v).toLocaleDateString('ja-JP') : '-' }
function fmtDateTime(v?:string){ return v ? new Date(v).toLocaleString('ja-JP') : '-' }
function pathText(v?:string[]){ return Array.isArray(v) ? v.join(' → ') : '-' }
function severityColor(v:string){ return v==='CRITICAL'?'error':v==='WARNING'?'warning':'info' }
function statusColor(v:string){ return v==='RESOLVED'?'success':v==='ACKNOWLEDGED'?'info':'warning' }
function nodeColor(v:string){ if(v==='SHORTAGE'||v==='QUALITY_HOLD')return 'error'; if(v==='WORK_ORDER'||v==='PURCHASE_ORDER'||v==='PLANNED_ORDER')return 'primary'; if(v==='WORK_CENTER'||v==='DETAILED_SCHEDULE')return 'purple'; return 'grey' }
function notify(err?:unknown){ errorMessage.value=err?String((err as any)?.response?.data?.message||(err as any)?.response?.data?.error||(err as any)?.message||err):''; snackbar.value=true }

async function loadBase(){
  try{orders.value=await SalesOrdersApi.list()??[];if(!selectedOrderId.value)selectedOrderId.value=orderOptions.value[0]?.id||'';await loadExceptions()}catch(e){notify(e)}
}
async function loadExceptions(){try{exceptions.value=await PeggingApi.exceptions({status:statusFilter.value,severity:severityFilter.value,type:typeFilter.value})??[]}catch(e){notify(e)}}
async function scan(){loading.value=true;try{const r=await PeggingApi.scan(scanHorizon.value);exceptions.value=r.exceptions??[];notify()}catch(e){notify(e)}finally{loading.value=false}}
async function runPegging(){if(!selectedOrderId.value)return;loading.value=true;try{result.value=await PeggingApi.run(selectedOrderId.value,peggingHorizon.value);await loadRuns();await loadExceptions();notify()}catch(e){notify(e)}finally{loading.value=false}}
async function loadRuns(){if(!selectedOrderId.value)return;try{runs.value=await PeggingApi.runs(selectedOrderId.value)??[]}catch(e){notify(e)}}
async function openRun(id:string){try{result.value=await PeggingApi.runDetail(id)}catch(e){notify(e)}}
function exceptionAction(id:string,action:'ACKNOWLEDGE'|'RESOLVE'|'REOPEN'){pendingAction.value={id,action};actionComment.value='';commentDialog.value=true}
async function submitAction(){if(!pendingAction.value)return;try{await PeggingApi.act(pendingAction.value.id,pendingAction.value.action,actionComment.value);commentDialog.value=false;await loadExceptions();notify()}catch(e){notify(e)}}

onMounted(loadBase)
</script>

<style scoped>
.root-path { max-width: 320px; white-space: normal; overflow-wrap: anywhere; font-size: 0.75rem; }
</style>
