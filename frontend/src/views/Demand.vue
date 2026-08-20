<template>
  <v-card>
    <v-card-title>旧需要データ (Legacy demand_forecasts)</v-card-title>
    <v-card-text>
      <v-alert type="info" variant="tonal" class="mb-4">
        0031以降、このテーブルは参照専用です。新しい顧客需要はSales Orders画面から登録してください。ForecastはForecast画面のVersion管理を使用します。
      </v-alert>
      <v-text-field v-model="search" prepend-inner-icon="mdi-magnify" label="検索" clearable density="compact" />
    </v-card-text>
    <v-data-table :items="demands" :headers="headers" :search="search" density="comfortable">
      <template #item.itemCode="{ item }">{{ codeMap[item.itemId] || item.itemId }}</template>
      <template #item.dueDate="{ item }">{{ fmt(item.dueDate) }}</template>
    </v-data-table>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { DemandApi, ItemsApi, type Demand, type Item } from '@/api'
const items=ref<Item[]>([]); const demands=ref<Demand[]>([]); const search=ref('')
const codeMap=computed(()=>Object.fromEntries(items.value.filter(i=>i.id).map(i=>[i.id!,`${i.code} – ${i.name}`])))
const headers=[{title:'品目',key:'itemCode'},{title:'納期',key:'dueDate'},{title:'数量',key:'quantity'},{title:'種別',key:'source'}]
async function load(){ const [i,d]=await Promise.all([ItemsApi.list(),DemandApi.list()]); items.value=i??[];demands.value=d??[] }
function fmt(d:string){return d?new Date(d).toLocaleDateString('ja-JP'):''}
onMounted(load)
</script>
