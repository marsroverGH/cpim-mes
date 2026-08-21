package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
)

// server wraps the service container so HTTP handlers can be methods.
type server struct {
	s *service.Services
}

func NewRouter(s *service.Services) http.Handler {
	srv := &server{s: s}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		AllowCredentials: false,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api", func(api chi.Router) {
		// Public: API documentation
		api.Get("/openapi.json", srv.serveOpenAPI)
		api.Get("/docs", srv.serveSwaggerUI)

		// Auth (no JWT required)
		api.Post("/auth/login", srv.login)

		// Everything below requires a valid JWT
		api.Group(func(secured chi.Router) {
			secured.Use(authMiddleware(srv.s.Auth))
			secured.Use(auditMiddleware(srv.s.Audit))

			secured.Get("/auth/me", srv.me)

			secured.Get("/items", srv.listItems)
			secured.With(requirePermission(PermItemMasterWrite)).Post("/items", srv.createItem)
			secured.Get("/items/{id}", srv.getItem)
			secured.With(requirePermission(PermItemMasterWrite)).Put("/items/{id}", srv.updateItem)
			secured.With(requirePermission(PermItemMasterWrite)).Delete("/items/{id}", srv.deleteItem)

			// CSV import / export for items
			secured.Get("/items/export.csv", srv.exportItemsCSV)
			secured.With(requirePermission(PermItemMasterWrite)).Post("/items/import", srv.importItemsCSV)

			secured.Get("/items/{id}/bom", srv.getBOM)
			secured.With(requirePermission(PermBOMWrite)).Post("/items/{id}/bom", srv.addBOMComponent)
			secured.With(requirePermission(PermBOMWrite)).Delete("/bom/{compId}", srv.deleteBOMComponent)
			secured.Get("/items/{id}/explode", srv.explodeBOM)
			secured.With(requirePermission(PermBOMWrite)).Post("/items/recompute-llc", srv.recomputeLLC)

			secured.Get("/demand", srv.listDemand)

			secured.Get("/mps", srv.listMPS)
			secured.With(requirePermission(PermMPSWrite)).Post("/mps", srv.upsertMPS)

			secured.Get("/inventory/on-hand", srv.onHand)
			secured.Get("/inventory/reconciliation", srv.inventoryReconciliation)
			secured.Get("/inventory/{itemId}/transactions", srv.itemTxns)
			secured.With(requirePermission(PermInventoryAdjust)).Post("/inventory/transactions", srv.postTxn)

			secured.Get("/work-orders", srv.listWorkOrders)
			secured.With(requirePermission(PermWOPlan)).Post("/work-orders", srv.createWorkOrder)
			secured.With(requirePermission(PermWOPlan)).Put("/work-orders/{id}/status", srv.updateWorkOrderStatus)

			secured.Get("/purchase-orders", srv.listPurchases)
			secured.Get("/purchase-orders/{id}/receipts", srv.listPurchaseReceipts)
			secured.With(requirePermission(PermPOPlan)).Post("/purchase-orders", srv.createPurchase)
			secured.Get("/purchase-orders/{id}/supplier-schedule", srv.listSupplierScheduleEvents)
			secured.With(requirePermission(PermSupplierScheduleManage)).Post("/purchase-orders/{id}/supplier-schedule/events", srv.recordSupplierScheduleEvent)
			secured.Get("/supplier-scheduling/reliability", srv.listSupplierReliability)
			secured.Get("/supplier-scheduling/reliability-runs", srv.listSupplierReliabilityRuns)
			secured.Get("/supplier-scheduling/reliability-runs/{id}", srv.getSupplierReliabilityRun)
			secured.With(requirePermission(PermSupplierReliabilityRun)).Post("/supplier-scheduling/reliability/refresh", srv.refreshSupplierReliability)

			// Statistical Safety Stock / Inventory Policy
			secured.Get("/inventory-policies", srv.listInventoryPolicies)
			secured.Get("/inventory-policy-versions", srv.listInventoryPolicyVersions)
			secured.With(requirePermission(PermInventoryPolicyManage)).Post("/inventory-policy-versions", srv.createInventoryPolicyVersion)
			secured.With(requirePermission(PermInventoryPolicyManage)).Post("/inventory-policy-versions/{id}/activate", srv.activateInventoryPolicyVersion)
			secured.With(requirePermission(PermInventoryPolicyManage)).Post("/inventory-policy-versions/{id}/archive", srv.archiveInventoryPolicyVersion)
			secured.With(requirePermission(PermInventoryPolicyRun)).Post("/inventory-policies/refresh", srv.refreshInventoryPolicies)
			secured.Get("/inventory-policy-runs", srv.listInventoryPolicyRuns)
			secured.Get("/inventory-policy-runs/{id}", srv.getInventoryPolicyRun)

			// Customers / Sales Orders
			secured.Get("/customers", srv.listCustomers)
			secured.With(requirePermission(PermSalesOrderManage)).Post("/customers", srv.createCustomer)
			secured.With(requirePermission(PermSalesOrderManage)).Put("/customers/{id}", srv.updateCustomer)
			secured.Get("/sales-orders", srv.listSalesOrders)
			secured.With(requirePermission(PermSalesOrderManage)).Post("/sales-orders", srv.createSalesOrder)
			secured.Get("/sales-orders/{id}", srv.getSalesOrder)
			secured.With(requirePermission(PermSalesOrderManage)).Post("/sales-orders/{id}/confirm", srv.confirmSalesOrder)
			secured.With(requirePermission(PermSalesOrderManage)).Post("/sales-orders/{id}/cancel", srv.cancelSalesOrder)
			secured.With(requirePermission(PermSalesOrderManage)).Post("/sales-order-lines/{id}/allocate", srv.allocateSalesOrderLine)
			secured.With(requirePermission(PermSalesOrderManage)).Post("/sales-order-lines/{id}/release-allocation", srv.releaseSalesOrderLine)
			secured.With(requirePermission(PermSalesOrderShip)).Post("/sales-order-lines/{id}/ship", srv.shipSalesOrderLine)
			secured.With(requirePermission(PermSalesOrderPromise)).Post("/sales-orders/{id}/promise/check", srv.checkSalesOrderPromise)
			secured.With(requirePermission(PermSalesOrderPromise)).Post("/sales-orders/{id}/promise/accept", srv.acceptSalesOrderPromise)
			secured.Get("/sales-orders/{id}/promise-runs", srv.listSalesOrderPromiseRuns)
			secured.Get("/order-promise-runs/{id}", srv.getOrderPromiseRun)

			// Backorder Processing / Product Allocation
			secured.Get("/customer-service-classes", srv.listCustomerServiceClasses)
			secured.With(requirePermission(PermProductAllocation)).Put("/customers/{id}/service-class", srv.setCustomerServiceClass)
			secured.With(requirePermission(PermBackorderRun)).Put("/sales-orders/{id}/priority", srv.setSalesOrderPriority)
			secured.Get("/product-allocation-plans", srv.listProductAllocationPlans)
			secured.With(requirePermission(PermProductAllocation)).Post("/product-allocation-plans", srv.createProductAllocationPlan)
			secured.With(requirePermission(PermProductAllocation)).Post("/product-allocation-plans/{id}/activate", srv.activateProductAllocationPlan)
			secured.With(requirePermission(PermProductAllocation)).Post("/product-allocation-plans/{id}/deactivate", srv.deactivateProductAllocationPlan)
			secured.With(requirePermission(PermBackorderRun)).Post("/backorders/preview", srv.previewBackorders)
			secured.With(requirePermission(PermBackorderRun)).Post("/backorders/publish", srv.publishBackorders)
			secured.Get("/backorders/runs", srv.listBackorderRuns)
			secured.Get("/backorders/runs/{id}", srv.getBackorderRun)

			// Full Pegging / Exception Management
			secured.With(requirePermission(PermPeggingRun)).Post("/sales-orders/{id}/pegging/run", srv.runSalesOrderPegging)
			secured.Get("/sales-orders/{id}/pegging-runs", srv.listSalesOrderPeggingRuns)
			secured.Get("/pegging-runs/{id}", srv.getPeggingRun)
			secured.With(requirePermission(PermExceptionManage)).Post("/planning-exceptions/scan", srv.scanPlanningExceptions)
			secured.Get("/planning-exceptions", srv.listPlanningExceptions)
			secured.With(requirePermission(PermExceptionManage)).Post("/planning-exceptions/{id}/actions", srv.actOnPlanningException)

			secured.With(requirePermission(PermMRPRun)).Post("/mrp/run", srv.runMRP)

			// Work Centers
			// Maintenance / Capacity Downtime
			secured.Get("/maintenance-events", srv.listMaintenanceEvents)
			secured.Get("/maintenance-events/{id}", srv.getMaintenanceEvent)
			secured.With(requirePermission(PermMaintenanceManage)).Post("/maintenance-events", srv.createMaintenanceEvent)
			secured.With(requirePermission(PermMaintenanceManage)).Post("/maintenance-events/{id}/revisions", srv.reviseMaintenanceEvent)

			secured.Get("/work-centers", srv.listWorkCenters)
			secured.With(requirePermission(PermCapacityMaster)).Post("/work-centers", srv.createWorkCenter)
			secured.With(requirePermission(PermCapacityMaster)).Put("/work-centers/{id}", srv.updateWorkCenter)
			secured.With(requirePermission(PermCapacityMaster)).Delete("/work-centers/{id}", srv.deleteWorkCenter)
			secured.Get("/work-centers/{id}/setup-matrix", srv.workCenterSetupMatrix)
			secured.With(requirePermission(PermCapacityMaster)).Post("/work-centers/{id}/setup-matrix", srv.upsertWorkCenterSetupMatrix)
			secured.With(requirePermission(PermCapacityMaster)).Delete("/work-center-setup-matrix/{id}", srv.deleteWorkCenterSetupMatrix)

			// Routings
			secured.Get("/routings", srv.listRoutings)
			secured.With(requirePermission(PermRoutingMaster)).Post("/routings", srv.createRouting)
			secured.Get("/routings/{id}/operations", srv.routingOps)
			secured.With(requirePermission(PermRoutingMaster)).Post("/routings/{id}/operations", srv.addRoutingOp)
			secured.With(requirePermission(PermRoutingMaster)).Put("/routing-operations/{opId}", srv.updateRoutingOp)
			secured.With(requirePermission(PermRoutingMaster)).Delete("/routing-operations/{opId}", srv.deleteRoutingOp)
			secured.Get("/routing-operations/{opId}/alternatives", srv.routingOpAlternatives)
			secured.With(requirePermission(PermRoutingMaster)).Post("/routing-operations/{opId}/alternatives", srv.addRoutingOpAlternative)
			secured.With(requirePermission(PermRoutingMaster)).Delete("/routing-operation-alternatives/{id}", srv.deleteRoutingOpAlternative)

			// CRP & Cost Rollup & ABC
			secured.With(requirePermission(PermCRPRun)).Post("/crp/run", srv.runCRP)
			secured.With(requirePermission(PermCRPRun)).Post("/crp/schedule", srv.runFiniteCRP)
			secured.Get("/crp/schedule-runs", srv.listCRPScheduleRuns)
			secured.Get("/crp/schedule-runs/{id}", srv.getCRPScheduleRun)
			secured.With(requirePermission(PermCRPRun)).Post("/detailed-scheduling/run", srv.runDetailedSchedule)
			secured.Get("/detailed-scheduling/runs", srv.listDetailedScheduleRuns)
			secured.Get("/detailed-scheduling/runs/{id}", srv.getDetailedScheduleRun)
			secured.Get("/cost-rollup", srv.runCostRollup)
			secured.Get("/abc-analysis", srv.runABC)

			// Lots / Traceability
			secured.Get("/lots", srv.listLots)
			secured.With(requirePermission(PermInventoryAdjust)).Post("/lots", srv.createLot)
			secured.Get("/lots/{id}/movements", srv.lotMovements)
			secured.With(requirePermission(PermInventoryAdjust)).Post("/lots/{id}/movements", srv.addLotMovement)
			secured.Get("/lots/{id}/where-used", srv.lotWhereUsed)
			secured.Get("/items/{itemId}/lots", srv.lotsByItem)

			// Audit log (admin/planner can view)
			secured.With(requirePermission(PermAuditRead)).Get("/audit-log", srv.listAudit)

			// Forecasting / versioning / consumption
			secured.With(requirePermission(PermForecastRun)).Post("/forecast/run", srv.runForecast)
			secured.Get("/forecast/runs", srv.listForecastRuns)
			secured.Get("/forecast/runs/{id}", srv.getForecastRun)
			secured.With(requirePermission(PermForecastRun)).Post("/forecast/runs/{id}/activate", srv.activateForecastRun)
			secured.Get("/forecast/runs/{id}/consumption", srv.forecastConsumption)
			secured.With(requirePermission(PermMPSWrite)).Post("/forecast/runs/{id}/apply-to-mps", srv.applyForecastConsumptionToMPS)

			// Cycle Count
			secured.Get("/cycle-counts", srv.listCycleCounts)
			secured.With(requirePermission(PermCycleCountPlan)).Post("/cycle-counts/generate", srv.generateCycleCounts)
			secured.With(requirePermission(PermCycleCountRecord)).Post("/cycle-counts/{id}/record", srv.recordCycleCount)

			// End-to-end workflow (受注→購買→製造→完成)
			secured.Get("/inventory/balance", srv.inventoryBalance)
			secured.With(requirePermission(PermWOPlan)).Post("/work-orders/{id}/release", srv.releaseWorkOrder)
			secured.Get("/work-orders/{id}/bom-snapshot", srv.getWorkOrderBOMSnapshot)
			secured.With(requirePermission(PermWOExecute)).Post("/work-orders/{id}/complete", srv.completeWorkOrder)
			secured.With(requirePermission(PermPOReceive)).Post("/purchase-orders/{id}/receive", srv.receivePurchase)

			// Working calendars
			secured.Get("/calendars", srv.listCalendars)
			secured.With(requirePermission(PermCalendarWrite)).Post("/calendars", srv.createCalendar)
			secured.Get("/calendars/{id}", srv.getCalendar)
			secured.With(requirePermission(PermCalendarWrite)).Put("/calendars/{id}", srv.updateCalendar)
			secured.With(requirePermission(PermCalendarWrite)).Delete("/calendars/{id}", srv.deleteCalendar)
			secured.Get("/calendars/{id}/exceptions", srv.listExceptions)
			secured.With(requirePermission(PermCalendarWrite)).Post("/calendars/{id}/exceptions", srv.addException)
			secured.With(requirePermission(PermCalendarWrite)).Delete("/calendar-exceptions/{exId}", srv.deleteException)

			// WIP / Progress (部分完成)
			secured.With(requirePermission(PermWOExecute)).Post("/work-orders/{id}/progress", srv.updateWOProgress)

			// ATP (Available-to-Promise)
			secured.Get("/items/{itemId}/atp", srv.runATP)

			// Quality
			secured.Get("/lots/{id}/inspections", srv.listLotInspections)
			secured.Get("/lots/{id}/quality-history", srv.lotQualityHistory)
			secured.With(requirePermission(PermQualityRecord)).Post("/lots/{id}/inspections", srv.recordInspection)
			secured.Get("/quality/recent", srv.recentInspections)

			// Supplier Quality / NCR
			secured.Get("/supplier-quality/suppliers", srv.listSupplierQualityProfiles)
			secured.With(requirePermission(PermSupplierQualityManage)).Post("/supplier-quality/suppliers", srv.upsertSupplierQualityProfile)
			secured.Get("/supplier-quality/scorecard", srv.supplierQualityScorecards)
			secured.Get("/supplier-quality/ncrs", srv.listSupplierNCRs)
			secured.With(requirePermission(PermNCRCreate)).Post("/supplier-quality/ncrs", srv.createSupplierNCR)
			secured.Get("/supplier-quality/ncrs/{id}/history", srv.supplierNCRHistory)
			secured.With(requirePermission(PermNCRDisposition)).Post("/supplier-quality/ncrs/{id}/disposition", srv.dispositionSupplierNCR)
			secured.With(requirePermission(PermNCRDisposition)).Post("/supplier-quality/ncrs/{id}/close-rework", srv.closeSupplierNCRRework)

			// MRP Action Messages
			secured.Get("/mrp/action-messages", srv.listActionMessages)

			// Shop Floor Control
			secured.Get("/shop-floor/active", srv.shopFloorActive)
			secured.Get("/work-orders/{id}/operations", srv.listWOOperations)
			secured.With(requirePermission(PermShopFloorExecute)).Post("/wo-operations/{opId}/start", srv.startOperation)
			secured.With(requirePermission(PermShopFloorExecute)).Post("/wo-operations/{opId}/stop", srv.stopOperation)
			secured.With(requirePermission(PermShopFloorExecute)).Post("/wo-operations/{opId}/complete", srv.completeOperation)
			secured.With(requirePermission(PermShopFloorExecute)).Post("/wo-operations/{opId}/scrap", srv.scrapOperation)
			secured.Get("/wo-operations/{opId}/logs", srv.operationLogs)

			// OEE / Production Performance / Actual Capacity Feedback
			secured.With(requirePermission(PermProductionPerformanceRun)).Post("/production-performance/runs", srv.runProductionPerformance)
			secured.Get("/production-performance/runs", srv.listProductionPerformanceRuns)
			secured.Get("/production-performance/runs/{id}", srv.getProductionPerformanceRun)
			secured.Get("/capacity-feedback", srv.listCapacityFeedback)
			secured.With(requirePermission(PermCapacityFeedbackManage)).Post("/capacity-feedback/{id}/activate", srv.activateCapacityFeedback)
			secured.With(requirePermission(PermCapacityFeedbackManage)).Post("/capacity-feedback/{id}/archive", srv.archiveCapacityFeedback)

			// KPI Dashboard
			secured.Get("/kpi/dashboard", srv.kpiDashboard)

			// S&OP
			secured.Get("/item-groups", srv.listItemGroups)
			secured.With(requirePermission(PermItemGroupWrite)).Post("/item-groups", srv.createItemGroup)
			secured.Get("/sop/plans", srv.listSOPPlans)
			secured.With(requirePermission(PermSOPWrite)).Post("/sop/plans", srv.upsertSOPPlan)
			secured.With(requirePermission(PermSOPWrite)).Delete("/sop/plans/{id}", srv.deleteSOPPlan)
			secured.Get("/sop/product-mix/versions", srv.listSOPProductMixVersions)
			secured.With(requirePermission(PermSOPWrite)).Post("/sop/product-mix/versions", srv.createSOPProductMixVersion)
			secured.With(requirePermission(PermSOPWrite)).Post("/sop/product-mix/versions/{id}/activate", srv.activateSOPProductMixVersion)
			secured.Get("/sop/plans/{id}/disaggregation/preview", srv.previewSOPDisaggregation)
			secured.With(requirePermission(PermSOPWrite)).Post("/sop/plans/{id}/disaggregate", srv.applySOPDisaggregation)
			secured.Get("/sop/disaggregation-runs", srv.listSOPDisaggregationRuns)

			// RCCP
			secured.Get("/rccp/run", srv.runRCCP)
			secured.Get("/rccp/profiles", srv.listRCCPProfiles)
			secured.With(requirePermission(PermRCCPWrite)).Post("/rccp/profiles", srv.upsertRCCPProfile)

			// Engineering Change Orders (ECO/ECN)
			secured.Get("/eco", srv.listECOs)
			secured.With(requirePermission(PermECODraft)).Post("/eco", srv.createECO)
			secured.With(requirePermission(PermECOApproveApply)).Post("/eco/{id}/approve", srv.approveECO)
			secured.With(requirePermission(PermECOApproveApply)).Post("/eco/{id}/apply", srv.applyECO)
			secured.With(requirePermission(PermECOApproveApply)).Post("/eco/{id}/cancel", srv.cancelECO)
			secured.Get("/eco/{id}/components", srv.listECOComponents)
			secured.Get("/eco/{id}/history", srv.listECOHistory)
			secured.With(requirePermission(PermECODraft)).Post("/eco/{id}/components", srv.addECOComponent)

			// AI Agent (rule-based, LLM-extensible)
			secured.With(requirePermission(PermAgentUse)).Post("/agent/ask", srv.askAgent)
		})
	})

	return r
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	// nil スライスは [] として返す (フロントエンドが null を配列扱いして落ちるのを防ぐ)
	if v != nil {
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			v = reflect.MakeSlice(rv.Type(), 0, 0).Interface()
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	// *domain.AppError があれば構造化情報を返す
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		status := appErr.Status
		if status == 0 {
			status = code
		}
		writeJSON(w, status, appErr)
		return
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func parseUUID(r *http.Request, name string) (uuid.UUID, error) {
	s := chi.URLParam(r, name)
	if s == "" {
		return uuid.Nil, errors.New("missing id")
	}
	return uuid.Parse(s)
}

// ---------- Items ----------

func (h *server) listItems(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.Items.List(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// POST /api/items/recompute-llc — BOM 変更後に低レベルコードを再計算
func (h *server) recomputeLLC(w http.ResponseWriter, r *http.Request) {
	if err := h.s.Items.RecomputeLLC(r.Context()); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"status": "recomputed"})
}

func (h *server) getItem(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	it, err := h.s.Items.Get(r.Context(), id)
	if err != nil {
		writeError(w, 404, err)
		return
	}
	writeJSON(w, 200, it)
}

func (h *server) createItem(w http.ResponseWriter, r *http.Request) {
	var it domain.Item
	if err := json.NewDecoder(r.Body).Decode(&it); err != nil {
		writeError(w, 400, err)
		return
	}
	if validateBody(w, &it) {
		return
	}
	if err := h.s.Items.Create(r.Context(), &it); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, it)
}

func (h *server) updateItem(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var it domain.Item
	if err := json.NewDecoder(r.Body).Decode(&it); err != nil {
		writeError(w, 400, err)
		return
	}
	it.ID = id
	if validateBody(w, &it) {
		return
	}
	if err := h.s.Items.Update(r.Context(), &it); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, it)
}

func (h *server) deleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.Items.Delete(r.Context(), id); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

// ---------- BOM ----------

func (h *server) getBOM(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.BOM.ComponentsOf(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) addBOMComponent(w http.ResponseWriter, r *http.Request) {
	parent, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var c domain.BOMComponent
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, 400, err)
		return
	}
	c.ParentID = parent
	if err := h.s.BOM.Add(r.Context(), &c); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, c)
}

func (h *server) deleteBOMComponent(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "compId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.BOM.Delete(r.Context(), id); err != nil {
		writeError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

func (h *server) explodeBOM(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	qty := 1.0
	if q := r.URL.Query().Get("qty"); q != "" {
		if f, err := strconv.ParseFloat(q, 64); err == nil {
			qty = f
		}
	}
	rows, err := h.s.BOM.Explode(r.Context(), id, qty)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// ---------- Demand / MPS ----------

func (h *server) listDemand(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.Demand.List(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createDemand(w http.ResponseWriter, r *http.Request) {
	var d domain.DemandForecast
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.Demand.Create(r.Context(), &d); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, d)
}

func (h *server) listMPS(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.MPS.List(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) upsertMPS(w http.ResponseWriter, r *http.Request) {
	var m domain.MPSEntry
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.MPS.Upsert(r.Context(), &m); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, m)
}

// ---------- Inventory ----------

func (h *server) onHand(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.Inventory.OnHand(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) inventoryReconciliation(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.Inventory.Reconciliation(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) itemTxns(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "itemId")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	rows, err := h.s.Inventory.Transactions(r.Context(), id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) postTxn(w http.ResponseWriter, r *http.Request) {
	var t domain.InventoryTxn
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.Inventory.Post(r.Context(), &t); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, t)
}

// ---------- Work Orders ----------

func (h *server) listWorkOrders(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.WorkOrders.List(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createWorkOrder(w http.ResponseWriter, r *http.Request) {
	var wo domain.WorkOrder
	if err := json.NewDecoder(r.Body).Decode(&wo); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.WorkOrders.Create(r.Context(), &wo); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, wo)
}

func (h *server) updateWorkOrderStatus(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.WorkOrders.UpdateStatus(r.Context(), id, body.Status); err != nil {
		writeError(w, 400, err)
		return
	}
	w.WriteHeader(204)
}

// ---------- Purchase Orders ----------

func (h *server) listPurchases(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.Purchases.List(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (h *server) createPurchase(w http.ResponseWriter, r *http.Request) {
	var p domain.PurchaseOrder
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := h.s.Purchases.Create(r.Context(), &p); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, p)
}

// ---------- MRP ----------

func (h *server) runMRP(w http.ResponseWriter, r *http.Request) {
	var req service.MRPRequest
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	rows, err := h.s.MRP.Run(r.Context(), req)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}
