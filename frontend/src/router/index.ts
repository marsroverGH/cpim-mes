import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login',           component: () => import('@/views/Login.vue'), meta: { public: true } },
    { path: '/',                component: () => import('@/views/Dashboard.vue') },
    { path: '/items',           component: () => import('@/views/Items.vue') },
    { path: '/bom',             component: () => import('@/views/Bom.vue') },
    { path: '/work-centers',    component: () => import('@/views/WorkCenters.vue') },
    { path: '/maintenance',     component: () => import('@/views/Maintenance.vue') },
    { path: '/production-performance', component: () => import('@/views/ProductionPerformance.vue') },
    { path: '/calendars',       component: () => import('@/views/Calendars.vue') },
    { path: '/routings',        component: () => import('@/views/Routings.vue') },
    { path: '/demand',          component: () => import('@/views/Demand.vue') },
    { path: '/sales-orders',     component: () => import('@/views/SalesOrders.vue') },
    { path: '/backorders',        component: () => import('@/views/Backorders.vue') },
    { path: '/pegging-exceptions', component: () => import('@/views/PeggingExceptions.vue') },
    { path: '/production-control-tower', component: () => import('@/views/ProductionControlTower.vue') },
    { path: '/recovery-planning', component: () => import('@/views/RecoveryPlanning.vue') },
    { path: '/forecast',        component: () => import('@/views/Forecast.vue') },
    { path: '/atp',             component: () => import('@/views/Atp.vue') },
    { path: '/sop',             component: () => import('@/views/Sop.vue') },
    { path: '/rccp',            component: () => import('@/views/Rccp.vue') },
    { path: '/eco',             component: () => import('@/views/Eco.vue') },
    { path: '/agent',           component: () => import('@/views/Agent.vue') },
    { path: '/mps',             component: () => import('@/views/Mps.vue') },
    { path: '/mrp',             component: () => import('@/views/Mrp.vue') },
    { path: '/mrp-actions',     component: () => import('@/views/ActionMessages.vue') },
    { path: '/crp',             component: () => import('@/views/Crp.vue') },
    { path: '/detailed-scheduling', component: () => import('@/views/DetailedScheduling.vue') },
    { path: '/dispatch-rescheduling', component: () => import('@/views/DispatchRescheduling.vue') },
    { path: '/cost-rollup',     component: () => import('@/views/CostRollup.vue') },
    { path: '/abc-analysis',    component: () => import('@/views/AbcAnalysis.vue') },
    { path: '/cycle-count',     component: () => import('@/views/CycleCount.vue') },
    { path: '/lots',            component: () => import('@/views/Lots.vue') },
    { path: '/gantt',           component: () => import('@/views/Gantt.vue') },
    { path: '/audit-log',       component: () => import('@/views/AuditLog.vue') },
    { path: '/inventory',       component: () => import('@/views/Inventory.vue') },
    { path: '/inventory-policy', component: () => import('@/views/InventoryPolicy.vue') },
    { path: '/work-orders',     component: () => import('@/views/WorkOrders.vue') },
    { path: '/shop-floor',      component: () => import('@/views/ShopFloor.vue') },
    { path: '/purchase-orders', component: () => import('@/views/PurchaseOrders.vue') },
    { path: '/supplier-scheduling', component: () => import('@/views/SupplierScheduling.vue') },
    { path: '/supplier-quality', component: () => import('@/views/SupplierQuality.vue') }
  ]
})

router.beforeEach((to) => {
  const auth = useAuthStore()
  if (!to.meta.public && !auth.isAuthenticated) {
    return { path: '/login' }
  }
  if (to.path === '/login' && auth.isAuthenticated) {
    return { path: '/' }
  }
  return true
})


// Safari/Chromeで、URLとAppBarタイトルだけが先に変わり、
// Vuetify配下のメイン領域が旧画面のまま残るケースを抑止する。
router.afterEach(() => {
  requestAnimationFrame(() => {
    window.dispatchEvent(new Event("resize"))
  })
})

export default router
