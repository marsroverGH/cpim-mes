<template>
  <div>
  <v-card>
    <v-card-title class="d-flex align-center">
      監査ログ (Audit Log)
      <v-spacer />
      <v-btn variant="text" prepend-icon="mdi-refresh" @click="load">更新</v-btn>
    </v-card-title>
    <v-card-text>
      <v-row dense>
        <v-col cols="12" sm="4">
          <v-text-field v-model="filterUser" label="ユーザー名でフィルタ"
                        clearable density="compact" prepend-inner-icon="mdi-account" />
        </v-col>
        <v-col cols="12" sm="4">
          <v-text-field v-model="filterRes" label="リソースでフィルタ (例: items)"
                        clearable density="compact" prepend-inner-icon="mdi-folder-outline" />
        </v-col>
        <v-col cols="12" sm="4" class="d-flex align-center">
          <v-btn color="primary" prepend-icon="mdi-magnify" @click="load">検索</v-btn>
          <span class="ml-3 text-medium-emphasis">{{ rows.length }} 件</span>
        </v-col>
      </v-row>

      <v-data-table :items="rows" :headers="headers" density="compact"
                    :items-per-page="50" hover>
        <template #item.occurredAt="{ item }">{{ fmt(item.occurredAt) }}</template>
        <template #item.action="{ item }">
          <v-chip size="x-small" :color="methodColor(item.action)" class="mr-1">
            {{ method(item.action) }}
          </v-chip>
          <span class="text-body-2">{{ path(item.action) }}</span>
        </template>
        <template #item.httpStatus="{ item }">
          <v-chip size="x-small" :color="statusColor(item.httpStatus)">
            {{ item.httpStatus }}
          </v-chip>
        </template>
        <template #item.payload="{ item }">
          <v-btn v-if="item.payload" size="x-small" variant="text"
                 prepend-icon="mdi-eye" @click="showPayload(item)">表示</v-btn>
          <span v-else class="text-medium-emphasis">—</span>
        </template>
      </v-data-table>
    </v-card-text>
  </v-card>

  <v-dialog v-model="payloadDialog" max-width="700">
    <v-card title="リクエストペイロード">
      <v-card-text>
        <pre style="white-space: pre-wrap; font-size: 0.85em">{{ payloadJSON }}</pre>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="payloadDialog = false">閉じる</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { AuditApi, type AuditEntry } from '@/api'

const rows = ref<AuditEntry[]>([])
const filterUser = ref('')
const filterRes = ref('')

const payloadDialog = ref(false)
const payloadJSON = ref('')

const headers = [
  { title: '日時',     key: 'occurredAt' },
  { title: 'ユーザー', key: 'username' },
  { title: 'ロール',   key: 'userRole' },
  { title: '操作',     key: 'action' },
  { title: 'リソース', key: 'resource' },
  { title: 'ID',       key: 'resourceId' },
  { title: 'ステータス', key: 'httpStatus' },
  { title: 'IP',       key: 'ipAddress' },
  { title: 'Payload',  key: 'payload', sortable: false }
]

async function load() {
  rows.value = (await AuditApi.list({
    username: filterUser.value || undefined,
    resource: filterRes.value || undefined
  })) ?? []
}
onMounted(load)

function method(a: string) { return a.split(' ')[0] || '?' }
function path(a: string) { return a.split(' ').slice(1).join(' ') }

function methodColor(a: string) {
  const colors: Record<string, string> = { POST: 'success', PUT: 'info', DELETE: 'error', PATCH: 'warning' }
  return colors[method(a)] || 'grey'
}
function statusColor(s: number) {
  if (s >= 500) return 'error'
  if (s >= 400) return 'warning'
  if (s >= 200 && s < 300) return 'success'
  return 'grey'
}
function showPayload(e: AuditEntry) {
  payloadJSON.value = typeof e.payload === 'string'
    ? e.payload
    : JSON.stringify(e.payload, null, 2)
  payloadDialog.value = true
}
function fmt(d: string) { return new Date(d).toLocaleString('ja-JP') }
</script>
