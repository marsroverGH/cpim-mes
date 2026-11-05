<template>
  <div>
    <div class="d-flex align-center mb-4">
      <div>
        <h1 class="text-h5">User Administration</h1>
        <div class="text-body-2 text-medium-emphasis">ユーザー、Role、有効状態、パスワードを管理します。</div>
      </div>
      <v-spacer />
      <v-btn prepend-icon="mdi-account-plus" color="primary" @click="openCreate">ユーザー追加</v-btn>
    </div>

    <v-alert type="info" variant="tonal" class="mb-4">
      最後のactive adminは無効化・降格できません。Role変更と無効化は次のAPI requestから即時反映されます。
    </v-alert>

    <v-card>
      <v-data-table :headers="headers" :items="users" :loading="loading" density="compact">
        <template #item.role="{ item }">
          <v-select
            :model-value="item.role"
            :items="roles"
            density="compact"
            hide-details
            style="min-width: 130px"
            @update:model-value="changeRole(item, $event)"
          />
        </template>
        <template #item.isActive="{ item }">
          <v-switch
            :model-value="item.isActive"
            color="primary"
            hide-details
            density="compact"
            @update:model-value="changeActive(item, Boolean($event))"
          />
        </template>
        <template #item.createdAt="{ item }">{{ formatDate(item.createdAt) }}</template>
        <template #item.actions="{ item }">
          <v-btn size="small" variant="text" prepend-icon="mdi-key-variant" @click="openReset(item)">
            Reset Password
          </v-btn>
        </template>
      </v-data-table>
    </v-card>

    <v-dialog v-model="createDialog" max-width="560">
      <v-card title="Create User">
        <v-card-text>
          <v-text-field v-model="createForm.username" label="Username" autocomplete="off" />
          <v-select v-model="createForm.role" :items="roles" label="Role" />
          <v-text-field
            v-model="createForm.password"
            label="Initial Password"
            type="password"
            autocomplete="new-password"
            :hint="`${passwordBytes(createForm.password)} bytes (15–72 required)`"
            persistent-hint
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="createDialog=false">Cancel</v-btn>
          <v-btn color="primary" :loading="saving" :disabled="!createValid" @click="createUser">Create</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="resetDialog" max-width="560">
      <v-card :title="`Reset Password — ${resetTarget?.username || ''}`">
        <v-card-text>
          <v-alert type="warning" variant="tonal" class="mb-4">
            新しいパスワードは監査ログへ保存されません。
          </v-alert>
          <v-text-field
            v-model="resetPasswordValue"
            label="New Password"
            type="password"
            autocomplete="new-password"
            :hint="`${passwordBytes(resetPasswordValue)} bytes (15–72 required)`"
            persistent-hint
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn @click="resetDialog=false">Cancel</v-btn>
          <v-btn color="primary" :loading="saving" :disabled="!passwordValid(resetPasswordValue)" @click="resetPassword">Reset</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snackbar" :color="errorMessage ? 'error' : 'success'">
      {{ errorMessage || successMessage }}
    </v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { UserAdminApi, type ManagedUser, type UserRole } from '@/api'

const roles: UserRole[] = ['admin', 'planner', 'operator', 'viewer']
const headers = [
  { title: 'Username', key: 'username' },
  { title: 'Role', key: 'role' },
  { title: 'Active', key: 'isActive' },
  { title: 'Created', key: 'createdAt' },
  { title: '', key: 'actions', sortable: false }
]

const users = ref<ManagedUser[]>([])
const loading = ref(false)
const saving = ref(false)
const snackbar = ref(false)
const errorMessage = ref('')
const successMessage = ref('完了しました')
const createDialog = ref(false)
const createForm = ref<{ username: string; role: UserRole; password: string }>({ username: '', role: 'viewer', password: '' })
const resetDialog = ref(false)
const resetTarget = ref<ManagedUser | null>(null)
const resetPasswordValue = ref('')
const encoder = new TextEncoder()

const passwordBytes = (value: string) => encoder.encode(value).length
const passwordValid = (value: string) => passwordBytes(value) >= 15 && passwordBytes(value) <= 72
const createValid = computed(() => createForm.value.username.trim().length > 0 && passwordValid(createForm.value.password))

function messageOf(err: unknown) {
  const e = err as any
  const details = e?.response?.data?.details
  if (details && typeof details === 'object') return Object.values(details).join(' / ')
  return String(e?.response?.data?.message || e?.response?.data?.error || e?.message || err)
}
function notify(message: string, err?: unknown) {
  successMessage.value = message
  errorMessage.value = err ? messageOf(err) : ''
  snackbar.value = true
}
function formatDate(value: string) {
  return value ? new Date(value).toLocaleString('ja-JP') : '-'
}

async function loadUsers() {
  loading.value = true
  try {
    users.value = await UserAdminApi.list()
  } catch (err) {
    notify('', err)
  } finally {
    loading.value = false
  }
}
function openCreate() {
  createForm.value = { username: '', role: 'viewer', password: '' }
  createDialog.value = true
}
async function createUser() {
  if (!createValid.value) return
  saving.value = true
  try {
    await UserAdminApi.create({ ...createForm.value, username: createForm.value.username.trim() })
    createDialog.value = false
    await loadUsers()
    notify('ユーザーを作成しました')
  } catch (err) {
    notify('', err)
  } finally {
    saving.value = false
  }
}
async function changeRole(user: ManagedUser, value: unknown) {
  const role = String(value) as UserRole
  if (!roles.includes(role) || role === user.role) return
  try {
    await UserAdminApi.setRole(user.id, role)
    await loadUsers()
    notify('Roleを変更しました')
  } catch (err) {
    await loadUsers()
    notify('', err)
  }
}
async function changeActive(user: ManagedUser, isActive: boolean) {
  if (isActive === user.isActive) return
  try {
    await UserAdminApi.setActive(user.id, isActive)
    await loadUsers()
    notify(isActive ? 'ユーザーを有効化しました' : 'ユーザーを無効化しました')
  } catch (err) {
    await loadUsers()
    notify('', err)
  }
}
function openReset(user: ManagedUser) {
  resetTarget.value = user
  resetPasswordValue.value = ''
  resetDialog.value = true
}
async function resetPassword() {
  if (!resetTarget.value || !passwordValid(resetPasswordValue.value)) return
  saving.value = true
  try {
    await UserAdminApi.resetPassword(resetTarget.value.id, resetPasswordValue.value)
    resetDialog.value = false
    resetPasswordValue.value = ''
    notify('パスワードをリセットしました')
  } catch (err) {
    notify('', err)
  } finally {
    saving.value = false
  }
}

onMounted(loadUsers)
</script>
