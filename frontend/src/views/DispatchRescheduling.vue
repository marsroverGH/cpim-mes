<template>
  <div>
    <div class="d-flex align-center mb-4"><h2>Real-Time Dispatch / Dynamic Rescheduling</h2><v-spacer/><v-btn :loading="busy" @click="load">更新</v-btn></div>
    <v-alert v-if="error" type="error" class="mb-3">{{ error }}</v-alert>
    <v-row class="mb-3">
      <v-col cols="12" md="3"><v-card><v-card-title>Active Schedule</v-card-title><v-card-text class="text-h6">{{ short(execution?.activeRunId) }}</v-card-text></v-card></v-col>
      <v-col cols="12" md="3"><v-card><v-card-title>Time Fence</v-card-title><v-card-text>Frozen {{ policy?.freezeMinutes ?? '-' }}m / Firm {{ policy?.firmMinutes ?? '-' }}m</v-card-text></v-card></v-col>
      <v-col cols="12" md="3"><v-card><v-card-title>Schedule Adherence</v-card-title><v-card-text>Start {{ fmtPct(adherence?.summary.onTimeStartPct) }} / Complete {{ fmtPct(adherence?.summary.onTimeCompletionPct) }}</v-card-text></v-card></v-col>
      <v-col cols="12" md="3"><v-card><v-card-title>Pending Signals</v-card-title><v-card-text class="text-h6">{{ signals.length }}</v-card-text></v-card></v-col>
    </v-row>
    <v-card class="mb-4">
      <v-card-title class="d-flex align-center">Dispatch List<v-spacer/><v-btn size="small" class="mr-2" :loading="busy" @click="snapshot">Adherence Snapshot</v-btn><v-btn size="small" color="primary" :loading="busy" @click="reschedule">再スケジュール</v-btn></v-card-title>
      <v-data-table :headers="dispatchHeaders" :items="board?.items || []" density="compact" :items-per-page="25">
        <template #item.timeFence="{ item }"><v-chip size="x-small" :color="fenceColor(item.timeFence)">{{ item.timeFence }}</v-chip></template>
        <template #item.dispatchStatus="{ item }"><v-chip size="x-small" :color="statusColor(item.dispatchStatus)">{{ item.dispatchStatus }}</v-chip></template>
        <template #item.plannedStart="{ item }">{{ dt(item.plannedStart) }}</template>
        <template #item.plannedEnd="{ item }">{{ dt(item.plannedEnd) }}</template>
      </v-data-table>
    </v-card>
    <v-card>
      <v-card-title>Dynamic Reschedule History</v-card-title>
      <v-data-table :headers="runHeaders" :items="runs" density="compact" :items-per-page="10">
        <template #item.status="{ item }"><v-chip size="x-small" :color="item.status==='ACTIVATED'?'success':item.status==='BLOCKED'?'error':'info'">{{ item.status }}</v-chip></template>
        <template #item.asOf="{ item }">{{ dt(item.asOf) }}</template>
      </v-data-table>
    </v-card>
  </div>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { DispatchApi, type DispatchBoard, type DispatchPolicyVersion, type ScheduleExecutionState, type ScheduleAdherenceResult, type DynamicRescheduleRun, type RescheduleSignal } from '@/api'
const busy=ref(false), error=ref(''); const board=ref<DispatchBoard|null>(null), policy=ref<DispatchPolicyVersion|null>(null), execution=ref<ScheduleExecutionState|null>(null), adherence=ref<ScheduleAdherenceResult|null>(null), runs=ref<DynamicRescheduleRun[]>([]), signals=ref<RescheduleSignal[]>([])
const dispatchHeaders=[{title:'WC',key:'workCenterCode'},{title:'WO',key:'orderNo'},{title:'Op',key:'operationSeq'},{title:'Item',key:'itemCode'},{title:'Fence',key:'timeFence'},{title:'Status',key:'dispatchStatus'},{title:'Plan Start',key:'plannedStart'},{title:'Plan End',key:'plannedEnd'},{title:'Start Δ min',key:'startVarianceMinutes'},{title:'Setup Match',key:'setupMatch'},{title:'Score',key:'dispatchScore'}]
const runHeaders=[{title:'As Of',key:'asOf'},{title:'Trigger',key:'triggerType'},{title:'Status',key:'status'},{title:'Frozen',key:'frozenConflicts'},{title:'Executed',key:'executionConflicts'},{title:'Firm',key:'firmChanges'},{title:'Flexible',key:'flexibleChanges'},{title:'Impacted WO',key:'impactedWorkOrders'}]
function dt(v?:string){return v?new Date(v).toLocaleString():''} function short(v?:string){return v?v.slice(0,8):'-'} function fmtPct(v?:number){return v==null?'-':`${v.toFixed(1)}%`}
function fenceColor(v:string){return v==='FROZEN'?'error':v==='FIRM'?'warning':v==='EXECUTED'?'grey':'success'} function statusColor(v:string){return v.startsWith('LATE')||v==='BLOCKED'?'error':v==='IN_PROCESS'?'primary':v==='READY'?'success':'default'}
async function load(){busy.value=true;error.value='';try{[board.value,policy.value,execution.value,adherence.value,runs.value,signals.value]=await Promise.all([DispatchApi.board(),DispatchApi.currentPolicy(),DispatchApi.execution(),DispatchApi.adherence(),DispatchApi.rescheduleRuns(),DispatchApi.signals()])}catch(e:any){error.value=e?.response?.data?.error||e.message}finally{busy.value=false}}
async function snapshot(){busy.value=true;try{adherence.value=await DispatchApi.snapshotAdherence();await load()}finally{busy.value=false}}
async function reschedule(){busy.value=true;try{await DispatchApi.reschedule({triggerType:'MANUAL',reason:'planner requested dynamic reschedule',horizonDays:28});await load()}catch(e:any){error.value=e?.response?.data?.error||e.message}finally{busy.value=false}}
onMounted(load)
</script>
