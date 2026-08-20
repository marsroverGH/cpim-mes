<template>
  <v-card>
    <v-card-title class="d-flex align-center">
      <v-icon icon="mdi-robot-outline" class="mr-2" />
      AI アシスタント (実験的)
    </v-card-title>
    <v-card-text>
      <p class="text-body-2 text-medium-emphasis">
        自然言語で在庫・計画・WO・ABC・KPI を尋ねられます。
        現在はルールベースの意図解析を使用しており、後で LLM (Anthropic API)
        への差し替えが可能です。
      </p>

      <v-form @submit.prevent="ask">
        <v-text-field v-model="query" prepend-inner-icon="mdi-message-text-outline"
                      label="質問を入力 (例: BIKE-100 の在庫)"
                      :loading="busy" clearable autofocus />
      </v-form>

      <div v-if="suggestions.length" class="mt-2">
        <v-chip v-for="s in suggestions" :key="s" size="small" variant="outlined"
                class="mr-1 mb-1" @click="quickAsk(s)">{{ s }}</v-chip>
      </div>

      <v-divider class="my-3" />

      <div v-if="!history.length" class="text-medium-emphasis text-body-2">
        質問を入力すると、ここに対話履歴が表示されます。
        まずは「/help」と入力してみてください。
      </div>

      <div v-for="(h, i) in history" :key="i" class="mb-3">
        <div class="text-caption text-medium-emphasis">あなた</div>
        <v-card variant="outlined" class="mb-1"><v-card-text class="py-2">{{ h.q }}</v-card-text></v-card>
        <div class="text-caption text-medium-emphasis">アシスタント
          <v-chip size="x-small" variant="tonal" class="ml-1">{{ h.r.intent }}</v-chip>
        </div>
        <v-card variant="tonal" color="primary">
          <v-card-text class="py-2">
            <div>{{ h.r.summary }}</div>
            <div v-if="h.r.suggestions?.length" class="mt-2">
              <v-chip v-for="s in h.r.suggestions" :key="s" size="x-small" variant="outlined"
                      class="mr-1" @click="quickAsk(s)">{{ s }}</v-chip>
            </div>
          </v-card-text>
        </v-card>
      </div>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { AgentApi, type AgentResponse } from '@/api'

const query = ref('')
const busy = ref(false)
const history = ref<{ q: string; r: AgentResponse }[]>([])
const suggestions = ref<string[]>([
  'BIKE-100 の在庫', 'BIKE-100 の計画オーダ',
  '今週完成するWO', '遅延中のPO', 'ABC分析', 'KPI', '/help'
])

async function ask() {
  const q = query.value.trim()
  if (!q) return
  busy.value = true
  try {
    const r = await AgentApi.ask(q)
    history.value.unshift({ q, r })
    suggestions.value = r.suggestions || []
    query.value = ''
  } catch (e: any) {
    history.value.unshift({
      q, r: { intent: 'ERROR', summary: e?.response?.data?.message || e?.message || String(e) }
    })
  } finally {
    busy.value = false
  }
}

function quickAsk(s: string) {
  query.value = s
  ask()
}
</script>
