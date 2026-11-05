<template>
  <v-row justify="center">
    <v-col cols="12" md="7" lg="5">
      <v-card>
        <v-card-title class="d-flex align-center">
          <v-icon class="mr-2">mdi-account-key</v-icon>
          アカウント
        </v-card-title>
        <v-card-subtitle>{{ auth.username }} ({{ auth.role }})</v-card-subtitle>
        <v-card-text>
          <v-alert type="info" variant="tonal" class="mb-4">
            パスワードは15〜72 bytesで設定してください。変更後は現在のBrowser sessionを終了し、再ログインします。
          </v-alert>
          <v-form @submit.prevent="changePassword">
            <v-text-field
              v-model="currentPassword"
              label="現在のパスワード"
              type="password"
              autocomplete="current-password"
              :disabled="loading"
            />
            <v-text-field
              v-model="newPassword"
              label="新しいパスワード"
              type="password"
              autocomplete="new-password"
              :hint="`${passwordBytes(newPassword)} bytes`"
              persistent-hint
              :disabled="loading"
            />
            <v-text-field
              v-model="confirmPassword"
              label="新しいパスワード（確認）"
              type="password"
              autocomplete="new-password"
              :disabled="loading"
            />
            <v-btn
              color="primary"
              type="submit"
              :loading="loading"
              :disabled="!canSubmit"
            >
              パスワードを変更
            </v-btn>
          </v-form>
        </v-card-text>
      </v-card>
      <v-alert v-if="errorMessage" type="error" class="mt-4">{{ errorMessage }}</v-alert>
    </v-col>
  </v-row>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { UserAdminApi } from '@/api'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const loading = ref(false)
const errorMessage = ref('')

const encoder = new TextEncoder()
const passwordBytes = (value: string) => encoder.encode(value).length
const canSubmit = computed(() =>
  currentPassword.value.length > 0 &&
  passwordBytes(newPassword.value) >= 15 &&
  passwordBytes(newPassword.value) <= 72 &&
  newPassword.value === confirmPassword.value
)

function messageOf(err: unknown) {
  const e = err as any
  const details = e?.response?.data?.details
  if (details && typeof details === 'object') {
    return Object.values(details).join(' / ')
  }
  return String(e?.response?.data?.message || e?.response?.data?.error || e?.message || err)
}

async function changePassword() {
  if (!canSubmit.value) return
  loading.value = true
  errorMessage.value = ''
  try {
    await UserAdminApi.changeOwnPassword(currentPassword.value, newPassword.value)
    await auth.logout()
    await router.push('/login')
  } catch (err) {
    errorMessage.value = messageOf(err)
  } finally {
    loading.value = false
  }
}
</script>
