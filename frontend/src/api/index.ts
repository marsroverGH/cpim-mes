import axios from 'axios'

const base = (import.meta.env.VITE_API_BASE as string) || '/api'

export const http = axios.create({
  baseURL: base,
  timeout: 30000
})

// Inject JWT into every request
http.interceptors.request.use((config) => {
  const tok = localStorage.getItem('cpim.token')
  if (tok) {
    config.headers.Authorization = `Bearer ${tok}`
  }
  return config
})

// Auto-logout on 401
http.interceptors.response.use(
  (r) => r,
  (err) => {
    if (err?.response?.status === 401) {
      localStorage.removeItem('cpim.token')
      localStorage.removeItem('cpim.user')
      // SPA routing - reload to /login
      if (location.pathname !== '/login') {
        location.href = '/login'
      }
    }
    return Promise.reject(err)
  }
)

// ------- Item -------
export interface Item {
  id?: string
  code: string
  name: string
  type: 'FG' | 'SA' | 'RM' | 'PP'
  uom: string
  leadTimeDays: number
  safetyStock: number
  lotSize: number
  standardCost: number
  lotSizeMethod: 'LFL' | 'FOQ' | 'POQ' | 'EOQ'
  poqPeriods: number
  orderingCost: number
  holdingCostPct: number
  lowLevelCode?: number
  groupId?: string | null
}
export const ItemsApi = {
  list: () => http.get<Item[]>('/items').then(r => r.data),
  create: (it: Item) => http.post<Item>('/items', it).then(r => r.data),
  update: (id: string, it: Item) => http.put<Item>(`/items/${id}`, it).then(r => r.data),
  remove: (id: string) => http.delete(`/items/${id}`),
  recomputeLLC: () => http.post<{ status: string }>('/items/recompute-llc', {}).then(r => r.data)
}

// ------- BOM -------
export interface BOMComponent {
  id?: string
  parentId: string
  childId: string
  quantity: number
  scrapPct: number
}
export interface ExplodedRow {
  level: number
  childId: string
  childCode: string
  childName: string
  totalQuantity: number
}
export const BomApi = {
  components: (parentId: string) => http.get<BOMComponent[]>(`/items/${parentId}/bom`).then(r => r.data),
  add: (parentId: string, c: Omit<BOMComponent, 'parentId'>) =>
    http.post<BOMComponent>(`/items/${parentId}/bom`, c).then(r => r.data),
  remove: (id: string) => http.delete(`/bom/${id}`),
  explode: (parentId: string, qty: number) =>
    http.get<ExplodedRow[]>(`/items/${parentId}/explode?qty=${qty}`).then(r => r.data)
}

// ------- Demand -------
export interface Demand {
  id?: string
  itemId: string
  dueDate: string
  quantity: number
  source: 'FORECAST' | 'ORDER'
}
export const DemandApi = {
  list: () => http.get<Demand[]>('/demand').then(r => r.data)
}


// ------- Customers / Sales Orders -------
export interface Customer {
  id: string
  customerNo: string
  name: string
  status: 'ACTIVE' | 'BLOCKED'
  serviceClassCode: string
  shipTo: string
  notes: string
  createdBy?: string
  createdAt?: string
  updatedAt?: string
}
export interface SalesOrder {
  id: string
  orderNo: string
  customerId: string
  customerNo: string
  customerName: string
  orderDate: string
  requestedDate: string
  promisedDate?: string
  status: 'DRAFT' | 'CONFIRMED' | 'PARTIALLY_SHIPPED' | 'SHIPPED' | 'CANCELLED'
  priority: 'EXPEDITE' | 'HIGH' | 'NORMAL'
  notes: string
  totalQty: number
  allocatedQty: number
  shippedQty: number
  cancelledQty: number
  openQty: number
  createdBy?: string
  confirmedBy?: string
  confirmedAt?: string
}
export interface SalesOrderLine {
  id: string
  salesOrderId: string
  lineNo: number
  itemId: string
  itemCode: string
  itemName: string
  quantity: number
  allocatedQty: number
  shippedQty: number
  cancelledQty: number
  openQty: number
  unitPrice: number
  requestedDate: string
  promisedDate?: string
  notes: string
}
export interface SalesOrderStatusHistory {
  id: string
  salesOrderId: string
  fromStatus?: string
  toStatus: string
  actorUsername: string
  occurredAt: string
  source: string
}
export interface SalesOrderShipment {
  shipmentId: string
  salesOrderId: string
  salesOrderLineId: string
  quantity: number
  inventoryTxnId: string
  shippedAt: string
  shippedByUsername: string
  carrier: string
  trackingNo: string
}
export interface SalesOrderDetail {
  order: SalesOrder
  lines: SalesOrderLine[]
  history: SalesOrderStatusHistory[]
  shipments: SalesOrderShipment[]
}
export interface SalesOrderCreateInput {
  orderNo: string
  customerId: string
  orderDate?: string
  requestedDate: string
  promisedDate?: string
  notes?: string
  lines: Array<{
    itemId: string
    quantity: number
    unitPrice?: number
    requestedDate?: string
    promisedDate?: string
    notes?: string
  }>
}
export interface OrderPromiseRun {
  id: string
  salesOrderId: string
  strategy: 'ATP_THEN_CTP'
  status: 'RUNNING' | 'SUCCEEDED' | 'FAILED'
  requestedAt: string
  completedAt?: string
  horizonDays: number
  resultHash?: string
  errorText: string
  requestedBy: string
}
export interface OrderPromiseLineResult {
  id: string
  runId: string
  salesOrderLineId: string
  requestedQty: number
  requestedDate: string
  atpQty: number
  ctpQty: number
  earliestFullDate?: string
  promiseMethod: 'ATP' | 'ATP_CTP' | 'CTP' | 'UNAVAILABLE'
  materialReadyDate?: string
  capacityReadyDate?: string
  constraintType: 'NONE' | 'MATERIAL' | 'CAPACITY' | 'MATERIAL_AND_CAPACITY' | 'HORIZON'
  constraintDetail: Record<string, unknown>
}
export interface OrderPromiseConfirmation {
  id: string
  runId: string
  salesOrderLineId: string
  sequenceNo: number
  quantity: number
  confirmedDate: string
  source: 'ON_HAND' | 'ATP' | 'CTP_PRODUCTION' | 'CTP_PURCHASE' | 'CTP_MIXED'
}
export interface OrderPromiseAcceptance {
  id: string
  runId: string
  salesOrderId: string
  resultHash: string
  acceptedBy: string
  acceptedAt: string
}
export interface OrderPromiseResult {
  run: OrderPromiseRun
  lines: OrderPromiseLineResult[]
  confirmations: OrderPromiseConfirmation[]
  acceptance?: OrderPromiseAcceptance
}
export const SalesOrdersApi = {
  customers: () => http.get<Customer[]>('/customers').then(r => r.data),
  serviceClasses: () => http.get<CustomerServiceClass[]>('/customer-service-classes').then(r => r.data),
  setCustomerServiceClass: (customerId: string, serviceClassCode: string) => http.put<Customer>(`/customers/${customerId}/service-class`, { serviceClassCode }).then(r => r.data),
  createCustomer: (body: Partial<Customer>) => http.post<Customer>('/customers', body).then(r => r.data),
  updateCustomer: (id: string, body: Partial<Customer>) => http.put<Customer>(`/customers/${id}`, body).then(r => r.data),
  list: () => http.get<SalesOrder[]>('/sales-orders').then(r => r.data),
  get: (id: string) => http.get<SalesOrderDetail>(`/sales-orders/${id}`).then(r => r.data),
  setPriority: (id: string, priority: 'EXPEDITE' | 'HIGH' | 'NORMAL') => http.put<SalesOrderDetail>(`/sales-orders/${id}/priority`, { priority }).then(r => r.data),
  create: (body: SalesOrderCreateInput) => http.post<SalesOrderDetail>('/sales-orders', body).then(r => r.data),
  confirm: (id: string) => http.post<SalesOrderDetail>(`/sales-orders/${id}/confirm`, {}).then(r => r.data),
  cancel: (id: string) => http.post<SalesOrderDetail>(`/sales-orders/${id}/cancel`, {}).then(r => r.data),
  allocate: (lineId: string, quantity: number) => http.post<SalesOrderDetail>(`/sales-order-lines/${lineId}/allocate`, { allocationId: crypto.randomUUID(), quantity }).then(r => r.data),
  release: (lineId: string, quantity: number) => http.post<SalesOrderDetail>(`/sales-order-lines/${lineId}/release-allocation`, { releaseId: crypto.randomUUID(), quantity }).then(r => r.data),
  ship: (lineId: string, quantity: number, carrier = '', trackingNo = '') => http.post<SalesOrderDetail>(`/sales-order-lines/${lineId}/ship`, { shipmentId: crypto.randomUUID(), quantity, carrier, trackingNo }).then(r => r.data),
  promiseCheck: (orderId: string, horizonDays = 180) => http.post<OrderPromiseResult>(`/sales-orders/${orderId}/promise/check`, { horizonDays }).then(r => r.data),
  promiseAccept: (orderId: string, runId: string) => http.post<OrderPromiseResult>(`/sales-orders/${orderId}/promise/accept`, { runId }).then(r => r.data),
  promiseRuns: (orderId: string) => http.get<OrderPromiseRun[]>(`/sales-orders/${orderId}/promise-runs`).then(r => r.data),
  promiseRun: (runId: string) => http.get<OrderPromiseResult>(`/order-promise-runs/${runId}`).then(r => r.data)
}


// ------- Backorder Processing / Product Allocation -------
export interface CustomerServiceClass {
  code: string
  name: string
  priorityRank: number
  isActive: boolean
}
export interface ProductAllocationBucket {
  id?: string
  planId?: string
  serviceClassCode: string
  allocationPct: number
  priorityRank: number
}
export interface ProductAllocationPlan {
  id: string
  itemId: string
  itemCode: string
  itemName: string
  name: string
  effectiveFrom: string
  effectiveTo: string
  status: 'DRAFT' | 'ACTIVE' | 'INACTIVE'
  createdBy: string
  activatedBy?: string
  activatedAt?: string
  deactivatedBy?: string
  deactivatedAt?: string
}
export interface ProductAllocationPlanDetail {
  plan: ProductAllocationPlan
  buckets: ProductAllocationBucket[]
}
export interface ProductAllocationPlanInput {
  itemId: string
  name: string
  effectiveFrom: string
  effectiveTo: string
  buckets: ProductAllocationBucket[]
}
export interface BackorderRun {
  id: string
  status: 'RUNNING' | 'SUCCEEDED' | 'FAILED'
  horizonDays: number
  filterItemId?: string
  requestedAt: string
  completedAt?: string
  resultHash?: string
  errorText: string
  requestedBy: string
}
export interface BackorderRunLine {
  id: string
  runId: string
  salesOrderId: string
  salesOrderNo: string
  salesOrderLineId: string
  itemId: string
  itemCode: string
  itemName: string
  customerId: string
  customerNo: string
  customerName: string
  serviceClassCode: string
  orderPriority: 'EXPEDITE' | 'HIGH' | 'NORMAL'
  rankNo: number
  openQty: number
  allocatedQty: number
  currentPromisedDate?: string
  proposedPromisedDate?: string
  atpQty: number
  ctpQty: number
  backorderQty: number
  decision: 'UNCHANGED' | 'IMPROVED' | 'DELAYED' | 'NEW_PROMISE' | 'BACKORDER'
  constraintType: 'NONE' | 'PRODUCT_ALLOCATION' | 'MATERIAL' | 'CAPACITY' | 'MATERIAL_AND_CAPACITY' | 'HORIZON'
  allocationPlanId?: string
  allocationBucketPct?: number
  detail: Record<string, unknown>
}
export interface BackorderRunConfirmation {
  id: string
  runId: string
  salesOrderLineId: string
  sequenceNo: number
  quantity: number
  confirmedDate: string
  source: 'ALLOCATED' | 'ATP' | 'CTP_PRODUCTION' | 'CTP_PURCHASE' | 'CTP_MIXED'
}
export interface BackorderPublication {
  id: string
  runId: string
  resultHash: string
  publishedBy: string
  publishedAt: string
}
export interface BackorderResult {
  run: BackorderRun
  lines: BackorderRunLine[]
  confirmations: BackorderRunConfirmation[]
  publication?: BackorderPublication
}
export const BackordersApi = {
  preview: (horizonDays = 90, filterItemId?: string) => http.post<BackorderResult>('/backorders/preview', { horizonDays, ...(filterItemId ? { filterItemId } : {}) }).then(r => r.data),
  publish: (runId: string) => http.post<BackorderResult>('/backorders/publish', { runId }).then(r => r.data),
  runs: () => http.get<BackorderRun[]>('/backorders/runs').then(r => r.data),
  run: (id: string) => http.get<BackorderResult>(`/backorders/runs/${id}`).then(r => r.data),
  plans: () => http.get<ProductAllocationPlanDetail[]>('/product-allocation-plans').then(r => r.data),
  createPlan: (body: ProductAllocationPlanInput) => http.post<ProductAllocationPlanDetail>('/product-allocation-plans', body).then(r => r.data),
  activatePlan: (id: string) => http.post<ProductAllocationPlanDetail>(`/product-allocation-plans/${id}/activate`, {}).then(r => r.data),
  deactivatePlan: (id: string) => http.post<ProductAllocationPlanDetail>(`/product-allocation-plans/${id}/deactivate`, {}).then(r => r.data)
}

// ------- Full Pegging / Exception Management -------
export interface PeggingRun {
  id: string
  salesOrderId: string
  status: 'RUNNING' | 'SUCCEEDED' | 'FAILED'
  asOf: string
  horizonDays: number
  resultHash?: string
  errorText: string
  generatedBy: string
  completedAt?: string
  createdAt: string
}
export interface PeggingNode {
  id: string
  runId: string
  nodeKey: string
  nodeType: 'SALES_ORDER' | 'SALES_ORDER_LINE' | 'PROMISE' | 'BACKORDER' | 'INVENTORY' | 'ITEM' | 'WORK_ORDER' | 'PLANNED_ORDER' | 'PURCHASE_ORDER' | 'SUPPLIER' | 'QUALITY_HOLD' | 'DETAILED_SCHEDULE' | 'WORK_CENTER' | 'SHORTAGE'
  entityId?: string
  entityRef: string
  itemId?: string
  itemCode: string
  label: string
  quantity?: number
  dueDate?: string
  status: string
  detail: Record<string, unknown>
}
export interface PeggingEdge {
  id: string
  runId: string
  fromNodeId: string
  toNodeId: string
  edgeType: string
  quantity?: number
  detail: Record<string, unknown>
}
export interface PlanningException {
  id: string
  runId: string
  salesOrderId: string
  salesOrderLineId?: string
  exceptionKey: string
  exceptionType: string
  severity: 'INFO' | 'WARNING' | 'CRITICAL'
  rootNodeId: string
  message: string
  requestedDate?: string
  promisedDate?: string
  impactDate?: string
  impactDays: number
  rootCausePath: string[]
  detail: Record<string, unknown>
  detectedAt: string
  currentStatus?: 'OPEN' | 'ACKNOWLEDGED' | 'RESOLVED'
  salesOrderNo?: string
  customerNo?: string
  customerName?: string
  lineNo?: number
  itemCode?: string
  itemName?: string
  latestActionType?: string
  latestActor?: string
  latestComment?: string
  latestActionAt?: string
}
export interface PlanningExceptionAction {
  id: string
  exceptionId: string
  actionType: 'ACKNOWLEDGE' | 'RESOLVE' | 'REOPEN'
  fromStatus: 'OPEN' | 'ACKNOWLEDGED' | 'RESOLVED'
  toStatus: 'OPEN' | 'ACKNOWLEDGED' | 'RESOLVED'
  actorUsername: string
  comment: string
  occurredAt: string
}
export interface PeggingResult {
  run: PeggingRun
  nodes: PeggingNode[]
  edges: PeggingEdge[]
  exceptions: PlanningException[]
}
export interface ExceptionScanResult {
  peggingRuns: PeggingRun[]
  exceptions: PlanningException[]
}
export const PeggingApi = {
  run: (salesOrderId: string, horizonDays = 180) => http.post<PeggingResult>(`/sales-orders/${salesOrderId}/pegging/run`, { horizonDays }).then(r => r.data),
  runs: (salesOrderId: string) => http.get<PeggingRun[]>(`/sales-orders/${salesOrderId}/pegging-runs`).then(r => r.data),
  runDetail: (runId: string) => http.get<PeggingResult>(`/pegging-runs/${runId}`).then(r => r.data),
  scan: (horizonDays = 180) => http.post<ExceptionScanResult>('/planning-exceptions/scan', { horizonDays }).then(r => r.data),
  exceptions: (filters: { status?: string; severity?: string; type?: string } = {}) => {
    const q = new URLSearchParams()
    if (filters.status) q.set('status', filters.status)
    if (filters.severity) q.set('severity', filters.severity)
    if (filters.type) q.set('type', filters.type)
    const suffix = q.toString() ? `?${q.toString()}` : ''
    return http.get<PlanningException[]>(`/planning-exceptions${suffix}`).then(r => r.data)
  },
  act: (id: string, actionType: 'ACKNOWLEDGE' | 'RESOLVE' | 'REOPEN', comment = '') => http.post<PlanningExceptionAction>(`/planning-exceptions/${id}/actions`, { actionType, comment }).then(r => r.data)
}

// ------- MPS -------
export interface MpsEntry {
  id?: string
  itemId: string
  period: string
  planned: number
  released: number
  sourceForecastRunId?: string
  sourceSopPlanId?: string
  sourceSopDisaggregationRunId?: string
  sourceProductMixVersionId?: string
  demandBasis?: 'MANUAL' | 'FORECAST_CONSUMPTION' | 'SOP_DISAGGREGATION'
}
export const MpsApi = {
  list: () => http.get<MpsEntry[]>('/mps').then(r => r.data),
  upsert: (m: MpsEntry) => http.post<MpsEntry>('/mps', m).then(r => r.data)
}

// ------- Inventory -------
export interface OnHandRow {
  itemId: string
  itemCode: string
  itemName: string
  onHand: number
}
export interface InventoryLotReconciliation {
  itemId: string
  itemCode: string
  itemName: string
  ledgerOnHand: number
  lotOnHand: number
  difference: number
}
export interface InventoryTxn {
  id?: string
  itemId: string
  quantity: number
  txnType: 'RECEIPT' | 'ISSUE' | 'ADJUST'
  refDoc: string
  occurredAt?: string
  lotId?: string
  lotNo?: string
}
export const InventoryApi = {
  onHand: () => http.get<OnHandRow[]>('/inventory/on-hand').then(r => r.data),
  reconciliation: () => http.get<InventoryLotReconciliation[]>('/inventory/reconciliation').then(r => r.data),
  txns: (itemId: string) => http.get<InventoryTxn[]>(`/inventory/${itemId}/transactions`).then(r => r.data),
  post: (t: InventoryTxn) => http.post<InventoryTxn>('/inventory/transactions', t).then(r => r.data)
}

// ------- Work Orders -------
export interface WorkOrder {
  id?: string
  orderNo: string
  itemId: string
  quantity: number
  startDate: string
  dueDate: string
  status: 'PLANNED' | 'RELEASED' | 'IN_PROGRESS' | 'COMPLETED' | 'CLOSED'
  completedQty?: number
  reportedProgressQty?: number
  producedLotId?: string
  releasedAt?: string
  completedAt?: string
}
export const WorkOrdersApi = {
  list: () => http.get<WorkOrder[]>('/work-orders').then(r => r.data),
  create: (w: WorkOrder) => http.post<WorkOrder>('/work-orders', w).then(r => r.data),
  setStatus: (id: string, status: WorkOrder['status']) =>
    http.put(`/work-orders/${id}/status`, { status })
}

// ------- Purchase Orders -------
export interface PurchaseOrder {
  id?: string
  poNo: string
  itemId: string
  supplier: string
  quantity: number
  receivedQty?: number
  remainingQty?: number
  orderDate?: string
  dueDate: string
  status: 'OPEN' | 'PARTIALLY_RECEIVED' | 'RECEIVED' | 'CLOSED'
  supplierQualityStatus?: 'APPROVED' | 'CONDITIONAL' | 'BLOCKED'
  receivedLotId?: string
  receivedAt?: string
  scheduleStatus?: 'UNCONFIRMED' | 'CONFIRMED' | 'ASN'
  confirmationEventId?: string
  confirmedQuantity?: number
  confirmedDeliveryDate?: string
  asnEventId?: string
  asnNo?: string
  asnQuantity?: number
  asnExpectedArrivalDate?: string
  expectedDeliveryDate?: string
  scheduleSource?: 'PO_DUE_DATE' | 'RELIABILITY' | 'SUPPLIER_CONFIRMATION' | 'ASN'
  reliabilitySampleCount?: number
  reliabilityOnTimeRate?: number
  reliabilityP90Days?: number
  recommendedLeadTimeDays?: number
}
export interface SupplierScheduleEvent {
  id: string
  purchaseOrderId: string
  revisionNo: number
  eventType: 'CONFIRM' | 'REVISE' | 'ASN' | 'CANCEL'
  quantity?: number
  confirmedDeliveryDate?: string
  asnNo: string
  expectedArrivalDate?: string
  supplierReference: string
  notes: string
  actorUserId: string
  actorUsername: string
  occurredAt: string
}
export interface SupplierLeadTimeRun {
  id: string
  windowStart: string
  windowEnd: string
  minSamples: number
  status: 'RUNNING' | 'COMPLETE' | 'FAILED'
  resultHash?: string
  generatedByUserId: string
  generatedBy: string
  completedAt?: string
  errorText: string
  createdAt: string
}
export interface SupplierLeadTimeResult {
  id: string
  runId: string
  supplierName: string
  itemId?: string
  itemCode?: string
  sampleCount: number
  averageLeadDays: number
  stddevLeadDays: number
  p50LeadDays: number
  p90LeadDays: number
  onTimeRate: number
  averageLatenessDays: number
  recommendedLeadDays: number
  confidence: 'LOW' | 'MEDIUM' | 'HIGH'
  createdAt: string
}
export interface SupplierLeadTimeRunResult {
  run: SupplierLeadTimeRun
  results: SupplierLeadTimeResult[]
}
export interface PurchaseReceipt {
  receiptId: string
  purchaseOrderId: string
  poNo: string
  itemId: string
  quantity: number
  lotId: string
  lotNo: string
  inventoryTxnId: string
  receivedAt: string
  receivedByUserId?: string
  receivedByUsername: string
  source: 'API' | 'LEGACY_MIGRATION'
}
export const PurchaseOrdersApi = {
  list: () => http.get<PurchaseOrder[]>('/purchase-orders').then(r => r.data),
  create: (p: PurchaseOrder) => http.post<PurchaseOrder>('/purchase-orders', p).then(r => r.data),
  receipts: (id: string) => http.get<PurchaseReceipt[]>(`/purchase-orders/${id}/receipts`).then(r => r.data),
  supplierSchedule: (id: string) => http.get<SupplierScheduleEvent[]>(`/purchase-orders/${id}/supplier-schedule`).then(r => r.data),
  addSupplierScheduleEvent: (id: string, event: {
    eventId?: string; eventType: SupplierScheduleEvent['eventType']; quantity?: number; confirmedDeliveryDate?: string;
    asnNo?: string; expectedArrivalDate?: string; supplierReference?: string; notes?: string
  }) => http.post<SupplierScheduleEvent>(`/purchase-orders/${id}/supplier-schedule/events`, { ...event, eventId: event.eventId || crypto.randomUUID() }).then(r => r.data)
}

export const SupplierSchedulingApi = {
  reliability: () => http.get<SupplierLeadTimeResult[]>('/supplier-scheduling/reliability').then(r => r.data),
  refreshReliability: (windowDays = 365, minSamples = 3) =>
    http.post<SupplierLeadTimeRunResult>('/supplier-scheduling/reliability/refresh', { windowDays, minSamples }).then(r => r.data),
  runs: () => http.get<SupplierLeadTimeRun[]>('/supplier-scheduling/reliability-runs').then(r => r.data),
  run: (id: string) => http.get<SupplierLeadTimeRunResult>(`/supplier-scheduling/reliability-runs/${id}`).then(r => r.data)
}

// ------- Statistical Safety Stock / Inventory Policy -------
export interface InventoryPolicyVersion {
  id: string; itemId: string; itemCode?: string; versionNo: number; status: 'DRAFT'|'ACTIVE'|'ARCHIVED'
  policyMethod: 'STATISTICAL'|'FIXED'; replenishmentMethod: 'SAFETY_STOCK'|'MIN_MAX'
  serviceLevel: number; demandWindowDays: number; minHistoryDays: number; orderCycleDays: number
  fixedSafetyStock?: number; effectiveFrom: string; notes: string; createdBy: string; createdAt: string
  activatedBy?: string; activatedAt?: string; archivedBy?: string; archivedAt?: string
}
export interface EffectiveInventoryPolicy {
  policyVersionId?: string; itemId: string; itemCode?: string; versionNo: number
  policyMethod: string; replenishmentMethod: 'SAFETY_STOCK'|'MIN_MAX'; serviceLevel: number
  demandWindowDays: number; minHistoryDays: number; orderCycleDays: number
  safetyStock: number; reorderPoint: number; minQty: number; maxQty: number
  averageDailyDemand: number; stddevDailyDemand: number; leadTimeMeanDays: number; leadTimeStddevDays: number
  confidence: 'LOW'|'MEDIUM'|'HIGH'|string; calculationStatus: 'CALCULATED'|'FALLBACK'|'ITEM_MASTER'|string
  demandSource: string; leadTimeSource: string; calculatedAsOf?: string
}
export interface InventoryPolicyRun {
  id: string; asOfDate: string; status: 'RUNNING'|'COMPLETE'|'FAILED'; resultHash?: string
  generatedBy: string; completedAt?: string; errorText: string; createdAt: string
}
export interface InventoryPolicyResult {
  id: string; runId: string; policyVersionId: string; itemId: string; itemCode?: string
  demandObservationDays: number; nonzeroDemandDays: number; averageDailyDemand: number; stddevDailyDemand: number
  leadTimeMeanDays: number; leadTimeStddevDays: number; serviceLevel: number; zValue: number
  safetyStock: number; reorderPoint: number; minQty: number; maxQty: number
  demandSource: string; leadTimeSource: string; confidence: string; createdAt: string
}
export interface InventoryPolicyRunResult { run: InventoryPolicyRun; results: InventoryPolicyResult[] }
export const InventoryPolicyApi = {
  current: () => http.get<EffectiveInventoryPolicy[]>('/inventory-policies').then(r => r.data),
  versions: (itemId?: string) => http.get<InventoryPolicyVersion[]>('/inventory-policy-versions', { params: itemId ? { itemId } : {} }).then(r => r.data),
  createVersion: (body: { itemId: string; policyMethod?: string; replenishmentMethod?: string; serviceLevel?: number; demandWindowDays?: number; minHistoryDays?: number; orderCycleDays?: number; fixedSafetyStock?: number; effectiveFrom?: string; notes?: string }) =>
    http.post<InventoryPolicyVersion>('/inventory-policy-versions', body).then(r => r.data),
  activate: (id: string) => http.post<InventoryPolicyVersion>(`/inventory-policy-versions/${id}/activate`, {}).then(r => r.data),
  archive: (id: string) => http.post<InventoryPolicyVersion>(`/inventory-policy-versions/${id}/archive`, {}).then(r => r.data),
  refresh: (asOfDate?: string) => http.post<InventoryPolicyRunResult>('/inventory-policies/refresh', asOfDate ? { asOfDate } : {}).then(r => r.data),
  runs: () => http.get<InventoryPolicyRun[]>('/inventory-policy-runs').then(r => r.data),
  run: (id: string) => http.get<InventoryPolicyRunResult>(`/inventory-policy-runs/${id}`).then(r => r.data)
}

// ------- MRP -------
export interface MrpResult {
  itemId: string
  itemCode: string
  period: string
  grossRequirement: number
  scheduledReceipts: number
  projectedOnHand: number
  netRequirement: number
  plannedOrderReceipt: number
  plannedOrderRelease: number
  plannedOrderReleaseDate?: string
  planningLeadTimeDays?: number
  leadTimeSource?: 'ITEM_MASTER' | 'SUPPLIER_RELIABILITY'
  safetyStockTarget: number
  reorderPoint: number
  minQty: number
  maxQty: number
  inventoryPolicyId?: string
  inventoryPolicyMode: 'SAFETY_STOCK'|'MIN_MAX'|string
  inventoryPolicyStatus: string
  serviceLevel?: number
  lotMethod: string
  eoq?: number
  pegging?: string[]
}
export const MrpApi = {
  run: (req: { horizonDays?: number; startDate?: string }) =>
    http.post<MrpResult[]>('/mrp/run', req).then(r => r.data)
}

// ------- Maintenance / Capacity Downtime -------
export type MaintenanceEventType = 'PREVENTIVE_MAINTENANCE'|'BREAKDOWN'|'PLANNED_DOWNTIME'|'UNPLANNED_DOWNTIME'
export type MaintenanceStatus = 'PLANNED'|'ACTIVE'|'COMPLETED'|'CANCELLED'
export interface CurrentMaintenanceEvent {
  id: string
  workCenterId: string
  workCenterCode: string
  workCenterName: string
  eventType: MaintenanceEventType
  revisionId: string
  revisionNo: number
  status: MaintenanceStatus
  startAt: string
  endAt: string
  unavailableMachines: number
  unavailableWorkers: number
  reason: string
  sourceRef: string
  createdBy: string
  createdAt: string
  actorUsername: string
  occurredAt: string
}
export interface MaintenanceRevision {
  id: string; maintenanceEventId: string; revisionNo: number; status: MaintenanceStatus
  startAt: string; endAt: string; unavailableMachines: number; unavailableWorkers: number
  reason: string; sourceRef: string; actorUsername: string; occurredAt: string
}
export interface MaintenanceEventDetail {
  event: { id:string; workCenterId:string; eventType:MaintenanceEventType; createdBy:string; createdAt:string }
  current?: CurrentMaintenanceEvent
  revisions: MaintenanceRevision[]
}
export interface DetailedScheduleMaintenanceSnapshot {
  runId: string; maintenanceEventId: string; revisionId: string; revisionNo: number; workCenterId: string
  eventType: MaintenanceEventType; status: MaintenanceStatus; startAt: string; endAt: string
  unavailableMachines: number; unavailableWorkers: number; reason: string; sourceRef: string
}
export const MaintenanceApi = {
  list: (workCenterId?: string, includeTerminal=false) => http.get<CurrentMaintenanceEvent[]>('/maintenance-events', { params: { workCenterId, includeTerminal } }).then(r => r.data),
  get: (id:string) => http.get<MaintenanceEventDetail>(`/maintenance-events/${id}`).then(r => r.data),
  create: (x:{workCenterId:string; eventType:MaintenanceEventType; status?:MaintenanceStatus; startAt:string; endAt:string; unavailableMachines:number; unavailableWorkers:number; reason?:string; sourceRef?:string}) => http.post<MaintenanceEventDetail>('/maintenance-events', x).then(r => r.data),
  revise: (id:string, x:{status?:MaintenanceStatus; startAt?:string; endAt?:string; unavailableMachines?:number; unavailableWorkers?:number; reason?:string; sourceRef?:string}) => http.post<MaintenanceEventDetail>(`/maintenance-events/${id}/revisions`, x).then(r => r.data)
}

// ------- OEE / Production Performance / Actual Capacity Feedback -------
export interface ProductionPerformanceRun {
  id:string; windowStart:string; windowEnd:string; minCompletedOps:number; status:'RUNNING'|'COMPLETE'|'FAILED'; resultHash?:string; generatedBy:string; generatedAt?:string; createdAt:string
}
export interface ProductionPerformanceResult {
  id:string; runId:string; workCenterId:string; workCenterCode:string; sampleCount:number
  plannedProductionMinutes:number; runTimeMinutes:number; downtimeMinutes:number
  activeSessionMinutes:number; plannedSetupMinutes:number; idealRunMinutes:number; pauseMinutes:number
  plannedDowntimeMinutes:number; unplannedDowntimeMinutes:number; setupLossMinutes:number; speedLossMinutes:number
  goodQuantity:number; rejectQuantity:number; availability:number; performance:number; quality:number; oee:number
  breakdownCount:number; mtbfMinutes:number; mttrMinutes:number; recommendedEfficiency:number; recommendedUtilization:number; confidence:'LOW'|'MEDIUM'|'HIGH'; createdAt:string
}
export interface CapacityFeedbackVersion {
  id:string; workCenterId:string; workCenterCode?:string; workCenterName?:string; versionNo:number; sourceRunId:string; sourceResultId:string
  status:'DRAFT'|'ACTIVE'|'ARCHIVED'; effectiveEfficiency:number; effectiveUtilization:number
  sourceOee:number; sourceAvailability:number; sourcePerformance:number; sourceQuality:number; sampleCount:number; confidence:'LOW'|'MEDIUM'|'HIGH'
  effectiveFrom:string; notes:string; createdBy:string; createdAt:string; activatedBy?:string; activatedAt?:string; archivedBy?:string; archivedAt?:string
}
export interface ProductionPerformanceRunResult { run:ProductionPerformanceRun; results:ProductionPerformanceResult[]; feedback:CapacityFeedbackVersion[] }
export interface DetailedScheduleCapacityFeedbackSnapshot {
  runId:string; feedbackVersionId:string; workCenterId:string; versionNo:number; sourceRunId:string; sourceResultId:string
  effectiveEfficiency:number; effectiveUtilization:number; sourceOee:number; sourceAvailability:number; sourcePerformance:number; sourceQuality:number
  sampleCount:number; confidence:'LOW'|'MEDIUM'|'HIGH'; effectiveFrom:string
}
export const ProductionPerformanceApi = {
  run: (x:{windowStart:string;windowEnd:string;minCompletedOps?:number}) => http.post<ProductionPerformanceRunResult>('/production-performance/runs',x).then(r=>r.data),
  runs: () => http.get<ProductionPerformanceRun[]>('/production-performance/runs').then(r=>r.data),
  getRun: (id:string) => http.get<ProductionPerformanceRunResult>(`/production-performance/runs/${id}`).then(r=>r.data),
  feedback: () => http.get<CapacityFeedbackVersion[]>('/capacity-feedback').then(r=>r.data),
  activate: (id:string,x:{effectiveFrom?:string;notes?:string}={}) => http.post<CapacityFeedbackVersion>(`/capacity-feedback/${id}/activate`,x).then(r=>r.data),
  archive: (id:string,notes='') => http.post<CapacityFeedbackVersion>(`/capacity-feedback/${id}/archive`,{notes}).then(r=>r.data)
}

// ------- Work Centers -------
export interface WorkCenter {
  id?: string
  code: string
  name: string
  capacityMinutesPerDay: number
  efficiency: number
  utilization: number
  laborRatePerMinute: number
  overheadRatePerMinute: number
  calendarId?: string | null
  shiftStartMinute?: number
  machineCount?: number
  workerCount?: number
}
export const WorkCentersApi = {
  list: () => http.get<WorkCenter[]>('/work-centers').then(r => r.data),
  create: (w: WorkCenter) => http.post<WorkCenter>('/work-centers', w).then(r => r.data),
  update: (id: string, w: WorkCenter) => http.put<WorkCenter>(`/work-centers/${id}`, w).then(r => r.data),
  remove: (id: string) => http.delete(`/work-centers/${id}`),
  setupMatrix: (id: string) => http.get<WorkCenterSetupMatrixRow[]>(`/work-centers/${id}/setup-matrix`).then(r => r.data),
  upsertSetupMatrix: (id: string, x: Omit<WorkCenterSetupMatrixRow,'id'|'workCenterId'>) => http.post<WorkCenterSetupMatrixRow>(`/work-centers/${id}/setup-matrix`, x).then(r => r.data),
  removeSetupMatrix: (id: string) => http.delete(`/work-center-setup-matrix/${id}`)
}

// ------- Routings -------
export interface Routing {
  id?: string
  itemId: string
  description: string
  isActive: boolean
}
export interface RoutingOperation {
  id?: string
  routingId: string
  seqNo: number
  workCenterId: string
  description: string
  setupMinutes: number
  runMinutesPerUnit: number
  setupFamily?: string
  overlapEnabled?: boolean
  transferBatchQty?: number
  machinesRequired?: number
  workersRequired?: number
}
export interface RoutingOperationAlternative {
  id?: string
  routingOperationId: string
  workCenterId: string
  priority: number
  runTimeMultiplier: number
  setupTimeMultiplier: number
  isActive: boolean
}
export interface WorkCenterSetupMatrixRow {
  id?: string
  workCenterId: string
  fromSetupFamily: string
  toSetupFamily: string
  setupMinutes: number
}
export const RoutingsApi = {
  list: () => http.get<Routing[]>('/routings').then(r => r.data),
  create: (rt: Routing) => http.post<Routing>('/routings', rt).then(r => r.data),
  operations: (id: string) =>
    http.get<RoutingOperation[]>(`/routings/${id}/operations`).then(r => r.data),
  addOperation: (id: string, op: Omit<RoutingOperation, 'routingId'>) =>
    http.post<RoutingOperation>(`/routings/${id}/operations`, op).then(r => r.data),
  updateOperation: (opId: string, op: Omit<RoutingOperation, 'id' | 'routingId'>) =>
    http.put<RoutingOperation>(`/routing-operations/${opId}`, op).then(r => r.data),
  removeOperation: (opId: string) => http.delete(`/routing-operations/${opId}`),
  alternatives: (opId: string) => http.get<RoutingOperationAlternative[]>(`/routing-operations/${opId}/alternatives`).then(r => r.data),
  addAlternative: (opId: string, x: Omit<RoutingOperationAlternative, 'id' | 'routingOperationId'>) => http.post<RoutingOperationAlternative>(`/routing-operations/${opId}/alternatives`, x).then(r => r.data),
  removeAlternative: (id: string) => http.delete(`/routing-operation-alternatives/${id}`)
}

// ------- CRP -------
export interface CapacityLoadRow {
  workCenterId: string
  workCenterCode: string
  workCenterName: string
  date: string
  requiredMinutes: number
  availableMinutes: number
  loadPct: number
  isHoliday?: boolean
}
export interface CrpScheduleRun {
  id: string
  startDate: string
  endDate: string
  horizonDays: number
  mode: 'FINITE_FORWARD'
  status: 'BUILDING' | 'COMPLETE'
  generatedAt: string
  generatedBy: string
}
export interface CrpScheduleOrder {
  id: string
  runId: string
  sourceType: 'FIRM_WO' | 'MRP_PLANNED'
  sourceRef: string
  workOrderId?: string
  itemId: string
  itemCode: string
  quantity: number
  priority: number
  earliestStart: string
  dueAt: string
  scheduledStart?: string
  scheduledEnd?: string
  requiredMinutes: number
  scheduledMinutes: number
  unscheduledMinutes: number
  tardyMinutes: number
  scheduleStatus: 'ON_TIME' | 'LATE' | 'PARTIAL' | 'UNSCHEDULED'
}
export interface CrpScheduleSegment {
  id: string
  runId: string
  scheduleOrderId: string
  sourceType: 'FIRM_WO' | 'MRP_PLANNED'
  sourceRef: string
  itemId: string
  itemCode: string
  operationSeq: number
  operationDescription: string
  workCenterId: string
  workCenterCode: string
  workCenterName: string
  segmentNo: number
  startAt: string
  endAt: string
  loadMinutes: number
  clockMinutes: number
  effectiveLoadRate: number
  firm: boolean
}
export interface CrpFiniteScheduleResult {
  run: CrpScheduleRun
  summary: { firmOrders: number; plannedOrders: number; scheduledOrders: number; lateOrders: number; unscheduledOrders: number; scheduledSegments: number; totalLoadMinutes: number }
  orders: CrpScheduleOrder[]
  segments: CrpScheduleSegment[]
  loads: CapacityLoadRow[]
  maintenance: DetailedScheduleMaintenanceSnapshot[]
}
export const CrpApi = {
  run: (req: { horizonDays?: number; startDate?: string }) =>
    http.post<CapacityLoadRow[]>('/crp/run', req).then(r => r.data),
  schedule: (req: { horizonDays?: number; startDate?: string }) =>
    http.post<CrpFiniteScheduleResult>('/crp/schedule', req).then(r => r.data),
  scheduleRuns: () => http.get<CrpScheduleRun[]>('/crp/schedule-runs').then(r => r.data),
  scheduleRun: (id: string) => http.get<CrpFiniteScheduleResult>(`/crp/schedule-runs/${id}`).then(r => r.data)
}

// ------- Detailed Scheduling -------
export interface DetailedScheduleRun {
  id: string; startDate: string; endDate: string; horizonDays: number; mode: 'DETAILED_HEURISTIC'; status: 'BUILDING'|'COMPLETE'; generatedAt: string; generatedBy: string
}
export interface DetailedScheduleOrder {
  id: string; runId: string; sourceType: 'FIRM_WO'|'MRP_PLANNED'; sourceRef: string; workOrderId?: string; itemId: string; itemCode: string; quantity: number; priority: number; earliestStart: string; dueAt: string; scheduledStart?: string; scheduledEnd?: string; scheduleStatus: 'ON_TIME'|'LATE'|'PARTIAL'|'UNSCHEDULED'; tardyMinutes: number
}
export interface DetailedScheduleBatch {
  id: string; runId: string; scheduleOrderId: string; operationSeq: number; operationDescription: string; batchNo: number; batchQty: number; cumulativeQty: number; setupFamily: string; workCenterId?: string; workCenterCode: string; workCenterName: string; primaryWorkCenter: boolean; alternativePriority: number; machineCapacitySnapshot: number; workerCapacitySnapshot: number; machinesRequired: number; workersRequired: number; sequenceSetupMinutes: number; runClockMinutes: number; scheduledStart?: string; scheduledEnd?: string; scheduleStatus: 'SCHEDULED'|'UNSCHEDULED'; machineLanes?: number[]
}
export interface DetailedScheduleSegment {
  id: string; runId: string; batchId: string; scheduleOrderId: string; operationSeq: number; batchNo: number; segmentNo: number; segmentType: 'SETUP'|'RUN'; workCenterId: string; workCenterCode?: string; startAt: string; endAt: string; machinesRequired: number; workersRequired: number; machineCapacitySnapshot: number; workerCapacitySnapshot: number; setupFamily: string; fromSetupFamily: string; clockMinutes: number; firm: boolean; machineLanes?: number[]
}
export interface DetailedScheduleResult {
  run: DetailedScheduleRun
  summary: { firmOrders:number; plannedOrders:number; scheduledOrders:number; lateOrders:number; unscheduledOrders:number; alternativeUses:number; transferBatches:number; setupMinutes:number; runMinutes:number; peakWorkers:number }
  orders: DetailedScheduleOrder[]
  batches: DetailedScheduleBatch[]
  dependencies: { batchId:string; predecessorBatchId:string; dependencyType:'ROUTING'|'SAME_OPERATION' }[]
  segments: DetailedScheduleSegment[]
  loads: CapacityLoadRow[]
  maintenance: DetailedScheduleMaintenanceSnapshot[]
  capacityFeedback: DetailedScheduleCapacityFeedbackSnapshot[]
}
export const DetailedSchedulingApi = {
  run: (req: { horizonDays?: number; startDate?: string }) => http.post<DetailedScheduleResult>('/detailed-scheduling/run', req).then(r => r.data),
  runs: () => http.get<DetailedScheduleRun[]>('/detailed-scheduling/runs').then(r => r.data),
  getRun: (id: string) => http.get<DetailedScheduleResult>(`/detailed-scheduling/runs/${id}`).then(r => r.data)
}

// ------- Cost Rollup -------
export interface CostRollupRow {
  itemId: string
  itemCode: string
  itemName: string
  itemType: 'FG' | 'SA' | 'RM' | 'PP'
  materialCost: number
  laborCost: number
  overheadCost: number
  totalCost: number
}
export const CostRollupApi = {
  run: () => http.get<CostRollupRow[]>('/cost-rollup').then(r => r.data)
}

// ------- ABC Analysis -------
export interface ABCRow {
  itemId: string
  itemCode: string
  itemName: string
  onHand: number
  standardCost: number
  onHandValue: number
  annualUsageQty: number
  annualUsageValue: number
  usageValuePct: number
  cumulativePct: number
  abcClass: 'A' | 'B' | 'C'
  usagePeriodStart: string
  usagePeriodEnd: string
  usageBasis: 'ISSUE'
  costBasis: 'STANDARD_COST'
}
export const ABCApi = {
  run: (asOf?: string) => http.get<ABCRow[]>('/abc-analysis', { params: asOf ? { asOf } : undefined }).then(r => r.data)
}

// ------- CSV import / export -------
export interface CsvImportResult {
  inserted: number
  updated: number
  skipped: number
  errors: string[]
}
export const CsvApi = {
  // Fetch CSV blob with JWT and trigger browser download
  downloadItems: async () => {
    const res = await http.get<Blob>('/items/export.csv', { responseType: 'blob' })
    const url = URL.createObjectURL(res.data)
    const a = document.createElement('a')
    a.href = url
    a.download = 'items.csv'
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  },
  importItems: (file: File) => {
    const fd = new FormData()
    fd.append('file', file)
    return http.post<CsvImportResult>('/items/import', fd, {
      headers: { 'Content-Type': 'multipart/form-data' }
    }).then(r => r.data)
  }
}

// ------- Lots / Traceability -------
export interface Lot {
  id?: string
  itemId: string
  itemCode?: string
  itemName?: string
  lotNo: string
  quantity: number
  receivedAt?: string
  expiryDate?: string | null
  supplier: string
  sourceDoc: string
  notes: string
  balance?: number
  qualityStatus?: 'OK' | 'HOLD' | 'REJECTED'
}
export interface LotMovement {
  id?: string
  lotId: string
  txnId?: string
  quantity: number
  movementType: 'RECEIPT' | 'ISSUE' | 'ADJUST' | 'CONSUMED' | 'PRODUCED' | 'RETURN_TO_SUPPLIER' | 'SCRAP'
  refDoc: string
  occurredAt?: string
}
export const LotsApi = {
  list: () => http.get<Lot[]>('/lots').then(r => r.data),
  byItem: (itemId: string) => http.get<Lot[]>(`/items/${itemId}/lots`).then(r => r.data),
  create: (l: Lot) => http.post<Lot>('/lots', l).then(r => r.data),
  movements: (id: string) => http.get<LotMovement[]>(`/lots/${id}/movements`).then(r => r.data),
  addMovement: (id: string, m: Omit<LotMovement, 'lotId'>) =>
    http.post<LotMovement>(`/lots/${id}/movements`, m).then(r => r.data),
  whereUsed: (id: string) => http.get<LotMovement[]>(`/lots/${id}/where-used`).then(r => r.data)
}

// ------- Audit Log -------
export interface AuditEntry {
  id: number
  username: string
  userRole: string
  action: string
  resource: string
  resourceId: string
  httpStatus: number
  ipAddress: string
  occurredAt: string
  payload?: any
}
export const AuditApi = {
  list: (filter?: { username?: string; resource?: string }) => {
    const params = new URLSearchParams()
    if (filter?.username) params.set('username', filter.username)
    if (filter?.resource) params.set('resource', filter.resource)
    const qs = params.toString()
    return http.get<AuditEntry[]>(`/audit-log${qs ? '?' + qs : ''}`).then(r => r.data)
  }
}

// ------- Forecasting -------
export interface ForecastPoint {
  period: string
  actual?: number
  forecast?: number
  isFuture: boolean
}
export interface ForecastResult {
  itemId: string
  itemCode: string
  method: string
  mae: number
  mape: number
  points: ForecastPoint[]
  runId?: string
  version?: number
  scenario?: string
  status?: 'DRAFT' | 'ACTIVE' | 'ARCHIVED'
}
export interface ForecastRequest {
  itemId: string
  method?: 'SMA' | 'EXPO' | 'HW'
  window?: number
  alpha?: number
  beta?: number
  gamma?: number
  seasonLength?: number
  horizonPeriods?: number
  bucketDays?: number
  scenario?: string
  asOfDate?: string
  saveAsVersion?: boolean
  saveAsForecast?: boolean
}
export interface ForecastRun {
  id: string
  itemId: string
  version: number
  scenario: string
  method: string
  bucketDays: number
  horizonPeriods: number
  asOfDate: string
  mae: number
  mape: number
  status: 'DRAFT' | 'ACTIVE' | 'ARCHIVED'
  generatedAt: string
  generatedBy: string
  generatedByUserId?: string
  activatedAt?: string
  activatedBy?: string
  activatedByUserId?: string
}
export interface ForecastValue {
  id: string
  forecastRunId: string
  period: string
  quantity: number
}
export interface ForecastRunDetail { run: ForecastRun; values: ForecastValue[] }
export interface ForecastConsumptionBucket {
  period: string
  forecastQty: number
  orderQty: number
  consumedForecast: number
  remainingForecast: number
  orderAboveForecast: number
  totalDemand: number
}
export interface ForecastConsumptionResult {
  runId: string
  itemId: string
  itemCode: string
  version: number
  scenario: string
  bucketDays: number
  status: string
  buckets: ForecastConsumptionBucket[]
}
export const ForecastApi = {
  run: (req: ForecastRequest) =>
    http.post<ForecastResult>('/forecast/run', req).then(r => r.data),
  runs: (itemId?: string) =>
    http.get<ForecastRun[]>(`/forecast/runs${itemId ? `?itemId=${encodeURIComponent(itemId)}` : ''}`).then(r => r.data),
  getRun: (id: string) => http.get<ForecastRunDetail>(`/forecast/runs/${id}`).then(r => r.data),
  activate: (id: string) => http.post(`/forecast/runs/${id}/activate`, {}),
  consumption: (id: string) => http.get<ForecastConsumptionResult>(`/forecast/runs/${id}/consumption`).then(r => r.data),
  applyToMps: (id: string) => http.post<{ updatedMpsEntries: number }>(`/forecast/runs/${id}/apply-to-mps`, {}).then(r => r.data)
}

// ------- Cycle Counts -------
export interface CycleCount {
  id: string
  itemId: string
  itemCode: string
  itemName: string
  abcClass: 'A' | 'B' | 'C'
  scheduledDate: string
  countedDate?: string
  expectedQty?: number
  countedQty?: number
  variance?: number
  status: 'PENDING' | 'COUNTED' | 'RECONCILED'
  notes: string
  createdAt: string
}
export const CycleCountApi = {
  list: (status?: string) => {
    const qs = status ? `?status=${encodeURIComponent(status)}` : ''
    return http.get<CycleCount[]>(`/cycle-counts${qs}`).then(r => r.data)
  },
  generate: () => http.post<{ created: number }>('/cycle-counts/generate', {}).then(r => r.data),
  record: (id: string, countedQty: number, notes: string) =>
    http.post(`/cycle-counts/${id}/record`, { countedQty, notes })
}

// ------- End-to-end Workflow -------
export interface ReservationLine {
  childId: string
  childCode: string
  required: number
  available: number
  sufficient: boolean
}
export interface ReleaseResult {
  workOrderId: string
  orderNo: string
  bomSnapshotId: string
  bomSnapshotAt: string
  reservations: ReservationLine[]
}
export interface BOMSnapshotHeader {
  id: string
  workOrderId: string
  parentItemId: string
  capturedAt: string
  source: string
  sourceRef?: string
  notes: string
}
export interface BOMSnapshotLine {
  id: string
  snapshotId: string
  lineNo: number
  sourceBomComponentId?: string
  childId: string
  childCode: string
  childName: string
  childUom: string
  quantityPer: number
  scrapPct: number
  requiredQty: number
  standardCostSnapshot: number
}
export interface BOMSnapshotResult {
  snapshot: BOMSnapshotHeader
  lines: BOMSnapshotLine[]
}
export interface CompletionLine {
  childId: string
  childCode: string
  quantity: number
  lotId: string
  lotNo: string
}
export interface CompletionResult {
  completionId: string
  workOrderId: string
  orderNo: string
  bomSnapshotId: string
  bomSnapshotAt: string
  completedNow: number
  completedQty: number
  plannedQty: number
  remainingQty: number
  status: string
  finalOperationSeqNo: number
  finalOperationCompletedQty: number
  finalOperationAvailableQty: number
  consumedLots: CompletionLine[]
  producedLot: CompletionLine
  idempotentHit: boolean
}
export interface ReceiveResult {
  receiptId: string
  purchaseOrderId: string
  poNo: string
  lotId: string
  lotNo: string
  inventoryTxnId: string
  quantity: number
  orderedQty: number
  receivedQty: number
  remainingQty: number
  status: 'PARTIALLY_RECEIVED' | 'RECEIVED'
  receivedAt: string
  receivedBy: string
  idempotentHit: boolean
}
export interface StockBalance {
  itemId: string
  itemCode: string
  itemName: string
  onHand: number
  reserved: number
}
export const WorkflowApi = {
  releaseWO:   (id: string) =>
    http.post<ReleaseResult>(`/work-orders/${id}/release`, {}).then(r => r.data),
  getBOMSnapshot: (id: string) =>
    http.get<BOMSnapshotResult>(`/work-orders/${id}/bom-snapshot`).then(r => r.data),
  completeWO:  (id: string, quantity: number, lotNo: string, completionId: string) =>
    http.post<CompletionResult>(`/work-orders/${id}/complete`,
      { quantity, lotNo, completionId }).then(r => r.data),
  receivePO:   (id: string, quantity: number, lotNo: string, receiptId: string) =>
    http.post<ReceiveResult>(`/purchase-orders/${id}/receive`, { quantity, lotNo, receiptId }).then(r => r.data),
  balance:     () =>
    http.get<StockBalance[]>('/inventory/balance').then(r => r.data)
}

// ------- Working Calendars -------
export interface WorkCalendar {
  id?: string
  code: string
  name: string
  isDefault: boolean
  mondayMin: number
  tuesdayMin: number
  wednesdayMin: number
  thursdayMin: number
  fridayMin: number
  saturdayMin: number
  sundayMin: number
}
export interface CalendarException {
  id?: string
  calendarId: string
  exceptionDate: string  // YYYY-MM-DD
  kind: 'HOLIDAY' | 'WORKDAY'
  minutes: number
  description: string
}
export const CalendarApi = {
  list:           ()              => http.get<WorkCalendar[]>('/calendars').then(r => r.data),
  get:            (id: string)    => http.get<WorkCalendar>(`/calendars/${id}`).then(r => r.data),
  create:         (c: WorkCalendar) => http.post<WorkCalendar>('/calendars', c).then(r => r.data),
  update:         (id: string, c: WorkCalendar) =>
    http.put<WorkCalendar>(`/calendars/${id}`, c).then(r => r.data),
  remove:         (id: string)    => http.delete(`/calendars/${id}`),
  exceptions:     (id: string)    =>
    http.get<CalendarException[]>(`/calendars/${id}/exceptions`).then(r => r.data),
  addException:   (id: string, e: CalendarException) =>
    http.post<CalendarException>(`/calendars/${id}/exceptions`, e).then(r => r.data),
  removeException:(exId: string)  => http.delete(`/calendar-exceptions/${exId}`)
}

// ------- WIP / Progress -------
export const WIPApi = {
  updateProgress: (id: string, completedQty: number) =>
    http.post<{ id: string; reportedProgressQty: number; completedQty: number; plannedQty: number; percentDone: number }>(
      `/work-orders/${id}/progress`, { completedQty }).then(r => r.data)
}

// ------- ATP -------
export interface ATPBucket {
  period: string
  startingOnHand: number
  scheduledIn: number
  committedOut: number
  endingProjected: number
  atp: number
  cumulativeAtp: number
}
export interface ATPResult {
  itemId: string
  itemCode: string
  safetyStockProtected: number
  inventoryPolicyId?: string
  serviceLevel?: number
  policyStatus: string
  buckets: ATPBucket[]
}
export const ATPApi = {
  run: (itemId: string, horizonDays = 56, bucketDays = 7) =>
    http.get<ATPResult>(
      `/items/${itemId}/atp?horizonDays=${horizonDays}&bucketDays=${bucketDays}`
    ).then(r => r.data)
}

// ------- Quality -------
export interface QualityInspection {
  id?: string
  lotId: string
  inspectorUserId?: string
  inspector: string
  inspectedAt?: string
  result: 'PASS' | 'FAIL' | 'HOLD'
  defectQty: number
  notes: string
  previousStatus?: 'OK' | 'HOLD' | 'REJECTED'
  resultingStatus: 'OK' | 'HOLD' | 'REJECTED'
}
export interface QualityRecordRequest {
  result: 'PASS' | 'FAIL' | 'HOLD'
  defectQty: number
  notes: string
}
export interface QualityStatusHistory {
  id: string
  lotId: string
  inspectionId?: string
  fromStatus?: 'OK' | 'HOLD' | 'REJECTED'
  toStatus: 'OK' | 'HOLD' | 'REJECTED'
  changedByUserId?: string
  changedBy: string
  changedAt: string
  source: string
  sourceRef?: string
  notes: string
}
export const QualityApi = {
  byLot:  (lotId: string) =>
    http.get<QualityInspection[]>(`/lots/${lotId}/inspections`).then(r => r.data),
  history: (lotId: string) =>
    http.get<QualityStatusHistory[]>(`/lots/${lotId}/quality-history`).then(r => r.data),
  record: (lotId: string, q: QualityRecordRequest) =>
    http.post<QualityInspection>(`/lots/${lotId}/inspections`, q).then(r => r.data),
  recent: (limit = 100) =>
    http.get<QualityInspection[]>(`/quality/recent?limit=${limit}`).then(r => r.data)
}

// ------- MRP Action Messages -------
export interface ActionMessage {
  kind: 'EXPEDITE' | 'RELEASE' | 'FUTURE_RELEASE' | 'RESCHEDULE_IN' | 'RESCHEDULE_OUT' | 'CANCEL'
  itemId: string
  itemCode: string
  quantity: number
  needDate: string
  currentDate?: string
  refDocType: 'PO' | 'WO' | 'PLANNED'
  refDocNo: string
  refDocId?: string
  severity: 'INFO' | 'WARNING' | 'CRITICAL'
  message: string
}
export const ActionsApi = {
  list: (horizonDays = 28) =>
    http.get<ActionMessage[]>(`/mrp/action-messages?horizonDays=${horizonDays}`).then(r => r.data)
}

// ------- Shop Floor -------
export interface WOOperationDetail {
  id: string
  woId: string
  seqNo: number
  workCenterId: string
  workCenterCode: string
  workCenterName: string
  description: string
  plannedSetupMin: number
  plannedRunPerUnit: number
  routingOperationId?: string
  setupFamily: string
  overlapEnabled: boolean
  transferBatchQty: number
  machinesRequired: number
  workersRequired: number
  actualMinutes: number
  completedQty: number
  status: 'PENDING' | 'READY' | 'IN_PROGRESS' | 'PAUSED' | 'COMPLETED'
  operator: string
  operatorUserId?: string
  startedAt?: string
  activeStartedAt?: string
  completedAt?: string
  orderNo: string
  itemCode: string
  itemName: string
  woQuantity: number
}
export interface OperationLog {
  id: string
  woOpId: string
  eventType: 'START' | 'STOP' | 'COMPLETE' | 'SCRAP'
  eventAt: string
  operator: string
  operatorUserId?: string
  quantity: number
  notes: string
}
export const ShopFloorApi = {
  active:    () => http.get<WOOperationDetail[]>('/shop-floor/active').then(r => r.data),
  forWO:     (woId: string) =>
    http.get<WOOperationDetail[]>(`/work-orders/${woId}/operations`).then(r => r.data),
  // Operator identity and elapsed minutes are backend-owned.
  start:     (opId: string) =>
    http.post(`/wo-operations/${opId}/start`, {}),
  stop:      (opId: string, notes = '') =>
    http.post(`/wo-operations/${opId}/stop`, { notes }),
  complete:  (opId: string, completedQty: number, notes = '') =>
    http.post(`/wo-operations/${opId}/complete`, { completedQty, notes }),
  scrap:     (opId: string, quantity: number, notes = '') =>
    http.post(`/wo-operations/${opId}/scrap`, { quantity, notes }),
  logs:      (opId: string) =>
    http.get<OperationLog[]>(`/wo-operations/${opId}/logs`).then(r => r.data)
}

// ------- KPI Dashboard -------
export interface KPIPoint { date: string; value: number }
export interface KPIDashboard {
  generatedAt: string
  otifRate: number
  onTimeRate: number
  inventoryTurnover: number
  inventoryValue: number
  throughputUnits: number
  wipUnits: number
  openWoCount: number
  openPoCount: number
  overdueWoCount: number
  overduePoCount: number
  qualityPassRate: number
  qualityHoldCount: number
  qualityRejectCount: number
  criticalActions: number
  warningActions: number
  dailyThroughput: KPIPoint[]
}
export const KPIApi = {
  dashboard: () => http.get<KPIDashboard>('/kpi/dashboard').then(r => r.data)
}

// ------- S&OP -------
export interface ItemGroup {
  id?: string
  code: string
  name: string
  description: string
}
export interface SOPPlan {
  id?: string
  groupId: string
  planMonth: string  // ISO date (YYYY-MM-01)
  demandQty: number
  supplyQty: number
  inventoryTarget: number
  notes: string
}
export interface SOPProductMixLine { id?: string; mixVersionId?: string; itemId: string; mixPct: number }
export interface SOPProductMixVersion {
  id: string; groupId: string; version: number; name: string; status: 'DRAFT' | 'ACTIVE' | 'ARCHIVED'
  createdAt?: string; createdBy?: string; activatedAt?: string; activatedBy?: string
  lines: SOPProductMixLine[]
}
export interface SOPDisaggregationLine {
  id?: string; runId?: string; itemId: string; period: string; mixPct: number; timeWeight: number; plannedQty: number
}
export interface SOPDisaggregationPreview {
  sopPlanId: string; mixVersionId: string; groupId: string; planMonth: string; supplyQty: number; lines: SOPDisaggregationLine[]
}
export interface SOPDisaggregationRun {
  id: string; sopPlanId: string; mixVersionId: string; groupId: string; planMonth: string; supplyQtySnapshot: number
  timePhasing: string; status: 'APPLIED'; appliedAt: string; appliedBy: string
}
export const SOPApi = {
  groups:      () => http.get<ItemGroup[]>('/item-groups').then(r => r.data),
  createGroup: (g: ItemGroup) => http.post<ItemGroup>('/item-groups', g).then(r => r.data),
  plans:       () => http.get<SOPPlan[]>('/sop/plans').then(r => r.data),
  upsert:      (p: SOPPlan) => http.post<SOPPlan>('/sop/plans', p).then(r => r.data),
  remove:      (id: string) => http.delete(`/sop/plans/${id}`),
  mixVersions: (groupId?: string) => http.get<SOPProductMixVersion[]>('/sop/product-mix/versions', { params: groupId ? { groupId } : {} }).then(r => r.data),
  createMixVersion: (body: { groupId: string; name: string; lines: SOPProductMixLine[] }) => http.post<SOPProductMixVersion>('/sop/product-mix/versions', body).then(r => r.data),
  activateMixVersion: (id: string) => http.post(`/sop/product-mix/versions/${id}/activate`, {}),
  previewDisaggregation: (planId: string, mixVersionId: string) => http.get<SOPDisaggregationPreview>(`/sop/plans/${planId}/disaggregation/preview`, { params: { mixVersionId } }).then(r => r.data),
  disaggregate: (planId: string, mixVersionId: string) => http.post<SOPDisaggregationRun>(`/sop/plans/${planId}/disaggregate`, { mixVersionId }).then(r => r.data),
  disaggregationRuns: (planId?: string) => http.get<SOPDisaggregationRun[]>('/sop/disaggregation-runs', { params: planId ? { planId } : {} }).then(r => r.data)
}

// ------- RCCP -------
export interface RCCPProfile {
  id?: string
  itemId: string
  workCenterId: string
  minutesPerUnit: number
}
export interface RCCPLoadRow {
  workCenterId: string
  workCenterCode: string
  workCenterName: string
  month: string
  requiredMinutes: number
  availableMinutes: number
  loadPct: number
}
export const RCCPApi = {
  run:           (workingDays = 22) =>
    http.get<RCCPLoadRow[]>(`/rccp/run?workingDays=${workingDays}`).then(r => r.data),
  profiles:      () => http.get<RCCPProfile[]>('/rccp/profiles').then(r => r.data),
  upsertProfile: (p: RCCPProfile) =>
    http.post<RCCPProfile>('/rccp/profiles', p).then(r => r.data)
}

// ------- ECO (Engineering Change) -------
export interface EngineeringChange {
  id?: string
  ecoNo: string
  title: string
  description: string
  status: 'DRAFT' | 'APPROVED' | 'APPLIED' | 'CANCELLED'
  effectiveDate: string
  requestedBy?: string
  requestedByUserId?: string
  approvedBy?: string
  approvedByUserId?: string
  approvedAt?: string
  appliedBy?: string
  appliedByUserId?: string
  appliedAt?: string
  cancelledBy?: string
  cancelledByUserId?: string
  cancelledAt?: string
  createdAt?: string
}
export interface ECOStatusHistory {
  id: string
  ecoId: string
  fromStatus: string
  toStatus: string
  actorUserId?: string
  actorUsername: string
  occurredAt: string
  effectiveDateSnapshot: string
}
export interface ECOComponent {
  id?: string
  ecoId: string
  action: 'ADD' | 'REMOVE' | 'MODIFY'
  parentId: string
  childId: string
  newQuantity: number
  newScrapPct: number
  notes: string
}
export const ECOApi = {
  list:        () => http.get<EngineeringChange[]>('/eco').then(r => r.data),
  create:      (e: EngineeringChange) => http.post<EngineeringChange>('/eco', e).then(r => r.data),
  approve:     (id: string) => http.post(`/eco/${id}/approve`, {}),
  apply:       (id: string) => http.post(`/eco/${id}/apply`, {}),
  cancel:      (id: string) => http.post(`/eco/${id}/cancel`, {}),
  components:  (id: string) => http.get<ECOComponent[]>(`/eco/${id}/components`).then(r => r.data),
  history:     (id: string) => http.get<ECOStatusHistory[]>(`/eco/${id}/history`).then(r => r.data),
  addComponent:(id: string, c: ECOComponent) =>
    http.post<ECOComponent>(`/eco/${id}/components`, c).then(r => r.data)
}

// ------- AI Agent -------
export interface AgentResponse {
  intent: string
  summary: string
  suggestions?: string[]
  data?: any
}
export const AgentApi = {
  ask: (query: string) =>
    http.post<AgentResponse>('/agent/ask', { query }).then(r => r.data)
}

// ------- Supplier Quality / NCR -------
export type SupplierQualityStatus = 'APPROVED' | 'CONDITIONAL' | 'BLOCKED'
export type NCRSeverity = 'MINOR' | 'MAJOR' | 'CRITICAL'
export type NCRStatus = 'OPEN' | 'IN_REWORK' | 'CLOSED' | 'CANCELLED'
export type NCRDisposition = 'RETURN_TO_SUPPLIER' | 'SCRAP' | 'REWORK' | 'USE_AS_IS'

export interface SupplierQualityProfile {
  supplierName: string
  status: SupplierQualityStatus
  inspectionRequired: boolean
  targetPpm: number
  notes: string
  updatedByUserId?: string
  updatedBy: string
  updatedAt: string
}

export interface SupplierQualityScorecard {
  supplier: string
  profileStatus: SupplierQualityStatus
  inspectionRequired: boolean
  targetPpm: number
  receiptCount: number
  receivedQty: number
  inspectionCount: number
  failInspectionCount: number
  rejectedLotCount: number
  defectQty: number
  ncrCount: number
  openNcrCount: number
  criticalNcrCount: number
  returnedQty: number
  scrappedQty: number
  defectPpm: number
}

export interface SupplierNCR {
  id: string
  ncrNo: string
  supplier: string
  purchaseOrderId?: string
  purchaseReceiptId?: string
  itemId: string
  lotId: string
  inspectionId?: string
  affectedQty: number
  severity: NCRSeverity
  description: string
  status: NCRStatus
  createdByUserId: string
  createdBy: string
  createdAt: string
  closedByUserId?: string
  closedBy: string
  closedAt?: string
  itemCode?: string
  itemName?: string
  lotNo?: string
  poNo?: string
  disposition?: NCRDisposition
  dispositionQty?: number
}

export interface SupplierNCRHistory {
  id: string
  ncrId: string
  fromStatus?: NCRStatus
  toStatus: NCRStatus
  eventType: string
  actorUserId?: string
  actor: string
  occurredAt: string
  notes: string
}

export interface SupplierNCRDispositionRecord {
  id: string
  ncrId: string
  disposition: NCRDisposition
  quantity: number
  notes: string
  inventoryTxnId?: string
  decidedByUserId: string
  decidedBy: string
  decidedAt: string
}

export const SupplierQualityApi = {
  profiles: () => http.get<SupplierQualityProfile[]>('/supplier-quality/suppliers').then(r => r.data),
  upsertProfile: (p: Pick<SupplierQualityProfile, 'supplierName'|'status'|'inspectionRequired'|'targetPpm'|'notes'>) =>
    http.post<SupplierQualityProfile>('/supplier-quality/suppliers', p).then(r => r.data),
  scorecard: () => http.get<SupplierQualityScorecard[]>('/supplier-quality/scorecard').then(r => r.data),
  ncrs: (status = '', supplier = '') =>
    http.get<SupplierNCR[]>('/supplier-quality/ncrs', { params: { status, supplier } }).then(r => r.data),
  createNcr: (p: { lotId: string; inspectionId?: string; affectedQty: number; severity: NCRSeverity; description: string }) =>
    http.post<SupplierNCR>('/supplier-quality/ncrs', p).then(r => r.data),
  history: (id: string) => http.get<SupplierNCRHistory[]>(`/supplier-quality/ncrs/${id}/history`).then(r => r.data),
  disposition: (id: string, p: { disposition: NCRDisposition; quantity: number; notes: string }) =>
    http.post<SupplierNCRDispositionRecord>(`/supplier-quality/ncrs/${id}/disposition`, p).then(r => r.data),
  closeRework: (id: string) => http.post<SupplierNCR>(`/supplier-quality/ncrs/${id}/close-rework`, {}).then(r => r.data)
}

// ------- Real-Time Dispatch / Dynamic Rescheduling / Schedule Adherence -------
export interface DispatchPolicyVersion {
  id: string; versionNo: number; status: 'DRAFT'|'ACTIVE'|'ARCHIVED'; freezeMinutes: number; firmMinutes: number
  startLateThresholdMinutes: number; completionLateThresholdMinutes: number; autoReschedule: boolean
  minAutoIntervalMinutes: number; setupMatchBonus: number; createdBy: string; createdAt: string
}
export interface ScheduleExecutionState { activeRunId: string; policyVersionId: string; activationHistoryId: string; activatedAt: string; updatedAt: string }
export interface DispatchItem {
  activeRunId: string; scheduleOrderId: string; workOrderId: string; woOperationId: string; orderNo: string; itemCode: string; itemName: string
  operationSeq: number; operationDescription: string; workCenterId: string; workCenterCode: string; workCenterName: string; setupFamily: string
  priority: number; dueAt: string; plannedStart?: string; plannedEnd?: string; actualStart?: string; actualEnd?: string
  operationStatus: string; timeFence: 'FROZEN'|'FIRM'|'FLEXIBLE'|'EXECUTED'; dispatchStatus: string; blockedReason: string
  startVarianceMinutes: number; completionVarianceMinutes: number; setupMatch: boolean; dispatchScore: number
}
export interface DispatchBoard { asOf: string; policy: DispatchPolicyVersion; execution: ScheduleExecutionState; items: DispatchItem[] }
export interface ScheduleAdherenceSummary {
  totalOperations: number; startedOperations: number; completedOperations: number; lateStarts: number; lateCompletions: number; blockedOperations: number
  onTimeStartPct: number; onTimeCompletionPct: number; averageStartVarianceMinutes: number; averageCompletionVarianceMinutes: number
}
export interface ScheduleAdherenceSnapshot { id: string; activeRunId: string; policyVersionId: string; asOf: string; resultHash: string; generatedBy: string; createdAt: string }
export interface ScheduleAdherenceResult { snapshot: ScheduleAdherenceSnapshot; summary: ScheduleAdherenceSummary; rows: any[] }
export interface DynamicRescheduleRun {
  id: string; sourceRunId: string; candidateRunId?: string; policyVersionId: string; adherenceSnapshotId?: string; triggerType: string; triggerRef: string; reason: string
  asOf: string; freezeUntil: string; firmUntil: string; horizonDays: number; status: 'EVALUATING'|'ACTIVATED'|'BLOCKED'|'NO_CHANGE'|'FAILED'|'THROTTLED'
  frozenConflicts: number; executionConflicts: number; firmChanges: number; flexibleChanges: number; impactedWorkOrders: number; resultHash?: string; actorType: string; actorUsername: string; createdAt: string; finishedAt?: string
}
export interface DynamicRescheduleChange { id: string; rescheduleRunId: string; workOrderId?: string; sourceRef: string; operationSeq: number; changeType: string; timeFence: string; startShiftMinutes: number; endShiftMinutes: number; frozenConflict: boolean; executionConflict: boolean }
export interface DynamicRescheduleResult { run: DynamicRescheduleRun; changes: DynamicRescheduleChange[]; adherence?: ScheduleAdherenceResult; activation?: any }
export interface RescheduleSignal { id: string; triggerType: string; sourceType: string; sourceRef: string; workCenterId?: string; workOrderId?: string; detectedAt: string; processedAt?: string; processedRunId?: string }
export const DispatchApi = {
  board: (workCenterId?: string) => http.get<DispatchBoard>('/dispatch', { params: workCenterId ? { workCenterId } : {} }).then(r => r.data),
  execution: () => http.get<ScheduleExecutionState>('/schedule-execution').then(r => r.data),
  currentPolicy: () => http.get<DispatchPolicyVersion>('/dispatch-policy/current').then(r => r.data),
  policies: () => http.get<DispatchPolicyVersion[]>('/dispatch-policy-versions').then(r => r.data),
  createPolicy: (p: Partial<DispatchPolicyVersion>) => http.post<DispatchPolicyVersion>('/dispatch-policy-versions', p).then(r => r.data),
  activatePolicy: (id: string) => http.post<DispatchPolicyVersion>(`/dispatch-policy-versions/${id}/activate`, {}).then(r => r.data),
  adherence: () => http.get<ScheduleAdherenceResult>('/schedule-adherence/current').then(r => r.data),
  snapshotAdherence: () => http.post<ScheduleAdherenceResult>('/schedule-adherence/snapshots', {}).then(r => r.data),
  adherenceSnapshots: () => http.get<ScheduleAdherenceSnapshot[]>('/schedule-adherence/snapshots').then(r => r.data),
  reschedule: (body: { triggerType?: string; triggerRef?: string; reason?: string; asOf?: string; horizonDays?: number }) => http.post<DynamicRescheduleResult>('/dynamic-rescheduling/run', body).then(r => r.data),
  processPending: () => http.post<DynamicRescheduleResult | null>('/dynamic-rescheduling/process-pending', {}).then(r => r.data),
  rescheduleRuns: () => http.get<DynamicRescheduleRun[]>('/dynamic-rescheduling/runs').then(r => r.data),
  signals: () => http.get<RescheduleSignal[]>('/dynamic-rescheduling/signals').then(r => r.data)
}

// ------- Production Control Tower / Constraint & Exception Prioritization -------

export type ControlTowerCaseStatus =
  'OPEN' |
  'ACKNOWLEDGED' |
  'ASSIGNED' |
  'IN_PROGRESS' |
  'RESOLVED' |
  'CLOSED'

export type ControlTowerPriorityBand = 'P1' | 'P2' | 'P3' | 'P4'

export type ControlTowerCaseActionType =
  'ACKNOWLEDGE' |
  'ASSIGN' |
  'START' |
  'RESOLVE' |
  'REOPEN' |
  'CLOSE'

export interface ControlTowerRefreshResult {
  asOf: string
  exceptionsEvaluated: number
  casesTouched: number
  snapshotsCreated: number
  recommendationsCreated: number
}

export interface ControlTowerDashboardSummary {
  totalCases: number
  openCases: number
  p1Cases: number
  p2Cases: number
  unassignedCases: number
  revenueAtRisk: number
}

export interface ControlTowerCurrentCase {
  caseId: string
  caseKey: string

  salesOrderId: string
  salesOrderLineId?: string

  exceptionType: string
  firstExceptionId: string
  firstDetectedAt: string
  caseCreatedAt: string

  currentStatus: ControlTowerCaseStatus

  latestActionType?: string
  latestActor?: string
  latestComment?: string
  latestActionAt?: string

  ownerUserId?: string
  ownerUsername?: string

  snapshotId?: string
  planningExceptionId?: string
  peggingRunId?: string
  asOf?: string

  severity?: 'INFO' | 'WARNING' | 'CRITICAL'
  impactDays?: number

  orderValue?: number
  openOrderValue?: number
  orderPriority?: 'EXPEDITE' | 'HIGH' | 'NORMAL'
  serviceClassCode?: string
  revenueAtRisk?: number

  severityScore?: number
  latenessScore?: number
  revenueScore?: number
  customerScore?: number
  materialScore?: number
  capacityScore?: number
  supplierScore?: number
  executionScore?: number
  agingScore?: number

  priorityScore?: number
  priorityBand?: ControlTowerPriorityBand

  rootCauseType?: string
  rootCauseRef?: string
  resultHash?: string
  snapshotCreatedAt?: string

  salesOrderNo: string
  customerNo: string
  customerName: string
  lineNo?: number
  itemCode?: string
  itemName?: string
}

export interface ControlTowerRecommendation {
  id: string
  snapshotId: string
  rankNo: number
  actionType: string
  targetType: string
  targetRef: string
  title: string
  reason: string
  estimatedEffect: Record<string, unknown>
  requiresApproval: boolean
  createdAt: string
}

export interface ControlTowerCaseAction {
  id: string
  caseId: string
  actionType: ControlTowerCaseActionType
  fromStatus: ControlTowerCaseStatus
  toStatus: ControlTowerCaseStatus
  assignedToUserId?: string
  assignedToUsername?: string
  actorUserId: string
  actorUsername: string
  comment: string
  occurredAt: string
}

export interface ControlTowerDashboard {
  asOf: string
  summary: ControlTowerDashboardSummary
  cases: ControlTowerCurrentCase[]
}

export const ControlTowerApi = {
  refresh: (asOf?: string) =>
    http.post<ControlTowerRefreshResult>(
      '/control-tower/refresh',
      asOf ? { asOf } : {}
    ).then(r => r.data),

  dashboard: (
    filters: {
      status?: string
      priorityBand?: string
    } = {}
  ) =>
    http.get<ControlTowerDashboard>(
      '/control-tower',
      { params: filters }
    ).then(r => r.data),

  case: (id: string) =>
    http.get<ControlTowerCurrentCase>(
      `/control-tower/cases/${id}`
    ).then(r => r.data),

  recommendations: (id: string) =>
    http.get<ControlTowerRecommendation[]>(
      `/control-tower/cases/${id}/recommendations`
    ).then(r => r.data),

  actions: (id: string) =>
    http.get<ControlTowerCaseAction[]>(
      `/control-tower/cases/${id}/actions`
    ).then(r => r.data),

  act: (
    id: string,
    body: {
      actionType: ControlTowerCaseActionType
      assignedToUserId?: string
      comment?: string
    }
  ) =>
    http.post<ControlTowerCaseAction>(
      `/control-tower/cases/${id}/actions`,
      body
    ).then(r => r.data)
}
