<template>
  <div class="d-flex flex-column ga-4">
    <v-card>
      <v-card-title class="d-flex align-center">
        S&amp;OP — 月次需給計画
        <v-spacer />
        <v-btn variant="outlined" prepend-icon="mdi-chart-donut" @click="openMix">Product Mix</v-btn>
        <v-btn class="ml-2" variant="text" prepend-icon="mdi-plus" @click="openNew">プラン追加</v-btn>
      </v-card-title>
      <v-card-text>
        <p class="text-body-2 text-medium-emphasis mb-3">
          Family × 月次のSupply Planを、ACTIVE Product Mixと7日bucketの日数比でItem-level MPSへ展開します。
        </p>
        <v-data-table :items="plans" :headers="headers" density="comfortable">
          <template #item.groupId="{ item }">{{ groupName(item.groupId) }}</template>
          <template #item.planMonth="{ item }">{{ fmtMonth(item.planMonth) }}</template>
          <template #item.gap="{ item }">
            <span :class="(item.supplyQty - item.demandQty) < 0 ? 'text-error' : 'text-success'">
              {{ (item.supplyQty - item.demandQty).toLocaleString() }}
            </span>
          </template>
          <template #item.lastDisaggregation="{ item }">
            <span v-if="lastRun(item.id)">{{ formatRun(lastRun(item.id)) }}</span>
            <span v-else class="text-medium-emphasis">未展開</span>
          </template>
          <template #item.actions="{ item }">
            <v-btn icon="mdi-source-branch" size="x-small" variant="text" color="primary"
                   title="MPSへ展開" @click="openDisaggregation(item)" />
            <v-btn icon="mdi-pencil" size="x-small" variant="text" @click="openEdit(item)" />
            <v-btn icon="mdi-delete" size="x-small" variant="text" color="error" @click="remove(item)" />
          </template>
        </v-data-table>
      </v-card-text>
    </v-card>

    <v-card>
      <v-card-title>Product Mix Version</v-card-title>
      <v-card-text>
        <v-data-table :items="mixVersions" :headers="mixHeaders" density="compact">
          <template #item.groupId="{ item }">{{ groupName(item.groupId) }}</template>
          <template #item.version="{ item }">v{{ item.version }}</template>
          <template #item.lines="{ item }">
            {{ (item.lines || []).map(x => `${itemCode(x.itemId)} ${x.mixPct}%`).join(' / ') }}
          </template>
          <template #item.status="{ item }">
            <v-chip size="small" :color="item.status === 'ACTIVE' ? 'success' : item.status === 'DRAFT' ? 'warning' : undefined">
              {{ item.status }}
            </v-chip>
          </template>
          <template #item.actions="{ item }">
            <v-btn v-if="item.status === 'DRAFT'" size="small" variant="text" color="primary" @click="activateMix(item)">ACTIVE化</v-btn>
          </template>
        </v-data-table>
      </v-card-text>
    </v-card>

    <v-dialog v-model="dialog" max-width="500">
      <v-card :title="form.id ? 'S&OP プラン編集' : '新規 S&OP プラン'">
        <v-card-text>
          <v-select v-model="form.groupId" :items="groupOptions" item-title="label" item-value="id" label="ファミリー" />
          <v-text-field v-model="form.planMonth" type="month" label="対象月" />
          <v-text-field v-model.number="form.demandQty" type="number" label="需要数量" />
          <v-text-field v-model.number="form.supplyQty" type="number" label="供給数量（MPS展開元）" />
          <v-text-field v-model.number="form.inventoryTarget" type="number" label="在庫目標" />
          <v-textarea v-model="form.notes" label="備考" rows="2" />
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn @click="dialog=false">キャンセル</v-btn><v-btn color="primary" @click="save">保存</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="mixDialog" max-width="760">
      <v-card title="Product Mix Version作成">
        <v-card-text>
          <v-select v-model="mixGroupId" :items="groupOptions" item-title="label" item-value="id" label="ファミリー" @update:model-value="resetMixLines" />
          <v-text-field v-model="mixName" label="Version名" placeholder="例: 2026秋 標準Mix" />
          <v-alert v-if="mixEligibleItems.length === 0 && mixGroupId" type="warning" variant="tonal" class="mb-3">
            このFamilyにMPS対象のFG/SA品目がありません。
          </v-alert>
          <v-table v-if="mixEligibleItems.length" density="compact">
            <thead><tr><th>品目</th><th style="width:180px">Mix %</th></tr></thead>
            <tbody>
              <tr v-for="i in mixEligibleItems" :key="i.id">
                <td>{{ i.code }} — {{ i.name }}</td>
                <td><v-text-field v-model.number="mixPct[i.id!]" type="number" min="0" max="100" density="compact" hide-details /></td>
              </tr>
            </tbody>
          </v-table>
          <div class="text-right mt-2" :class="Math.abs(mixTotal-100) < 0.000001 ? 'text-success' : 'text-error'">
            合計: {{ mixTotal.toFixed(3) }}%
          </div>
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn @click="mixDialog=false">キャンセル</v-btn><v-btn color="primary" :disabled="Math.abs(mixTotal-100)>0.000001" @click="createMix">DRAFT作成</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="disaggDialog" max-width="900">
      <v-card title="S&OP → MPS Disaggregation">
        <v-card-text v-if="disaggPlan">
          <v-alert type="info" variant="tonal" class="mb-3">
            {{ groupName(disaggPlan.groupId) }} / {{ fmtMonth(disaggPlan.planMonth) }} / 月次Supply {{ disaggPlan.supplyQty.toLocaleString() }}
          </v-alert>
          <v-select v-model="selectedMixId" :items="activeMixOptions" item-title="label" item-value="id" label="ACTIVE Product Mix" @update:model-value="loadPreview" />
          <v-alert v-if="activeMixOptions.length===0" type="warning" variant="tonal">ACTIVE Product Mixを先に作成してください。</v-alert>
          <v-data-table v-if="preview" :items="preview.lines" :headers="previewHeaders" density="compact" class="mt-3">
            <template #item.itemId="{item}">{{ itemCode(item.itemId) }}</template>
            <template #item.period="{item}">{{ fmtDate(item.period) }}</template>
            <template #item.timeWeight="{item}">{{ (item.timeWeight*100).toFixed(2) }}%</template>
            <template #item.plannedQty="{item}">{{ item.plannedQty.toLocaleString() }}</template>
          </v-data-table>
          <div v-if="preview" class="text-caption text-medium-emphasis mt-2">
            配賦合計: {{ previewTotal.toLocaleString() }} / S&OP Supply: {{ preview.supplyQty.toLocaleString() }}
          </div>
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn @click="disaggDialog=false">閉じる</v-btn><v-btn color="primary" :disabled="!preview || applying" :loading="applying" @click="applyDisaggregation">MPSへ反映</v-btn></v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ItemsApi, SOPApi, type Item, type ItemGroup, type SOPPlan, type SOPProductMixVersion, type SOPDisaggregationPreview, type SOPDisaggregationRun } from '@/api'

const groups=ref<ItemGroup[]>([]), plans=ref<SOPPlan[]>([]), items=ref<Item[]>([])
const mixVersions=ref<SOPProductMixVersion[]>([]), runs=ref<SOPDisaggregationRun[]>([])
const dialog=ref(false), mixDialog=ref(false), disaggDialog=ref(false), applying=ref(false)
const form=ref<SOPPlan>(blank())
const mixGroupId=ref(''), mixName=ref(''), mixPct=ref<Record<string,number>>({})
const disaggPlan=ref<SOPPlan|null>(null), selectedMixId=ref(''), preview=ref<SOPDisaggregationPreview|null>(null)

function blank():SOPPlan{return{groupId:'',planMonth:new Date().toISOString().slice(0,7),demandQty:0,supplyQty:0,inventoryTarget:0,notes:''}}
const groupOptions=computed(()=>groups.value.map(g=>({id:g.id!,label:`${g.code} — ${g.name}`})))
const mixEligibleItems=computed(()=>items.value.filter(i=>i.groupId===mixGroupId.value&&(i.type==='FG'||i.type==='SA')))
const mixTotal=computed(()=>mixEligibleItems.value.reduce((s,i)=>s+Number(mixPct.value[i.id!]||0),0))
const activeMixOptions=computed(()=>mixVersions.value.filter(x=>x.groupId===disaggPlan.value?.groupId&&x.status==='ACTIVE').map(x=>({id:x.id,label:`v${x.version} ${x.name||''} — ${(x.lines||[]).map(l=>`${itemCode(l.itemId)} ${l.mixPct}%`).join(' / ')}`})))
const previewTotal=computed(()=>preview.value?.lines.reduce((s,x)=>s+x.plannedQty,0)??0)

const headers=[
 {title:'ファミリー',key:'groupId'},{title:'対象月',key:'planMonth'},{title:'需要',key:'demandQty',align:'end' as const},
 {title:'供給',key:'supplyQty',align:'end' as const},{title:'GAP',key:'gap',align:'end' as const,sortable:false},
 {title:'目標在庫',key:'inventoryTarget',align:'end' as const},{title:'最終MPS展開',key:'lastDisaggregation',sortable:false},{title:'',key:'actions',sortable:false,align:'end' as const}
]
const mixHeaders=[{title:'Family',key:'groupId'},{title:'Version',key:'version'},{title:'名称',key:'name'},{title:'Mix',key:'lines',sortable:false},{title:'状態',key:'status'},{title:'',key:'actions',sortable:false}]
const previewHeaders=[{title:'品目',key:'itemId'},{title:'MPS期間',key:'period'},{title:'Mix %',key:'mixPct',align:'end' as const},{title:'時間配賦',key:'timeWeight',align:'end' as const},{title:'MPS計画数',key:'plannedQty',align:'end' as const}]

async function load(){const [g,p,i,m,r]=await Promise.all([SOPApi.groups(),SOPApi.plans(),ItemsApi.list(),SOPApi.mixVersions(),SOPApi.disaggregationRuns()]);groups.value=g??[];plans.value=p??[];items.value=i??[];mixVersions.value=m??[];runs.value=r??[]}
onMounted(load)
function groupName(id:string){const g=groups.value.find(x=>x.id===id);return g?g.code:id?.slice(0,8)}
function itemCode(id:string){const i=items.value.find(x=>x.id===id);return i?i.code:id.slice(0,8)}
function lastRun(planId?:string){return runs.value.find(r=>r.sopPlanId===planId)}
function formatRun(r:SOPDisaggregationRun|undefined){return r?`${fmtDateTime(r.appliedAt)} / ${r.appliedBy}`:'未展開'}
function openNew(){form.value=blank();dialog.value=true}
function openEdit(p:SOPPlan){form.value={...p,planMonth:p.planMonth.slice(0,7)};dialog.value=true}
async function save(){const m=form.value.planMonth;await SOPApi.upsert({...form.value,planMonth:m.length===7?`${m}-01`:m});dialog.value=false;await load()}
async function remove(p:SOPPlan){if(!p.id||!confirm('削除しますか？'))return;await SOPApi.remove(p.id);await load()}
function openMix(){mixGroupId.value=groups.value[0]?.id||'';mixName.value='';resetMixLines();mixDialog.value=true}
function resetMixLines(){mixPct.value={};const eligible=items.value.filter(i=>i.groupId===mixGroupId.value&&(i.type==='FG'||i.type==='SA'));if(eligible.length){const each=100/eligible.length;eligible.forEach((i,idx)=>mixPct.value[i.id!]=idx===eligible.length-1?100-each*(eligible.length-1):each)}}
async function createMix(){const lines=mixEligibleItems.value.map(i=>({itemId:i.id!,mixPct:Number(mixPct.value[i.id!]||0)})).filter(x=>x.mixPct>0);await SOPApi.createMixVersion({groupId:mixGroupId.value,name:mixName.value,lines});mixDialog.value=false;await load()}
async function activateMix(m:SOPProductMixVersion){if(!confirm(`v${m.version}をACTIVEにしますか？ 現在のACTIVEはARCHIVEDになります。`))return;await SOPApi.activateMixVersion(m.id);await load()}
async function openDisaggregation(p:SOPPlan){disaggPlan.value=p;preview.value=null;selectedMixId.value='';disaggDialog.value=true;const active=mixVersions.value.find(x=>x.groupId===p.groupId&&x.status==='ACTIVE');if(active){selectedMixId.value=active.id;await loadPreview()}}
async function loadPreview(){if(!disaggPlan.value?.id||!selectedMixId.value){preview.value=null;return}preview.value=await SOPApi.previewDisaggregation(disaggPlan.value.id,selectedMixId.value)}
async function applyDisaggregation(){if(!disaggPlan.value?.id||!selectedMixId.value)return;if(!confirm('この配賦結果でMPSを更新しますか？'))return;applying.value=true;try{await SOPApi.disaggregate(disaggPlan.value.id,selectedMixId.value);disaggDialog.value=false;await load()}finally{applying.value=false}}
function fmtMonth(d:string){return new Date(d).toLocaleDateString('ja-JP',{year:'numeric',month:'2-digit'})}
function fmtDate(d:string){return d?new Date(d).toLocaleDateString('ja-JP'):''}
function fmtDateTime(d:string){return d?new Date(d).toLocaleString('ja-JP'):''}
</script>
