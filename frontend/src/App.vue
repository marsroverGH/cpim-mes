<template>
  <v-app>
    <!-- Logged-in layout -->
    <template v-if="auth.isAuthenticated && !isLoginRoute">
      <v-navigation-drawer permanent width="240">
        <v-list-item
          prepend-icon="mdi-factory"
          title="CPIM-MES"
          subtitle="Production Management"
        />
        <v-divider />
        <v-list density="compact" nav>
          <v-list-item
            v-for="link in links"
            :key="link.to"
            :to="link.to"
            :prepend-icon="link.icon"
            :title="link.title"
          />
        </v-list>
      </v-navigation-drawer>

      <v-app-bar color="primary" density="compact">
        <v-app-bar-title>{{ pageTitle }}</v-app-bar-title>
        <v-spacer />
        <v-chip class="mr-2" size="small" prepend-icon="mdi-account">
          {{ auth.username }} ({{ auth.role }})
        </v-chip>
        <v-btn icon="mdi-refresh" @click="reload" />
        <v-btn icon="mdi-logout" @click="logout" />
      </v-app-bar>

      <v-main>
        <v-container fluid>
          <router-view />
        </v-container>
      </v-main>
    </template>

    <!-- Login layout (no chrome) -->
    <v-main v-else>
      <router-view />
    </v-main>
  </v-app>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const isLoginRoute = computed(() => route.path === '/login')

const links = [
  { to: '/',                title: 'ダッシュボード',  icon: 'mdi-view-dashboard' },
  { to: '/items',           title: '品目マスタ',      icon: 'mdi-package-variant-closed' },
  { to: '/bom',             title: 'BOM',             icon: 'mdi-file-tree' },
  { to: '/work-centers',    title: '作業区',          icon: 'mdi-factory' },
  { to: '/maintenance',     title: 'Maintenance / Downtime', icon: 'mdi-tools' },
  { to: '/production-performance', title: 'OEE / Production Performance', icon: 'mdi-chart-box-outline' },
  { to: '/calendars',       title: '作業カレンダー',  icon: 'mdi-calendar-month' },
  { to: '/routings',        title: 'ルーティング',    icon: 'mdi-routes' },
  { to: '/demand',          title: '旧需要データ',    icon: 'mdi-database-clock' },
  { to: '/sales-orders',     title: 'Sales Orders',    icon: 'mdi-file-document-outline' },
  { to: '/backorders',        title: 'Backorder / Allocation', icon: 'mdi-swap-horizontal-bold' },
  { to: '/pegging-exceptions', title: 'Pegging / Exceptions', icon: 'mdi-family-tree' },
  { to: '/production-control-tower', title: 'Production Control Tower', icon: 'mdi-view-dashboard-variant-outline' },
  { to: '/forecast',        title: '需要予測',        icon: 'mdi-chart-line' },
  { to: '/sop',             title: 'S&OP 月次計画',   icon: 'mdi-chart-timeline' },
  { to: '/rccp',            title: 'RCCP',            icon: 'mdi-chart-gantt' },
  { to: '/atp',             title: 'ATP',             icon: 'mdi-handshake' },
  { to: '/mps',             title: 'MPS',             icon: 'mdi-calendar-clock' },
  { to: '/mrp',             title: 'MRP実行',         icon: 'mdi-cogs' },
  { to: '/mrp-actions',     title: 'MRP アクション',  icon: 'mdi-bell-alert' },
  { to: '/crp',             title: 'CRP (能力計画)',  icon: 'mdi-chart-timeline-variant' },
  { to: '/detailed-scheduling', title: 'Detailed Scheduling', icon: 'mdi-calendar-multiselect' },
  { to: '/dispatch-rescheduling', title: 'Dispatch / Rescheduling', icon: 'mdi-update' },
  { to: '/cost-rollup',     title: '原価積み上げ',    icon: 'mdi-currency-jpy' },
  { to: '/abc-analysis',    title: 'ABC分析',         icon: 'mdi-chart-bar-stacked' },
  { to: '/cycle-count',     title: 'サイクルカウント',icon: 'mdi-counter' },
  { to: '/inventory',       title: '在庫',            icon: 'mdi-warehouse' },
  { to: '/inventory-policy', title: 'Inventory Policy', icon: 'mdi-shield-chart' },
  { to: '/lots',            title: 'ロット追跡',      icon: 'mdi-barcode' },
  { to: '/work-orders',     title: '製造指示',        icon: 'mdi-clipboard-list-outline' },
  { to: '/shop-floor',      title: 'Shop Floor',     icon: 'mdi-account-hard-hat' },
  { to: '/gantt',           title: 'ガントチャート',  icon: 'mdi-chart-gantt' },
  { to: '/purchase-orders', title: '購買発注',        icon: 'mdi-cart' },
  { to: '/supplier-scheduling', title: 'Supplier Scheduling', icon: 'mdi-truck-fast' },
  { to: '/supplier-quality', title: 'Supplier Quality / NCR', icon: 'mdi-shield-check' },
  { to: '/audit-log',       title: '監査ログ',        icon: 'mdi-history' },
  { to: '/eco',             title: 'ECO (BOM変更)',   icon: 'mdi-file-document-edit' },
  { to: '/agent',           title: 'AI アシスタント', icon: 'mdi-robot-outline' }
]

const pageTitle = computed(() => {
  const m = links.find(l => l.to === route.path)
  return m?.title ?? 'CPIM-MES'
})

function reload() { window.location.reload() }
function logout() {
  auth.logout()
  router.push('/login')
}
</script>
