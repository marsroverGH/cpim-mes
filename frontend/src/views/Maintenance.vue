<template>
  <div>
    <v-card class="mb-4">
      <v-card-title class="d-flex align-center">
        Maintenance / Capacity Downtime
        <v-spacer />
        <v-btn color="primary" prepend-icon="mdi-tools" @click="dialog=true">停止予定を登録</v-btn>
      </v-card-title>
      <v-card-text>
        <v-alert type="info" variant="tonal" density="compact" class="mb-3">
          Preventive Maintenance / Breakdown / Planned Downtime / Unplanned Downtime を設備・作業者能力から時間区間で減算します。Detailed Scheduling と CTP は同じ停止情報を使用します。
        </v-alert>
        <v-row dense>
          <v-col cols="12" md="4"><v-select v-model="filterWC" :items="wcItems" label="Work Center" clearable /></v-col>
          <v-col cols="12" md="3"><v-switch v-model="includeTerminal" label="完了/取消も表示" @update:model-value="load" /></v-col>
        </v-row>
        <v-data-table :items="filtered" :headers="headers" density="compact">
          <template #item.eventType="{item}"><v-chip size="small" :color="typeColor(item.eventType)">{{ item.eventType }}</v-chip></template>
          <template #item.status="{item}"><v-chip size="small" :color="statusColor(item.status)">{{ item.status }}</v-chip></template>
          <template #item.window="{item}">{{ dt(item.startAt) }} → {{ dt(item.endAt) }}</template>
          <template #item.capacity="{item}">M {{ item.unavailableMachines }} / W {{ item.unavailableWorkers }}</template>
          <template #item.actions="{item}">
            <v-btn v-if="item.status==='PLANNED'" size="x-small" color="warning" variant="text" @click="changeStatus(item,'ACTIVE')">開始</v-btn>
            <v-btn v-if="item.status==='PLANNED'||item.status==='ACTIVE'" size="x-small" color="success" variant="text" @click="changeStatus(item,'COMPLETED')">完了</v-btn>
            <v-btn v-if="item.status==='PLANNED'||item.status==='ACTIVE'" size="x-small" color="error" variant="text" @click="changeStatus(item,'CANCELLED')">取消</v-btn>
            <v-btn size="x-small" variant="text" @click="openDetail(item.id)">履歴</v-btn>
          </template>
        </v-data-table>
      </v-card-text>
    </v-card>

    <v-dialog v-model="dialog" max-width="850">
      <v-card title="Maintenance Event">
        <v-card-text>
          <v-row dense>
            <v-col cols="12" md="6"><v-select v-model="form.workCenterId" :items="wcItems" label="Work Center" /></v-col>
            <v-col cols="12" md="6"><v-select v-model="form.eventType" :items="eventTypes" label="Type" /></v-col>
            <v-col cols="12" md="6"><v-text-field v-model="form.startAt" type="datetime-local" label="開始" /></v-col>
            <v-col cols="12" md="6"><v-text-field v-model="form.endAt" type="datetime-local" label="終了" /></v-col>
            <v-col cols="12" md="3"><v-text-field v-model.number="form.unavailableMachines" type="number" min="0" label="停止設備台数" /></v-col>
            <v-col cols="12" md="3"><v-text-field v-model.number="form.unavailableWorkers" type="number" min="0" label="使用不可作業者" /></v-col>
            <v-col cols="12" md="6"><v-text-field v-model="form.sourceRef" label="Source Ref / Ticket" /></v-col>
            <v-col cols="12"><v-textarea v-model="form.reason" label="理由" rows="2" /></v-col>
          </v-row>
        </v-card-text>
        <v-card-actions><v-spacer/><v-btn @click="dialog=false">閉じる</v-btn><v-btn color="primary" :loading="busy" @click="create">登録</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="historyDialog" max-width="950">
      <v-card title="Maintenance Revision History">
        <v-card-text><v-data-table :items="detail?.revisions || []" :headers="historyHeaders" density="compact"><template #item.window="{item}">{{dt(item.startAt)}} → {{dt(item.endAt)}}</template></v-data-table></v-card-text>
        <v-card-actions><v-spacer/><v-btn @click="historyDialog=false">閉じる</v-btn></v-card-actions>
      </v-card>
    </v-dialog>
    <v-snackbar v-model="snack" :color="snackColor">{{snackText}}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { MaintenanceApi, WorkCentersApi, type CurrentMaintenanceEvent, type MaintenanceEventDetail, type MaintenanceEventType } from '@/api'
const rows=ref<CurrentMaintenanceEvent[]>([]), wcItems=ref<{title:string,value:string}[]>([]), filterWC=ref<string|null>(null), includeTerminal=ref(false)
const dialog=ref(false),historyDialog=ref(false),detail=ref<MaintenanceEventDetail|null>(null),busy=ref(false),snack=ref(false),snackText=ref(''),snackColor=ref('success')
const eventTypes:MaintenanceEventType[]=['PREVENTIVE_MAINTENANCE','BREAKDOWN','PLANNED_DOWNTIME','UNPLANNED_DOWNTIME']
const now=new Date(); const later=new Date(now.getTime()+2*3600000)
function localInput(d:Date){const z=new Date(d.getTime()-d.getTimezoneOffset()*60000);return z.toISOString().slice(0,16)}
const form=reactive({workCenterId:'',eventType:'PREVENTIVE_MAINTENANCE' as MaintenanceEventType,startAt:localInput(now),endAt:localInput(later),unavailableMachines:1,unavailableWorkers:0,reason:'',sourceRef:''})
const headers=[{title:'WC',key:'workCenterCode'},{title:'Type',key:'eventType'},{title:'Status',key:'status'},{title:'期間',key:'window'},{title:'能力減',key:'capacity'},{title:'理由',key:'reason'},{title:'Rev',key:'revisionNo'},{title:'操作',key:'actions',sortable:false}]
const historyHeaders=[{title:'Rev',key:'revisionNo'},{title:'Status',key:'status'},{title:'期間',key:'window'},{title:'Machines',key:'unavailableMachines'},{title:'Workers',key:'unavailableWorkers'},{title:'理由',key:'reason'},{title:'Actor',key:'actorUsername'}]
const filtered=computed(()=>filterWC.value?rows.value.filter(x=>x.workCenterId===filterWC.value):rows.value)
async function load(){rows.value=await MaintenanceApi.list(undefined,includeTerminal.value)}
onMounted(async()=>{const w=await WorkCentersApi.list();wcItems.value=w.filter(x=>x.id).map(x=>({title:`${x.code} / ${x.name}`,value:x.id!}));if(wcItems.value.length)form.workCenterId=wcItems.value[0].value;await load()})
async function create(){busy.value=true;try{await MaintenanceApi.create({...form,startAt:new Date(form.startAt).toISOString(),endAt:new Date(form.endAt).toISOString()});dialog.value=false;await load();ok('Maintenance eventを登録しました')}catch(e:any){fail(e)}finally{busy.value=false}}
async function changeStatus(item:CurrentMaintenanceEvent,status:'ACTIVE'|'COMPLETED'|'CANCELLED'){try{await MaintenanceApi.revise(item.id,{status});await load();ok(`${status} に更新しました`)}catch(e:any){fail(e)}}
async function openDetail(id:string){detail.value=await MaintenanceApi.get(id);historyDialog.value=true}
function dt(v:string){return new Date(v).toLocaleString('ja-JP')}
function typeColor(v:string){return v==='BREAKDOWN'||v==='UNPLANNED_DOWNTIME'?'error':v==='PREVENTIVE_MAINTENANCE'?'primary':'warning'}
function statusColor(v:string){return v==='ACTIVE'?'error':v==='COMPLETED'?'success':v==='CANCELLED'?'default':'warning'}
function ok(m:string){snackText.value=m;snackColor.value='success';snack.value=true} function fail(e:any){snackText.value=e?.response?.data?.error||e?.message||String(e);snackColor.value='error';snack.value=true}
</script>
