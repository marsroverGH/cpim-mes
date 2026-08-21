<template>
  <div>
    <v-card class="mb-4">
      <v-card-title class="d-flex align-center">
        OEE / Production Performance / Actual Capacity Feedback
        <v-spacer />
        <v-btn prepend-icon="mdi-refresh" variant="tonal" class="mr-2" @click="loadAll">再読込</v-btn>
        <v-btn color="primary" prepend-icon="mdi-chart-timeline-variant" :loading="busy" @click="run">実績再計算</v-btn>
      </v-card-title>
      <v-card-text>
        <v-alert type="info" variant="tonal" density="compact" class="mb-3">
          Shop FloorのSTART/STOP/COMPLETE/SCRAP、工程標準時間、Maintenance実績からOEE・MTBF・MTTRを再構成します。
          作成されたCapacity FeedbackはDRAFTで、Planner/AdminがACTIVE化したものだけがCRP / Detailed Scheduling / CTPへ反映されます。
        </v-alert>
        <v-row dense>
          <v-col cols="12" md="3"><v-text-field v-model="windowStart" type="date" label="集計開始日" /></v-col>
          <v-col cols="12" md="3"><v-text-field v-model="windowEnd" type="date" label="集計終了日" /></v-col>
          <v-col cols="12" md="3"><v-text-field v-model.number="minCompletedOps" type="number" min="1" max="100" label="Feedback最低完了工程数" /></v-col>
        </v-row>
      </v-card-text>
    </v-card>

    <v-card class="mb-4">
      <v-card-title>Latest Work Center Performance</v-card-title>
      <v-data-table :items="latestResults" :headers="resultHeaders" density="compact">
        <template #item.oee="{item}"><v-chip size="small" :color="oeeColor(item.oee)">{{ pct(item.oee) }}</v-chip></template>
        <template #item.availability="{item}">{{ pct(item.availability) }}</template>
        <template #item.performance="{item}">{{ pct(item.performance) }}</template>
        <template #item.quality="{item}">{{ pct(item.quality) }}</template>
        <template #item.recommendedEfficiency="{item}">{{ item.recommendedEfficiency.toFixed(3) }}</template>
        <template #item.recommendedUtilization="{item}">{{ item.recommendedUtilization.toFixed(3) }}</template>
        <template #item.timeLoss="{item}">{{ Math.round(item.plannedProductionMinutes) }} / {{ Math.round(item.runTimeMinutes) }} / {{ Math.round(item.downtimeMinutes) }}</template>
        <template #item.loss="{item}">{{ Math.round(item.setupLossMinutes) }} / {{ Math.round(item.speedLossMinutes) }}</template>
        <template #item.mt="{item}">{{ Math.round(item.mtbfMinutes) }} / {{ Math.round(item.mttrMinutes) }}</template>
      </v-data-table>
    </v-card>

    <v-card class="mb-4">
      <v-card-title>Capacity Feedback Versions</v-card-title>
      <v-data-table :items="feedback" :headers="feedbackHeaders" density="compact">
        <template #item.status="{item}"><v-chip size="small" :color="statusColor(item.status)">{{ item.status }}</v-chip></template>
        <template #item.sourceOee="{item}">{{ pct(item.sourceOee) }}</template>
        <template #item.effectiveEfficiency="{item}">{{ item.effectiveEfficiency.toFixed(3) }}</template>
        <template #item.effectiveUtilization="{item}">{{ item.effectiveUtilization.toFixed(3) }}</template>
        <template #item.actions="{item}">
          <v-btn v-if="item.status==='DRAFT'" size="x-small" color="success" variant="tonal" class="mr-1" @click="activate(item)">ACTIVE化</v-btn>
          <v-btn v-if="item.status!=='ARCHIVED'" size="x-small" color="warning" variant="tonal" @click="archive(item)">Archive</v-btn>
        </template>
      </v-data-table>
    </v-card>

    <v-card>
      <v-card-title>Performance Run History</v-card-title>
      <v-data-table :items="runs" :headers="runHeaders" density="compact" @click:row="openRun">
        <template #item.window="{item}">{{ d(item.windowStart) }} → {{ d(item.windowEnd) }}</template>
        <template #item.resultHash="{item}"><code>{{ item.resultHash?.slice(0,12) || '' }}</code></template>
      </v-data-table>
    </v-card>

    <v-snackbar v-model="snack" :color="snackColor">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ProductionPerformanceApi, type CapacityFeedbackVersion, type ProductionPerformanceResult, type ProductionPerformanceRun } from '@/api'

const today = new Date().toISOString().slice(0,10)
const ago = new Date(Date.now()-30*86400000).toISOString().slice(0,10)
const windowStart=ref(ago), windowEnd=ref(today), minCompletedOps=ref(3), busy=ref(false)
const latestResults=ref<ProductionPerformanceResult[]>([]), runs=ref<ProductionPerformanceRun[]>([]), feedback=ref<CapacityFeedbackVersion[]>([])
const snack=ref(false), snackText=ref(''), snackColor=ref('success')
const resultHeaders=[
  {title:'WC',key:'workCenterCode'},{title:'Samples',key:'sampleCount'},{title:'Availability',key:'availability'},
  {title:'Performance',key:'performance'},{title:'Quality',key:'quality'},{title:'OEE',key:'oee'},
  {title:'Planned/Run/Down分',key:'timeLoss'},{title:'Good',key:'goodQuantity'},{title:'Scrap',key:'rejectQuantity'},{title:'Setup/Speed Loss分',key:'loss'},
  {title:'MTBF/MTTR分',key:'mt'},{title:'推奨Efficiency',key:'recommendedEfficiency'},{title:'推奨Utilization',key:'recommendedUtilization'},{title:'Confidence',key:'confidence'}
]
const feedbackHeaders=[
  {title:'WC',key:'workCenterCode'},{title:'Ver',key:'versionNo'},{title:'Status',key:'status'},{title:'Source OEE',key:'sourceOee'},
  {title:'Efficiency',key:'effectiveEfficiency'},{title:'Utilization',key:'effectiveUtilization'},{title:'Samples',key:'sampleCount'},
  {title:'Confidence',key:'confidence'},{title:'Effective',key:'effectiveFrom'},{title:'',key:'actions',sortable:false}
]
const runHeaders=[{title:'期間',key:'window'},{title:'Min Ops',key:'minCompletedOps'},{title:'Status',key:'status'},{title:'作成者',key:'generatedBy'},{title:'Hash',key:'resultHash'}]
async function loadAll(){
  runs.value=(await ProductionPerformanceApi.runs())??[]
  feedback.value=(await ProductionPerformanceApi.feedback())??[]
  if(runs.value.length){const x=await ProductionPerformanceApi.getRun(runs.value[0].id); latestResults.value=x.results??[]}
}
onMounted(loadAll)
async function run(){busy.value=true;try{const x=await ProductionPerformanceApi.run({windowStart:windowStart.value,windowEnd:windowEnd.value,minCompletedOps:minCompletedOps.value});latestResults.value=x.results??[];await loadAll();message('OEE / Capacity Feedbackを再計算しました')}catch(e:any){message(e?.response?.data?.error||String(e),'error')}finally{busy.value=false}}
async function openRun(_e:Event,row:any){const id=row?.item?.id??row?.id;if(!id)return;const x=await ProductionPerformanceApi.getRun(id);latestResults.value=x.results??[]}
async function activate(x:CapacityFeedbackVersion){try{await ProductionPerformanceApi.activate(x.id,{effectiveFrom:String(x.effectiveFrom).slice(0,10),notes:'Activated from OEE review'});await loadAll();message(`${x.workCenterCode} feedbackをACTIVE化しました`)}catch(e:any){message(e?.response?.data?.error||String(e),'error')}}
async function archive(x:CapacityFeedbackVersion){try{await ProductionPerformanceApi.archive(x.id,'Archived from OEE review');await loadAll();message(`${x.workCenterCode} feedbackをArchiveしました`)}catch(e:any){message(e?.response?.data?.error||String(e),'error')}}
function pct(v:number){return `${((v||0)*100).toFixed(1)}%`}
function d(v?:string){return v?new Date(v).toLocaleDateString('ja-JP'):'—'}
function oeeColor(v:number){return v>=.85?'success':v>=.65?'warning':'error'}
function statusColor(s:string){return s==='ACTIVE'?'success':s==='DRAFT'?'info':'grey'}
function message(t:string,c='success'){snackText.value=t;snackColor.value=c;snack.value=true}
</script>
