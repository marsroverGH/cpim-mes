<template>
  <v-card>
    <v-card-title class="d-flex align-center">
      MRP アクションメッセージ
      <v-spacer />
      <v-text-field v-model.number="horizonDays" type="number"
                    style="max-width: 140px" density="compact" hide-details
                    label="期間 (日)" />
      <v-btn class="ml-2" color="primary" prepend-icon="mdi-refresh"
             :loading="busy" @click="load">再計算</v-btn>
    </v-card-title>
    <v-card-text>
      <p class="text-body-2 text-medium-emphasis">
        MRP v2 で既存 PO/WO を Scheduled Receipt として Netting した後の不足分に対し、
        Expedite / Release / Future Release の推奨アクションを提示します。
      </p>

      <v-row dense class="my-2">
        <v-col cols="12" sm="3">
          <v-card variant="tonal" color="error" density="compact">
            <v-card-text class="d-flex align-center">
              <v-icon icon="mdi-alert" class="mr-2" />
              <div>
                <div class="text-h5">{{ counts.CRITICAL }}</div>
                <div class="text-caption">CRITICAL</div>
              </div>
            </v-card-text>
          </v-card>
        </v-col>
        <v-col cols="12" sm="3">
          <v-card variant="tonal" color="warning" density="compact">
            <v-card-text class="d-flex align-center">
              <v-icon icon="mdi-alert-circle-outline" class="mr-2" />
              <div>
                <div class="text-h5">{{ counts.WARNING }}</div>
                <div class="text-caption">WARNING</div>
              </div>
            </v-card-text>
          </v-card>
        </v-col>
        <v-col cols="12" sm="3">
          <v-card variant="tonal" color="info" density="compact">
            <v-card-text class="d-flex align-center">
              <v-icon icon="mdi-information-outline" class="mr-2" />
              <div>
                <div class="text-h5">{{ counts.INFO }}</div>
                <div class="text-caption">INFO</div>
              </div>
            </v-card-text>
          </v-card>
        </v-col>
        <v-col cols="12" sm="3">
          <v-card variant="outlined" density="compact">
            <v-card-text class="d-flex align-center">
              <v-icon icon="mdi-clipboard-list-outline" class="mr-2" />
              <div>
                <div class="text-h5">{{ messages.length }}</div>
                <div class="text-caption">合計</div>
              </div>
            </v-card-text>
          </v-card>
        </v-col>
      </v-row>

      <v-data-table :items="messages" :headers="headers" density="comfortable">
        <template #item.severity="{ item }">
          <v-chip size="small" :color="severityColor(item.severity)">{{ item.severity }}</v-chip>
        </template>
        <template #item.kind="{ item }">
          <v-chip size="small" variant="tonal" :color="kindColor(item.kind)">{{ item.kind }}</v-chip>
        </template>
        <template #item.needDate="{ item }">{{ fmt(item.needDate) }}</template>
      </v-data-table>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ActionsApi, type ActionMessage } from '@/api'

const messages = ref<ActionMessage[]>([])
const horizonDays = ref(28)
const busy = ref(false)

const counts = computed(() => ({
  CRITICAL: (messages.value ?? []).filter(m => m.severity === 'CRITICAL').length,
  WARNING:  (messages.value ?? []).filter(m => m.severity === 'WARNING').length,
  INFO:     (messages.value ?? []).filter(m => m.severity === 'INFO').length
}))

const headers = [
  { title: '重要度', key: 'severity' },
  { title: '種別',   key: 'kind' },
  { title: '品目',   key: 'itemCode' },
  { title: '数量',   key: 'quantity', align: 'end' as const },
  { title: '必要日', key: 'needDate' },
  { title: 'メッセージ', key: 'message' }
]

async function load() {
  busy.value = true
  try {
    messages.value = (await ActionsApi.list(horizonDays.value)) ?? []
  } finally { busy.value = false }
}
onMounted(load)

function severityColor(s: string) {
  const colors: Record<string, string> = { CRITICAL: 'error', WARNING: 'warning', INFO: 'info' }
  return colors[s] || 'grey'
}
function kindColor(k: string) {
  if (k === 'EXPEDITE' || k === 'CANCEL') return 'error'
  if (k === 'RELEASE') return 'warning'
  if (k.startsWith('RESCHEDULE')) return 'primary'
  return 'grey'
}
function fmt(d: string) { return new Date(d).toLocaleDateString('ja-JP') }
</script>
