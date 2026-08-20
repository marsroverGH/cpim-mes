<template>
  <div>
    <div class="d-flex align-center mb-4">
      <div>
        <h1 class="text-h5">Backorder Processing / Product Allocation</h1>
        <div class="text-body-2 text-medium-emphasis">供給変動後の受注優先順位・ATP再配分・CTP再計算を Preview → Publish で管理します。</div>
      </div>
      <v-spacer />
      <v-btn prepend-icon="mdi-refresh" variant="text" @click="loadAll">更新</v-btn>
    </div>

    <v-row>
      <v-col cols="12" lg="7">
        <v-card class="mb-4">
          <v-card-title class="d-flex align-center">
            BOP Preview
            <v-spacer />
            <v-chip v-if="result" :color="result.publication ? 'success' : 'info'" size="small">
              {{ result.publication ? 'PUBLISHED' : result.run.status }}
            </v-chip>
          </v-card-title>
          <v-card-text>
            <v-row>
              <v-col cols="12" md="3"><v-text-field v-model.number="horizonDays" type="number" min="1" max="366" label="Horizon days" /></v-col>
              <v-col cols="12" md="6"><v-select v-model="filterItemId" :items="sellableItems" item-title="label" item-value="id" clearable label="品目フィルタ（任意）" /></v-col>
              <v-col cols="12" md="3" class="d-flex align-center"><v-btn block color="primary" :loading="loading" prepend-icon="mdi-play" @click="preview">Preview</v-btn></v-col>
            </v-row>
            <v-alert type="info" variant="tonal" density="compact">既存のInventory Allocationは固定保護し、未引当ATPだけをProduct Allocationで再配分します。PreviewはWO/PO/Inventory/Scheduleを変更しません。</v-alert>
          </v-card-text>
        </v-card>

        <v-card v-if="result" class="mb-4">
          <v-card-title>Before / After</v-card-title>
          <v-data-table :headers="resultHeaders" :items="result.lines" density="compact" :items-per-page="25">
            <template #item.rankNo="{ item }"><strong>{{ item.rankNo }}</strong></template>
            <template #item.order="{ item }"><div>{{ item.salesOrderNo }}</div><div class="text-caption">{{ item.customerNo }} {{ item.customerName }}</div></template>
            <template #item.policy="{ item }"><v-chip size="x-small" class="mr-1">{{ item.orderPriority }}</v-chip><v-chip size="x-small" variant="outlined">{{ item.serviceClassCode }}</v-chip></template>
            <template #item.qty="{ item }">{{ num(item.openQty) }} / {{ num(item.allocatedQty) }}</template>
            <template #item.supply="{ item }">ATP {{ num(item.atpQty) }} / CTP {{ num(item.ctpQty) }}<div v-if="item.backorderQty" class="text-error">Backorder {{ num(item.backorderQty) }}</div></template>
            <template #item.before="{ item }">{{ fmtDate(item.currentPromisedDate) }}</template>
            <template #item.after="{ item }">{{ fmtDate(item.proposedPromisedDate) }}</template>
            <template #item.decision="{ item }"><v-chip size="small" :color="decisionColor(item.decision)">{{ item.decision }}</v-chip></template>
            <template #item.constraintType="{ item }"><v-chip size="x-small" variant="outlined" :color="constraintColor(item.constraintType)">{{ item.constraintType }}</v-chip></template>
          </v-data-table>
          <v-card-text>
            <div class="text-caption">Run {{ result.run.id }} / Hash {{ result.run.resultHash || '-' }}</div>
            <v-alert v-if="backorderTotal > 0" type="warning" variant="tonal" density="compact" class="mt-2">Horizon内に未確約 {{ num(backorderTotal) }} が残ります。Publishすると該当明細のPromised Dateは未確約として空になります。</v-alert>
          </v-card-text>
          <v-card-actions>
            <v-btn variant="text" @click="showConfirmations = !showConfirmations">分納詳細</v-btn>
            <v-spacer />
            <v-btn v-if="canPlan && !result.publication" color="success" :loading="loading" prepend-icon="mdi-publish" @click="publish">Publish</v-btn>
          </v-card-actions>
        </v-card>

        <v-card v-if="result && showConfirmations" class="mb-4">
          <v-card-title>Confirmation detail</v-card-title>
          <v-table density="compact">
            <thead><tr><th>Sales Order Line</th><th>Seq</th><th>Qty</th><th>Date</th><th>Source</th></tr></thead>
            <tbody><tr v-for="c in result.confirmations" :key="c.id || `${c.salesOrderLineId}-${c.sequenceNo}`"><td>{{ lineLabel(c.salesOrderLineId) }}</td><td>{{ c.sequenceNo }}</td><td>{{ num(c.quantity) }}</td><td>{{ fmtDate(c.confirmedDate) }}</td><td>{{ c.source }}</td></tr></tbody>
          </v-table>
        </v-card>
      </v-col>

      <v-col cols="12" lg="5">
        <v-card class="mb-4">
          <v-card-title>Customer Service Class</v-card-title>
          <v-data-table :headers="customerHeaders" :items="customers" density="compact" :items-per-page="8">
            <template #item.status="{ item }"><v-chip size="x-small" :color="item.status==='ACTIVE'?'success':'error'">{{ item.status }}</v-chip></template>
            <template #item.serviceClassCode="{ item }">
              <v-select v-model="customerClassDraft[item.id]" :items="serviceClasses" item-title="name" item-value="code" density="compact" hide-details :disabled="!canPlan" @update:model-value="saveCustomerClass(item.id)" />
            </template>
          </v-data-table>
        </v-card>

        <v-card class="mb-4">
          <v-card-title>Sales Order Priority</v-card-title>
          <v-data-table :headers="orderHeaders" :items="openOrders" density="compact" :items-per-page="8">
            <template #item.promisedDate="{ item }">{{ fmtDate(item.promisedDate) }}</template>
            <template #item.priority="{ item }">
              <v-select v-model="orderPriorityDraft[item.id]" :items="['EXPEDITE','HIGH','NORMAL']" density="compact" hide-details :disabled="!canPlan" @update:model-value="saveOrderPriority(item.id)" />
            </template>
          </v-data-table>
        </v-card>

        <v-card>
          <v-card-title class="d-flex align-center">Product Allocation Plans<v-spacer/><v-btn v-if="canPlan" size="small" prepend-icon="mdi-plus" @click="openPlanDialog">新規</v-btn></v-card-title>
          <v-data-table :headers="planHeaders" :items="plans" density="compact" :items-per-page="8">
            <template #item.plan="{ item }"><div>{{ item.plan.name }}</div><div class="text-caption">{{ item.plan.itemCode }}</div></template>
            <template #item.period="{ item }">{{ fmtDate(item.plan.effectiveFrom) }} – {{ fmtDate(item.plan.effectiveTo) }}</template>
            <template #item.buckets="{ item }"><div v-for="b in item.buckets" :key="b.serviceClassCode" class="text-caption">{{ b.serviceClassCode }} {{ num(b.allocationPct) }}%</div></template>
            <template #item.status="{ item }"><v-chip size="x-small" :color="item.plan.status==='ACTIVE'?'success':item.plan.status==='DRAFT'?'info':'grey'">{{ item.plan.status }}</v-chip></template>
            <template #item.actions="{ item }"><v-btn v-if="canPlan && item.plan.status==='DRAFT'" size="x-small" variant="text" @click="activatePlan(item.plan.id)">Activate</v-btn><v-btn v-if="canPlan && item.plan.status==='ACTIVE'" size="x-small" variant="text" color="warning" @click="deactivatePlan(item.plan.id)">Deactivate</v-btn></template>
          </v-data-table>
        </v-card>
      </v-col>
    </v-row>

    <v-dialog v-model="planDialog" max-width="760">
      <v-card title="Product Allocation Plan">
        <v-card-text>
          <v-row>
            <v-col cols="12" md="6"><v-select v-model="planForm.itemId" :items="sellableItems" item-title="label" item-value="id" label="品目" /></v-col>
            <v-col cols="12" md="6"><v-text-field v-model="planForm.name" label="Plan名" /></v-col>
            <v-col cols="6"><v-text-field v-model="planForm.effectiveFrom" type="date" label="From" /></v-col>
            <v-col cols="6"><v-text-field v-model="planForm.effectiveTo" type="date" label="To" /></v-col>
          </v-row>
          <h3 class="text-subtitle-1 mb-2">Allocation %（合計100%）</h3>
          <v-row v-for="b in planForm.buckets" :key="b.serviceClassCode">
            <v-col cols="5"><v-text-field :model-value="b.serviceClassCode" readonly label="Service Class" /></v-col>
            <v-col cols="4"><v-text-field v-model.number="b.allocationPct" type="number" min="0" max="100" label="Allocation %" /></v-col>
            <v-col cols="3"><v-text-field v-model.number="b.priorityRank" type="number" min="1" label="Rank" /></v-col>
          </v-row>
          <div class="text-caption">Total: {{ num(planPctTotal) }}%</div>
        </v-card-text>
        <v-card-actions><v-spacer/><v-btn @click="planDialog=false">Cancel</v-btn><v-btn color="primary" :disabled="Math.abs(planPctTotal-100)>0.000001" @click="createPlan">保存</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snackbar" :color="errorMessage ? 'error' : 'success'">{{ errorMessage || '完了しました' }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { BackordersApi, ItemsApi, SalesOrdersApi, type BackorderResult, type Customer, type CustomerServiceClass, type Item, type ProductAllocationPlanDetail, type ProductAllocationPlanInput, type SalesOrder } from '@/api'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const loading = ref(false)
const horizonDays = ref(90)
const filterItemId = ref<string>()
const result = ref<BackorderResult | null>(null)
const showConfirmations = ref(false)
const items = ref<Item[]>([])
const customers = ref<Customer[]>([])
const orders = ref<SalesOrder[]>([])
const serviceClasses = ref<CustomerServiceClass[]>([])
const plans = ref<ProductAllocationPlanDetail[]>([])
const customerClassDraft = ref<Record<string,string>>({})
const orderPriorityDraft = ref<Record<string,'EXPEDITE'|'HIGH'|'NORMAL'>>({})
const snackbar = ref(false)
const errorMessage = ref('')
const planDialog = ref(false)
const canPlan = computed(() => auth.role === 'admin' || auth.role === 'planner')

const today = () => new Date().toISOString().slice(0,10)
const plusDays = (n:number) => new Date(Date.now()+n*86400000).toISOString().slice(0,10)
const planForm = ref<ProductAllocationPlanInput>({ itemId:'', name:'', effectiveFrom:today(), effectiveTo:plusDays(90), buckets:[] })
const sellableItems = computed(() => items.value.filter(i=>i.type==='FG'||i.type==='SA').map(i=>({id:i.id!,label:`${i.code} – ${i.name}`})))
const openOrders = computed(() => orders.value.filter(o=>!['SHIPPED','CANCELLED'].includes(o.status)))
const backorderTotal = computed(() => result.value?.lines.reduce((a,l)=>a+l.backorderQty,0) || 0)
const planPctTotal = computed(() => planForm.value.buckets.reduce((a,b)=>a+Number(b.allocationPct||0),0))

const resultHeaders = [
  {title:'#',key:'rankNo'},{title:'Order / Customer',key:'order'},{title:'Priority / Class',key:'policy'},{title:'Item',key:'itemCode'},
  {title:'Open / Alloc',key:'qty'},{title:'Supply',key:'supply'},{title:'Before',key:'before'},{title:'After',key:'after'},
  {title:'Decision',key:'decision'},{title:'Constraint',key:'constraintType'}
]
const customerHeaders = [{title:'Customer',key:'customerNo'},{title:'Name',key:'name'},{title:'Status',key:'status'},{title:'Service Class',key:'serviceClassCode'}]
const orderHeaders = [{title:'SO',key:'orderNo'},{title:'Customer',key:'customerName'},{title:'Promised',key:'promisedDate'},{title:'Priority',key:'priority'}]
const planHeaders = [{title:'Plan / Item',key:'plan'},{title:'Period',key:'period'},{title:'Buckets',key:'buckets'},{title:'Status',key:'status'},{title:'',key:'actions',sortable:false}]

function num(v:number){ return Number(v||0).toLocaleString('ja-JP',{maximumFractionDigits:3}) }
function fmtDate(v?:string){ return v ? new Date(v).toLocaleDateString('ja-JP') : '-' }
function decisionColor(v:string){ return v==='BACKORDER'?'error':v==='DELAYED'?'warning':v==='IMPROVED'?'success':v==='NEW_PROMISE'?'info':'grey' }
function constraintColor(v:string){ return v==='NONE'?'success':v==='PRODUCT_ALLOCATION'?'purple':v==='HORIZON'?'error':'warning' }
function lineLabel(id:string){ const l=result.value?.lines.find(x=>x.salesOrderLineId===id); return l ? `${l.salesOrderNo} / ${l.itemCode}` : id.slice(0,8) }
function notify(err?:unknown){ errorMessage.value=err?String((err as any)?.response?.data?.message||(err as any)?.response?.data?.error||(err as any)?.message||err):''; snackbar.value=true }

async function loadAll(){
  try{
    const [i,c,o,sc,p]=await Promise.all([ItemsApi.list(),SalesOrdersApi.customers(),SalesOrdersApi.list(),SalesOrdersApi.serviceClasses(),BackordersApi.plans()])
    items.value=i??[]; customers.value=c??[]; orders.value=o??[]; serviceClasses.value=sc??[]; plans.value=p??[]
    customerClassDraft.value=Object.fromEntries(customers.value.map(x=>[x.id,x.serviceClassCode||'STANDARD']))
    orderPriorityDraft.value=Object.fromEntries(orders.value.map(x=>[x.id,x.priority||'NORMAL'])) as Record<string,'EXPEDITE'|'HIGH'|'NORMAL'>
  }catch(e){notify(e)}
}
async function preview(){ loading.value=true; try{ result.value=await BackordersApi.preview(horizonDays.value,filterItemId.value); showConfirmations.value=false; notify() }catch(e){notify(e)}finally{loading.value=false} }
async function publish(){ if(!result.value||!confirm('このBOP案をSales Orderの約束日に反映しますか？'))return; loading.value=true; try{result.value=await BackordersApi.publish(result.value.run.id); await loadAll(); notify()}catch(e){notify(e)}finally{loading.value=false} }
async function saveCustomerClass(id:string){ if(!canPlan.value)return; try{await SalesOrdersApi.setCustomerServiceClass(id,customerClassDraft.value[id]); await loadAll(); notify()}catch(e){notify(e)} }
async function saveOrderPriority(id:string){ if(!canPlan.value)return; try{await SalesOrdersApi.setPriority(id,orderPriorityDraft.value[id]); await loadAll(); notify()}catch(e){notify(e)} }
function openPlanDialog(){
  const active=serviceClasses.value.filter(x=>x.isActive)
  const equal=active.length?100/active.length:0
  planForm.value={itemId:sellableItems.value[0]?.id||'',name:`Allocation ${today()}`,effectiveFrom:today(),effectiveTo:plusDays(90),buckets:active.map((x,i)=>({serviceClassCode:x.code,allocationPct:i===active.length-1?100-equal*(active.length-1):equal,priorityRank:x.priorityRank}))}
  planDialog.value=true
}
async function createPlan(){ try{await BackordersApi.createPlan(planForm.value); planDialog.value=false; await loadAll(); notify()}catch(e){notify(e)} }
async function activatePlan(id:string){ try{await BackordersApi.activatePlan(id); await loadAll(); notify()}catch(e){notify(e)} }
async function deactivatePlan(id:string){ try{await BackordersApi.deactivatePlan(id); await loadAll(); notify()}catch(e){notify(e)} }

onMounted(loadAll)
</script>
