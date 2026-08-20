import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { http } from '@/api'

export type Role = 'admin' | 'planner' | 'operator' | 'viewer'

const TOKEN_KEY = 'cpim.token'
const USER_KEY  = 'cpim.user'

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string>(localStorage.getItem(TOKEN_KEY) || '')
  const userJson = localStorage.getItem(USER_KEY)
  const username = ref<string>(userJson ? (JSON.parse(userJson).username || '') : '')
  const role = ref<Role | ''>(userJson ? ((JSON.parse(userJson).role || '') as Role) : '')

  const isAuthenticated = computed(() => !!token.value)
  const canEdit = computed(() => role.value === 'admin' || role.value === 'planner')
  const isAdmin = computed(() => role.value === 'admin')

  async function login(u: string, p: string) {
    const { data } = await http.post<{ token: string; username: string; role: Role }>(
      '/auth/login', { username: u, password: p }
    )
    token.value = data.token
    username.value = data.username
    role.value = data.role
    localStorage.setItem(TOKEN_KEY, data.token)
    localStorage.setItem(USER_KEY, JSON.stringify({ username: data.username, role: data.role }))
  }

  function logout() {
    token.value = ''
    username.value = ''
    role.value = ''
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(USER_KEY)
  }

  return { token, username, role, isAuthenticated, canEdit, isAdmin, login, logout }
})
