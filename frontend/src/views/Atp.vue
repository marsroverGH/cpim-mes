<template>
  <v-card>
    <v-card-title>ATP (Available-to-Promise) — 引当可能数量</v-card-title>
    <v-card-text>
      <p class="text-body-2 text-medium-emphasis">
        受注引当可能数量を期間別に算出します。
        <code>ATP[t] = ScheduledIn - CommittedOut</code> (1期目は期首在庫を含む)。
        累積ATP は新規受注に対して納期回答できる最大量です。
      </p>

      <v-row dense class="mt-2">
        <v-col cols="12" md="5">
          <v-select v-model="form.itemId" :items="itemOptions"
                    item-title="label" item-value="id" label="対象品目" />
        </v-col>
        <v-col cols="6" md="2">
          <v-text-field v-model.number="form.horizonDays" type="number"
                        label="期間 (日)" />
        </v-col>
        <v-col cols="6" md="2">
          <v-text-field v-model.number="form.bucketDays" type="number"
                        label="バケット (日)" />
        </v-col>
        <v-col cols="12" md="3" class="d-flex align-center">
          <v-btn color="primary" prepend-icon="mdi-calculator-variant" :loading="busy"
                 :disabled="!form.itemId" @click="run">ATP計算</v-btn>
        </v-col>
      </v-row>

      <v-divider class="my-3" />

      <div v-if="!result" class="text-medium-emphasis">
        品目を選択して「ATP計算」をクリックしてください
      </div>
      <div v-else>
        <div class="text-subtitle-1 mb-2">
          {{ result.itemCode }} — 累積ATP合計:
          <strong class="text-primary">
            {{ result.buckets.length ? result.buckets[result.buckets.length-1].cumulativeAtp.toFixed(0) : 0 }}
          </strong>
          <v-chip size="small" class="ml-3" color="warning">Safety Stock保護 {{ result.safetyStockProtected.toFixed(2) }}</v-chip>
          <v-chip v-if="result.serviceLevel" size="small" class="ml-2">Service {{ (result.serviceLevel*100).toFixed(1) }}%</v-chip>
          <span class="ml-2 text-caption text-medium-emphasis">{{ result.policyStatus }}</span>
        </div>

        <v-data-table :items="result.buckets" :headers="headers" density="compact" :items-per-page="-1">
          <template #item.period="{ item }">{{ fmt(item.period) }}</template>
          <template #item.scheduledIn="{ item }">
            <span :class="item.scheduledIn > 0 ? 'text-success' : ''">
              {{ item.scheduledIn > 0 ? '+' + item.scheduledIn : 0 }}
            </span>
          </template>
          <template #item.committedOut="{ item }">
            <span :class="item.committedOut > 0 ? 'text-error' : ''">
              {{ item.committedOut > 0 ? '-' + item.committedOut : 0 }}
            </span>
          </template>
          <template #item.atp="{ item }">
            <v-chip size="small" :color="item.atp > 0 ? 'primary' : 'grey'">
              {{ item.atp.toFixed(0) }}
            </v-chip>
          </template>
          <template #item.cumulativeAtp="{ item }">
            <strong>{{ item.cumulativeAtp.toFixed(0) }}</strong>
          </template>
          <template #item.endingProjected="{ item }">
            <span :class="item.endingProjected < 0 ? 'text-error' : ''">
              {{ item.endingProjected.toFixed(0) }}
            </span>
          </template>
        </v-data-table>
      </div>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ATPApi, ItemsApi, type ATPResult, type Item } from '@/api'

const items = ref<Item[]>([])
const result = ref<ATPResult | null>(null)
const busy = ref(false)
const form = ref({ itemId: '', horizonDays: 56, bucketDays: 7 })

const itemOptions = computed(() =>
  (items.value ?? []).map(i => ({ id: i.id!, label: `${i.code} – ${i.name}` })))

const headers = [
  { title: '期間',           key: 'period' },
  { title: '期首在庫',       key: 'startingOnHand',  align: 'end' as const },
  { title: '計画入庫',       key: 'scheduledIn',     align: 'end' as const },
  { title: '確定受注',       key: 'committedOut',    align: 'end' as const },
  { title: '期末見込',       key: 'endingProjected', align: 'end' as const },
  { title: 'ATP',            key: 'atp',             align: 'end' as const },
  { title: '累積ATP',        key: 'cumulativeAtp',   align: 'end' as const }
]

onMounted(async () => { items.value = await ItemsApi.list() })

async function run() {
  busy.value = true
  try {
    result.value = (await ATPApi.run(form.value.itemId, form.value.horizonDays, form.value.bucketDays)) ?? []
  } catch (e: any) {
    alert(e?.response?.data?.message || 'ATP計算に失敗しました')
  } finally { busy.value = false }
}

function fmt(d: string) { return new Date(d).toLocaleDateString('ja-JP') }
</script>
