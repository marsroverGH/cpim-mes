<template>
  <div>
    <v-card>
      <v-card-title class="d-flex align-center">
        Detailed Scheduling
        <v-spacer />
        <v-btn color="primary" prepend-icon="mdi-calendar-multiselect" :loading="busy" @click="run">詳細日程を作成</v-btn>
      </v-card-title>
      <v-card-text>
        <v-row dense>
          <v-col cols="12" md="3"><v-text-field v-model="startDate" type="date" label="開始日" /></v-col>
          <v-col cols="12" md="3"><v-text-field v-model.number="horizon" type="number" min="1" max="366" label="期間(日)" /></v-col>
        </v-row>
        <v-alert type="info" variant="tonal" density="compact" class="mb-3">
          代替Work Center、Transfer Batch、工程Overlap、Sequence-dependent Setup、設備台数、作業者数を同時に考慮する有限Job Shopヒューリスティックです。
        </v-alert>

        <v-row v-if="result">
          <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">日程化</div><div class="text-h5">{{ result.summary.scheduledOrders }}</div></v-card-text></v-card></v-col>
          <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">遅延</div><div class="text-h5 text-warning">{{ result.summary.lateOrders }}</div></v-card-text></v-card></v-col>
          <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">未日程</div><div class="text-h5 text-error">{{ result.summary.unscheduledOrders }}</div></v-card-text></v-card></v-col>
          <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">代替WC使用</div><div class="text-h5">{{ result.summary.alternativeUses }}</div></v-card-text></v-card></v-col>
          <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">Transfer Batch</div><div class="text-h5">{{ result.summary.transferBatches }}</div></v-card-text></v-card></v-col>
          <v-col cols="6" md="2"><v-card variant="tonal"><v-card-text><div class="text-caption">Peak Workers/WC</div><div class="text-h5">{{ result.summary.peakWorkers }}</div></v-card-text></v-card></v-col>
        </v-row>

        <v-tabs v-if="result" v-model="tab" class="mt-4">
          <v-tab value="orders">オーダ</v-tab><v-tab value="batches">Transfer Batch</v-tab><v-tab value="segments">資源セグメント</v-tab><v-tab value="loads">設備負荷</v-tab><v-tab value="history">履歴</v-tab>
        </v-tabs>
        <v-window v-if="result" v-model="tab">
          <v-window-item value="orders">
            <v-data-table :items="result.orders" :headers="orderHeaders" density="compact">
              <template #item.scheduledStart="{item}">{{ dt(item.scheduledStart) }}</template><template #item.scheduledEnd="{item}">{{ dt(item.scheduledEnd) }}</template><template #item.dueAt="{item}">{{ dt(item.dueAt) }}</template>
              <template #item.scheduleStatus="{item}"><v-chip size="small" :color="statusColor(item.scheduleStatus)">{{ item.scheduleStatus }}</v-chip></template>
            </v-data-table>
          </v-window-item>
          <v-window-item value="batches">
            <v-data-table :items="result.batches" :headers="batchHeaders" density="compact">
              <template #item.workCenterCode="{item}"><v-chip size="x-small" :color="item.primaryWorkCenter ? 'default' : 'secondary'">{{ item.workCenterCode || '—' }}</v-chip></template>
              <template #item.machineLanes="{item}">{{ (item.machineLanes ?? []).join(',') || '—' }}</template>
              <template #item.scheduledStart="{item}">{{ dt(item.scheduledStart) }}</template><template #item.scheduledEnd="{item}">{{ dt(item.scheduledEnd) }}</template>
            </v-data-table>
          </v-window-item>
          <v-window-item value="segments">
            <v-data-table :items="result.segments" :headers="segmentHeaders" density="compact">
              <template #item.startAt="{item}">{{ dt(item.startAt) }}</template><template #item.endAt="{item}">{{ dt(item.endAt) }}</template><template #item.machineLanes="{item}">{{ (item.machineLanes ?? []).join(',') }}</template>
            </v-data-table>
          </v-window-item>
          <v-window-item value="loads"><v-data-table :items="result.loads" :headers="loadHeaders" density="compact"><template #item.date="{item}">{{ d(item.date) }}</template><template #item.loadPct="{item}">{{ item.loadPct.toFixed(0) }}%</template></v-data-table></v-window-item>
          <v-window-item value="history"><v-data-table :items="runs" :headers="runHeaders" density="compact" @click:row="openRun"><template #item.generatedAt="{item}">{{ dt(item.generatedAt) }}</template></v-data-table></v-window-item>
        </v-window>
      </v-card-text>
    </v-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { DetailedSchedulingApi, type DetailedScheduleResult, type DetailedScheduleRun } from '@/api'
const today = new Date().toISOString().slice(0,10)
const startDate = ref(today), horizon = ref(28), busy=ref(false), tab=ref('orders')
const result=ref<DetailedScheduleResult|null>(null), runs=ref<DetailedScheduleRun[]>([])
const orderHeaders=[{title:'区分',key:'sourceType'},{title:'参照',key:'sourceRef'},{title:'品目',key:'itemCode'},{title:'数量',key:'quantity'},{title:'開始',key:'scheduledStart'},{title:'終了',key:'scheduledEnd'},{title:'納期',key:'dueAt'},{title:'状態',key:'scheduleStatus'}]
const batchHeaders=[{title:'工程',key:'operationSeq'},{title:'Batch',key:'batchNo'},{title:'数量',key:'batchQty'},{title:'累計',key:'cumulativeQty'},{title:'WC',key:'workCenterCode'},{title:'設備Lane',key:'machineLanes'},{title:'作業者',key:'workersRequired'},{title:'Setup Family',key:'setupFamily'},{title:'Seq段取(分)',key:'sequenceSetupMinutes'},{title:'開始',key:'scheduledStart'},{title:'終了',key:'scheduledEnd'}]
const segmentHeaders=[{title:'工程',key:'operationSeq'},{title:'Batch',key:'batchNo'},{title:'種別',key:'segmentType'},{title:'WC',key:'workCenterCode'},{title:'設備Lane',key:'machineLanes'},{title:'作業者',key:'workersRequired'},{title:'開始',key:'startAt'},{title:'終了',key:'endAt'},{title:'分',key:'clockMinutes'}]
const loadHeaders=[{title:'日付',key:'date'},{title:'WC',key:'workCenterCode'},{title:'設備分',key:'requiredMinutes'},{title:'設備能力分',key:'availableMinutes'},{title:'負荷率',key:'loadPct'}]
const runHeaders=[{title:'開始',key:'startDate'},{title:'終了',key:'endDate'},{title:'期間',key:'horizonDays'},{title:'作成者',key:'generatedBy'},{title:'作成日時',key:'generatedAt'}]
async function loadRuns(){runs.value=(await DetailedSchedulingApi.runs())??[]}
onMounted(loadRuns)
async function run(){busy.value=true;try{result.value=await DetailedSchedulingApi.run({startDate:startDate.value,horizonDays:horizon.value});await loadRuns()}finally{busy.value=false}}
async function openRun(_e:Event,row:any){const id=row?.item?.id??row?.id;if(!id)return;result.value=await DetailedSchedulingApi.getRun(id);startDate.value=result.value.run.startDate.slice(0,10);horizon.value=result.value.run.horizonDays}
function statusColor(s:string){return s==='ON_TIME'?'success':s==='LATE'?'warning':'error'}
function dt(v?:string){return v?new Date(v).toLocaleString('ja-JP'):'—'}
function d(v?:string){return v?new Date(v).toLocaleDateString('ja-JP'):'—'}
</script>
