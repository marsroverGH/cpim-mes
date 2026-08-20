<template>
  <div>
    <v-card class="mb-4">
      <v-card-title class="d-flex align-center">
        Sales Order / Customer Order Management
        <v-spacer />
        <v-btn v-if="canManage" class="mr-2" prepend-icon="mdi-account-plus" variant="outlined" @click="customerDialog = true">顧客登録</v-btn>
        <v-btn v-if="canManage" color="primary" prepend-icon="mdi-plus" @click="openOrderDialog">受注登録</v-btn>
      </v-card-title>
      <v-card-text>
        <v-alert type="info" variant="tonal" density="compact">
          Confirm済みSales Orderの受注残がForecast Consumption / ATPの正式な確定需要になります。出荷は引当済み数量からのみ行えます。
        </v-alert>
      </v-card-text>
    </v-card>

    <v-card class="mb-4">
      <v-card-title>受注一覧</v-card-title>
      <v-card-text class="pb-0">
        <v-text-field v-model="search" label="検索" prepend-inner-icon="mdi-magnify" density="compact" clearable />
      </v-card-text>
      <v-data-table :headers="headers" :items="orders" :search="search" density="comfortable">
        <template #item.status="{ item }"><v-chip size="small" :color="statusColor(item.status)">{{ item.status }}</v-chip></template>
        <template #item.orderDate="{ item }">{{ fmtDate(item.orderDate) }}</template>
        <template #item.promisedDate="{ item }">{{ fmtDate(item.promisedDate) }}</template>
        <template #item.qty="{ item }">{{ num(item.shippedQty) }} / {{ num(item.allocatedQty) }} / {{ num(item.totalQty) }}</template>
        <template #item.actions="{ item }">
          <v-btn size="small" variant="text" @click="openDetail(item.id)">詳細</v-btn>
          <v-btn v-if="canManage && !['SHIPPED','CANCELLED'].includes(item.status)" size="small" color="secondary" variant="text" @click="openPromise(item.id)">納期回答</v-btn>
          <v-btn v-if="canManage && item.status === 'DRAFT'" size="small" color="primary" variant="text" @click="confirmOrder(item.id)">Confirm</v-btn>
          <v-btn v-if="canManage && !['SHIPPED','CANCELLED'].includes(item.status)" size="small" color="error" variant="text" @click="cancelOrder(item.id)">取消</v-btn>
        </template>
      </v-data-table>
    </v-card>

    <v-card>
      <v-card-title>顧客</v-card-title>
      <v-data-table :headers="customerHeaders" :items="customers" density="compact">
        <template #item.status="{ item }"><v-chip size="small" :color="item.status === 'ACTIVE' ? 'success' : 'error'">{{ item.status }}</v-chip></template>
        <template #item.actions="{ item }">
          <v-btn v-if="canManage" size="small" variant="text" @click="editCustomer(item)">編集</v-btn>
        </template>
      </v-data-table>
    </v-card>

    <v-dialog v-model="customerDialog" max-width="620">
      <v-card :title="customerForm.id ? '顧客編集' : '顧客登録'">
        <v-card-text>
          <v-text-field v-model="customerForm.customerNo" label="顧客コード" />
          <v-text-field v-model="customerForm.name" label="顧客名" />
          <v-select v-model="customerForm.status" :items="['ACTIVE','BLOCKED']" label="状態" />
          <v-textarea v-model="customerForm.shipTo" label="出荷先" rows="2" />
          <v-textarea v-model="customerForm.notes" label="備考" rows="2" />
        </v-card-text>
        <v-card-actions><v-spacer/><v-btn @click="customerDialog=false">キャンセル</v-btn><v-btn color="primary" @click="saveCustomer">保存</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="orderDialog" max-width="900">
      <v-card title="Sales Order登録">
        <v-card-text>
          <v-row>
            <v-col cols="12" md="4"><v-text-field v-model="orderForm.orderNo" label="受注番号" /></v-col>
            <v-col cols="12" md="4"><v-select v-model="orderForm.customerId" :items="activeCustomerOptions" item-title="label" item-value="id" label="顧客" /></v-col>
            <v-col cols="12" md="4"><v-text-field v-model="orderForm.orderDate" type="date" label="受注日" /></v-col>
            <v-col cols="12" md="4"><v-text-field v-model="orderForm.requestedDate" type="date" label="Requested Date" /></v-col>
            <v-col cols="12" md="4"><v-text-field v-model="orderForm.promisedDate" type="date" label="Promised Date" /></v-col>
            <v-col cols="12" md="4"><v-text-field v-model="orderForm.notes" label="備考" /></v-col>
          </v-row>
          <v-divider class="my-3" />
          <div class="d-flex align-center mb-2"><strong>明細</strong><v-spacer/><v-btn size="small" prepend-icon="mdi-plus" @click="addLine">明細追加</v-btn></div>
          <v-row v-for="(line, idx) in orderForm.lines" :key="idx" dense>
            <v-col cols="12" md="4"><v-select v-model="line.itemId" :items="sellableItems" item-title="label" item-value="id" label="品目" /></v-col>
            <v-col cols="4" md="2"><v-text-field v-model.number="line.quantity" type="number" min="0.000001" label="数量" /></v-col>
            <v-col cols="4" md="2"><v-text-field v-model.number="line.unitPrice" type="number" min="0" label="単価" /></v-col>
            <v-col cols="4" md="3"><v-text-field v-model="line.promisedDate" type="date" label="明細Promised" /></v-col>
            <v-col cols="12" md="1"><v-btn icon="mdi-delete" variant="text" color="error" @click="removeLine(idx)" /></v-col>
          </v-row>
        </v-card-text>
        <v-card-actions><v-spacer/><v-btn @click="orderDialog=false">キャンセル</v-btn><v-btn color="primary" @click="saveOrder">DRAFT保存</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="detailDialog" max-width="1100">
      <v-card v-if="detail" :title="`${detail.order.orderNo} – ${detail.order.customerName}`">
        <v-card-text>
          <v-row class="mb-2">
            <v-col cols="6" md="3"><strong>Status:</strong> {{ detail.order.status }}</v-col>
            <v-col cols="6" md="3"><strong>Requested:</strong> {{ fmtDate(detail.order.requestedDate) }}</v-col>
            <v-col cols="6" md="3"><strong>Promised:</strong> {{ fmtDate(detail.order.promisedDate) }}</v-col>
            <v-col cols="6" md="3"><strong>Open:</strong> {{ num(detail.order.openQty) }}</v-col>
          </v-row>
          <v-data-table :headers="lineHeaders" :items="detail.lines" density="compact">
            <template #item.qty="{ item }">{{ num(item.shippedQty) }} / {{ num(item.allocatedQty) }} / {{ num(item.quantity) }}</template>
            <template #item.promisedDate="{ item }">{{ fmtDate(item.promisedDate) }}</template>
            <template #item.actions="{ item }">
              <v-btn v-if="canManage && canAllocate(detail.order.status) && item.openQty-item.allocatedQty > 0" size="small" variant="text" @click="allocateLine(item)">引当</v-btn>
              <v-btn v-if="canManage && canAllocate(detail.order.status) && item.allocatedQty > 0" size="small" variant="text" @click="releaseLine(item)">解除</v-btn>
              <v-btn v-if="canShip && canAllocate(detail.order.status) && item.allocatedQty > 0" size="small" color="success" variant="text" @click="shipLine(item)">出荷</v-btn>
            </template>
          </v-data-table>

          <v-divider class="my-4"/><h3 class="mb-2">Status History</h3>
          <v-table density="compact"><thead><tr><th>遷移</th><th>実行者</th><th>日時</th><th>Source</th></tr></thead><tbody>
            <tr v-for="h in detail.history" :key="h.id"><td>{{ h.fromStatus || '-' }} → {{ h.toStatus }}</td><td>{{ h.actorUsername }}</td><td>{{ fmtTime(h.occurredAt) }}</td><td>{{ h.source }}</td></tr>
          </tbody></v-table>

          <v-divider class="my-4"/><h3 class="mb-2">Shipments</h3>
          <v-table density="compact"><thead><tr><th>Shipment ID</th><th>数量</th><th>実行者</th><th>日時</th><th>Carrier / Tracking</th></tr></thead><tbody>
            <tr v-for="s in detail.shipments" :key="s.shipmentId"><td>{{ s.shipmentId }}</td><td>{{ num(s.quantity) }}</td><td>{{ s.shippedByUsername }}</td><td>{{ fmtTime(s.shippedAt) }}</td><td>{{ s.carrier }} {{ s.trackingNo }}</td></tr>
          </tbody></v-table>
        </v-card-text>
        <v-card-actions><v-spacer/><v-btn @click="detailDialog=false">閉じる</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="promiseDialog" max-width="1150">
      <v-card title="Advanced Order Promising / ATP + CTP">
        <v-card-text>
          <v-alert type="info" variant="tonal" density="compact" class="mb-3">
            納期回答はWhat-ifです。確定するまで在庫・WO・PO・Detailed Scheduleは変更しません。ATP不足分だけをCTPで材料・有限能力から評価します。
          </v-alert>
          <div class="d-flex align-center ga-3 mb-3">
            <v-text-field v-model.number="promiseHorizon" type="number" min="1" max="366" label="CTP Horizon（日）" density="compact" style="max-width:220px" />
            <v-btn :loading="promiseLoading" color="secondary" prepend-icon="mdi-refresh" @click="runPromise">再計算</v-btn>
            <v-chip v-if="promiseResult?.acceptance" color="success">PROMISED</v-chip>
            <v-chip v-else-if="promiseResult" color="info">WHAT-IF</v-chip>
          </div>
          <v-data-table v-if="promiseResult" :headers="promiseHeaders" :items="promiseResult.lines" density="compact">
            <template #item.itemCode="{ item }">{{ promiseLineCode(item.salesOrderLineId) }}</template>
            <template #item.requestedQty="{ item }">{{ num(item.requestedQty) }}</template>
            <template #item.atpQty="{ item }">{{ num(item.atpQty) }}</template>
            <template #item.ctpQty="{ item }">{{ num(item.ctpQty) }}</template>
            <template #item.requestedDate="{ item }">{{ fmtDate(item.requestedDate) }}</template>
            <template #item.materialReadyDate="{ item }">{{ fmtDate(item.materialReadyDate) }}</template>
            <template #item.capacityReadyDate="{ item }">{{ fmtDate(item.capacityReadyDate) }}</template>
            <template #item.earliestFullDate="{ item }">{{ fmtDate(item.earliestFullDate) }}</template>
            <template #item.constraintType="{ item }"><v-chip size="small" :color="constraintColor(item.constraintType)">{{ item.constraintType }}</v-chip></template>
          </v-data-table>

          <v-divider v-if="promiseResult" class="my-4" />
          <h3 v-if="promiseResult" class="mb-2">分納回答案</h3>
          <v-table v-if="promiseResult" density="compact">
            <thead><tr><th>品目</th><th>Seq</th><th>数量</th><th>回答日</th><th>Source</th></tr></thead>
            <tbody>
              <tr v-for="c in promiseResult.confirmations" :key="c.id || `${c.salesOrderLineId}-${c.sequenceNo}`">
                <td>{{ promiseLineCode(c.salesOrderLineId) }}</td><td>{{ c.sequenceNo }}</td><td>{{ num(c.quantity) }}</td><td>{{ fmtDate(c.confirmedDate) }}</td><td>{{ c.source }}</td>
              </tr>
            </tbody>
          </v-table>
          <v-alert v-if="promiseResult && !promiseFullyCovered" type="warning" variant="tonal" density="compact" class="mt-3">
            Horizon内で全量確約できない明細があります。Promise確定はできません。
          </v-alert>
          <div v-if="promiseResult" class="text-caption mt-3">Run: {{ promiseResult.run.id }} / Hash: {{ promiseResult.run.resultHash || '-' }}</div>
        </v-card-text>
        <v-card-actions>
          <v-spacer/>
          <v-btn @click="promiseDialog=false">閉じる</v-btn>
          <v-btn v-if="canManage && promiseResult && !promiseResult.acceptance" :disabled="!promiseFullyCovered" :loading="promiseLoading" color="primary" prepend-icon="mdi-check-decagram" @click="acceptPromise">Promise確定</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snackbar" :color="errorMessage ? 'error' : 'success'">{{ errorMessage || '完了しました' }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ItemsApi, SalesOrdersApi, type Customer, type Item, type OrderPromiseResult, type SalesOrder, type SalesOrderCreateInput, type SalesOrderDetail, type SalesOrderLine } from '@/api'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const customers = ref<Customer[]>([])
const orders = ref<SalesOrder[]>([])
const items = ref<Item[]>([])
const detail = ref<SalesOrderDetail | null>(null)
const search = ref('')
const customerDialog = ref(false)
const orderDialog = ref(false)
const detailDialog = ref(false)
const promiseDialog = ref(false)
const promiseResult = ref<OrderPromiseResult | null>(null)
const promiseDetail = ref<SalesOrderDetail | null>(null)
const promiseOrderId = ref('')
const promiseHorizon = ref(180)
const promiseLoading = ref(false)
const snackbar = ref(false)
const errorMessage = ref('')
const canManage = computed(() => auth.role === 'admin' || auth.role === 'planner')
const canShip = computed(() => auth.role === 'admin' || auth.role === 'planner' || auth.role === 'operator')

const customerForm = ref<Partial<Customer>>({ customerNo: '', name: '', status: 'ACTIVE', shipTo: '', notes: '' })
const orderForm = ref<SalesOrderCreateInput>({ orderNo: '', customerId: '', orderDate: today(), requestedDate: today(), promisedDate: today(), notes: '', lines: [] })

const headers = [
  { title:'受注番号', key:'orderNo' }, { title:'顧客', key:'customerName' }, { title:'受注日', key:'orderDate' },
  { title:'Promised', key:'promisedDate' }, { title:'Status', key:'status' }, { title:'出荷 / 引当 / 受注', key:'qty' },
  { title:'Open', key:'openQty' }, { title:'操作', key:'actions', sortable:false }
]
const customerHeaders = [
  { title:'顧客コード',key:'customerNo' }, { title:'顧客名',key:'name' }, { title:'状態',key:'status' }, { title:'出荷先',key:'shipTo' }, { title:'操作',key:'actions',sortable:false }
]
const lineHeaders = [
  { title:'#',key:'lineNo' }, { title:'品目',key:'itemCode' }, { title:'出荷 / 引当 / 受注',key:'qty' }, { title:'Open',key:'openQty' },
  { title:'単価',key:'unitPrice' }, { title:'Promised',key:'promisedDate' }, { title:'操作',key:'actions',sortable:false }
]
const promiseHeaders = [
  { title:'品目',key:'itemCode' }, { title:'要求数量',key:'requestedQty' }, { title:'要求日',key:'requestedDate' },
  { title:'ATP',key:'atpQty' }, { title:'CTP',key:'ctpQty' }, { title:'材料可能日',key:'materialReadyDate' },
  { title:'能力可能日',key:'capacityReadyDate' }, { title:'全量回答日',key:'earliestFullDate' }, { title:'制約',key:'constraintType' }
]
const promiseFullyCovered = computed(() => !!promiseResult.value && promiseResult.value.lines.length > 0 && promiseResult.value.lines.every(l => !!l.earliestFullDate && l.atpQty + l.ctpQty + 1e-9 >= l.requestedQty))
const activeCustomerOptions = computed(() => customers.value.filter(c => c.status === 'ACTIVE').map(c => ({ id:c.id,label:`${c.customerNo} – ${c.name}` })))
const sellableItems = computed(() => items.value.filter(i => i.type === 'FG' || i.type === 'SA').map(i => ({ id:i.id!,label:`${i.code} – ${i.name}` })))

function today(){ return new Date().toISOString().slice(0,10) }
function fmtDate(v?:string){ return v ? new Date(v).toLocaleDateString('ja-JP') : '-' }
function fmtTime(v?:string){ return v ? new Date(v).toLocaleString('ja-JP') : '-' }
function num(v:number){ return Number(v || 0).toLocaleString('ja-JP',{maximumFractionDigits:3}) }
function statusColor(s:string){ return s==='SHIPPED'?'success':s==='CANCELLED'?'error':s==='DRAFT'?'grey':s==='PARTIALLY_SHIPPED'?'warning':'primary' }
function constraintColor(s:string){ return s==='NONE'?'success':s==='HORIZON'?'error':s==='CAPACITY'?'warning':s==='MATERIAL_AND_CAPACITY'?'error':'info' }
function promiseLineCode(id:string){ return promiseDetail.value?.lines.find(l=>l.id===id)?.itemCode || id.slice(0,8) }
function canAllocate(s:string){ return s==='CONFIRMED'||s==='PARTIALLY_SHIPPED' }
function notify(err?:unknown){ errorMessage.value = err ? String((err as any)?.response?.data?.message || (err as any)?.message || err) : ''; snackbar.value=true }

async function load(){ const [c,o,i]=await Promise.all([SalesOrdersApi.customers(),SalesOrdersApi.list(),ItemsApi.list()]); customers.value=c??[]; orders.value=o??[]; items.value=i??[] }
function editCustomer(c:Customer){ customerForm.value={...c}; customerDialog.value=true }
async function saveCustomer(){ try { const f=customerForm.value; if(f.id) await SalesOrdersApi.updateCustomer(f.id,f); else await SalesOrdersApi.createCustomer(f); customerDialog.value=false; customerForm.value={customerNo:'',name:'',status:'ACTIVE',shipTo:'',notes:''}; await load(); notify() } catch(e){ notify(e) } }
function addLine(){ orderForm.value.lines.push({itemId:'',quantity:1,unitPrice:0,promisedDate:orderForm.value.promisedDate}) }
function removeLine(i:number){ orderForm.value.lines.splice(i,1) }
function openOrderDialog(){ orderForm.value={orderNo:`SO-${Date.now()}`,customerId:activeCustomerOptions.value[0]?.id||'',orderDate:today(),requestedDate:today(),promisedDate:today(),notes:'',lines:[]}; addLine(); orderDialog.value=true }
async function saveOrder(){ try { const d=await SalesOrdersApi.create(orderForm.value); orderDialog.value=false; await load(); detail.value=d; detailDialog.value=true; notify() } catch(e){ notify(e) } }
async function openDetail(id:string){ try { detail.value=await SalesOrdersApi.get(id); detailDialog.value=true } catch(e){ notify(e) } }
async function confirmOrder(id:string){ try { detail.value=await SalesOrdersApi.confirm(id); await load(); notify() } catch(e){ notify(e) } }
async function cancelOrder(id:string){ if(!confirm('未出荷残を取消し、引当を解除します。よろしいですか？'))return; try { detail.value=await SalesOrdersApi.cancel(id); await load(); notify() } catch(e){ notify(e) } }
async function allocateLine(line:SalesOrderLine){ const raw=prompt('引当数量',String(Math.max(0,line.openQty-line.allocatedQty))); if(raw===null)return; const q=Number(raw); if(!(q>0))return; try{ detail.value=await SalesOrdersApi.allocate(line.id,q); await load(); notify() }catch(e){notify(e)} }
async function releaseLine(line:SalesOrderLine){ const raw=prompt('引当解除数量',String(line.allocatedQty)); if(raw===null)return; const q=Number(raw); if(!(q>0))return; try{ detail.value=await SalesOrdersApi.release(line.id,q); await load(); notify() }catch(e){notify(e)} }
async function shipLine(line:SalesOrderLine){ const raw=prompt('出荷数量',String(line.allocatedQty)); if(raw===null)return; const q=Number(raw); if(!(q>0))return; try{ detail.value=await SalesOrdersApi.ship(line.id,q); await load(); notify() }catch(e){notify(e)} }
async function openPromise(id:string){ promiseOrderId.value=id; promiseDialog.value=true; promiseResult.value=null; try{ promiseDetail.value=await SalesOrdersApi.get(id); await runPromise() }catch(e){notify(e)} }
async function runPromise(){ if(!promiseOrderId.value)return; promiseLoading.value=true; try{ promiseResult.value=await SalesOrdersApi.promiseCheck(promiseOrderId.value,promiseHorizon.value) }catch(e){notify(e)}finally{promiseLoading.value=false} }
async function acceptPromise(){ if(!promiseResult.value||!promiseOrderId.value)return; promiseLoading.value=true; try{ promiseResult.value=await SalesOrdersApi.promiseAccept(promiseOrderId.value,promiseResult.value.run.id); await load(); promiseDetail.value=await SalesOrdersApi.get(promiseOrderId.value); if(detail.value?.order.id===promiseOrderId.value) detail.value=promiseDetail.value; notify() }catch(e){notify(e)}finally{promiseLoading.value=false} }

onMounted(load)
</script>
