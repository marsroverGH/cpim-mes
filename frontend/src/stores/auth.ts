import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { http } from '@/api'

export type Role = 'admin' | 'planner' | 'operator' | 'viewer'

interface SessionUser {
  username: string
  role: Role
  userId?: string
  permissions?: string[]
}

export const useAuthStore = defineStore('auth', () => {
  const username = ref('')
  const role = ref<Role | ''>('')
  const initialized = ref(false)
  const authenticated = ref(false)
  let initialization: Promise<void> | null = null

  const isAuthenticated = computed(() => authenticated.value)
  const canEdit = computed(() => role.value === 'admin' || role.value === 'planner')
  const isAdmin = computed(() => role.value === 'admin')

  function applyUser(user: SessionUser) {
    username.value = user.username
    role.value = user.role
    authenticated.value = true
  }

  function clearSessionState() {
    username.value = ''
    role.value = ''
    authenticated.value = false
  }

  async function initialize() {
    if (initialized.value) return
    if (initialization) return initialization
    initialization = (async () => {
      try {
        const { data } = await http.get<SessionUser>('/auth/me')
        applyUser(data)
      } catch {
        clearSessionState()
      } finally {
        initialized.value = true
        initialization = null
      }
    })()
    return initialization
  }

  async function login(u: string, p: string) {
    const { data } = await http.post<SessionUser>('/auth/login', { username: u, password: p })
    applyUser(data)
    initialized.value = true
  }

  async function logout() {
    try {
      await http.post('/auth/logout', {})
    } finally {
      clearSessionState()
      initialized.value = true
    }
  }

  return {
    username, role, initialized, isAuthenticated, canEdit, isAdmin,
    initialize, login, logout, clearSessionState
  }
})
