<template>
  <v-container class="fill-height" fluid>
    <v-row align="center" justify="center">
      <v-col cols="12" sm="8" md="5" lg="4">
        <v-card>
          <v-card-title class="text-h5 text-center pa-4">
            <v-icon icon="mdi-factory" size="32" class="mr-2" />
            CPIM-MES ログイン
          </v-card-title>
          <v-divider />
          <v-card-text class="pa-6">
            <v-text-field
              v-model="username"
              label="ユーザー名"
              prepend-inner-icon="mdi-account"
              autocomplete="username"
              @keyup.enter="submit"
            />
            <v-text-field
              v-model="password"
              label="パスワード"
              type="password"
              prepend-inner-icon="mdi-lock"
              autocomplete="current-password"
              @keyup.enter="submit"
            />
            <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mt-2">
              {{ error }}
            </v-alert>
            <v-alert type="info" variant="tonal" density="compact" class="mt-3">
              <strong>初期ユーザー:</strong> admin / admin123, planner / planner123,
              operator / operator123, viewer / viewer123
            </v-alert>
          </v-card-text>
          <v-card-actions class="px-6 pb-6">
            <v-btn block color="primary" :loading="busy" @click="submit">ログイン</v-btn>
          </v-card-actions>
        </v-card>
      </v-col>
    </v-row>
  </v-container>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const username = ref('admin')
const password = ref('admin123')
const error = ref('')
const busy = ref(false)
const auth = useAuthStore()
const router = useRouter()

async function submit() {
  busy.value = true
  error.value = ''
  try {
    await auth.login(username.value, password.value)
    router.push('/')
  } catch (e: any) {
    error.value = e?.response?.data?.error || 'ログインに失敗しました'
  } finally {
    busy.value = false
  }
}
</script>
