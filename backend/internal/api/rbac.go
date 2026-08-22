package api

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/service"
)

// Permission is a backend-enforced capability. Route authorization is expressed
// in permissions rather than UI visibility so callers cannot bypass RBAC by
// invoking the API directly.
type Permission string

const (
	PermItemMasterWrite          Permission = "item.master.write"
	PermBOMWrite                 Permission = "bom.write"
	PermDemandWrite              Permission = "planning.demand.write"
	PermMPSWrite                 Permission = "planning.mps.write"
	PermInventoryAdjust          Permission = "inventory.adjust"
	PermWOPlan                   Permission = "wo.plan"
	PermWOExecute                Permission = "wo.execute"
	PermPOPlan                   Permission = "po.plan"
	PermPOReceive                Permission = "po.receive"
	PermSupplierScheduleManage   Permission = "supplier.schedule.manage"
	PermSupplierReliabilityRun   Permission = "supplier.reliability.run"
	PermInventoryPolicyManage    Permission = "inventory.policy.manage"
	PermInventoryPolicyRun       Permission = "inventory.policy.run"
	PermMaintenanceManage        Permission = "maintenance.manage"
	PermProductionPerformanceRun Permission = "production.performance.run"
	PermCapacityFeedbackManage   Permission = "capacity.feedback.manage"
	PermDispatchManage           Permission = "planning.dispatch.manage"
	PermDynamicReschedule        Permission = "planning.reschedule.run"
	PermSalesOrderManage         Permission = "sales-order.manage"
	PermSalesOrderShip           Permission = "sales-order.ship"
	PermSalesOrderPromise        Permission = "sales-order.promise"
	PermBackorderRun             Permission = "sales-order.backorder"
	PermProductAllocation        Permission = "sales-order.product-allocation"
	PermPeggingRun               Permission = "planning.pegging.run"
	PermExceptionManage          Permission = "planning.exception.manage"
	PermControlTowerRefresh      Permission = "planning.control_tower.refresh"
	PermControlTowerManage       Permission = "planning.control_tower.manage"
	PermRecoveryScenarioManage   Permission = "planning.recovery_scenario.manage"
	PermRecoverySimulationRun    Permission = "planning.recovery_scenario.simulate"
	PermRecoveryPublish          Permission = "planning.recovery_scenario.publish"
	PermMRPRun                   Permission = "planning.mrp.run"
	PermCapacityMaster           Permission = "capacity.master.write"
	PermRoutingMaster            Permission = "routing.master.write"
	PermCRPRun                   Permission = "planning.crp.run"
	PermForecastRun              Permission = "planning.forecast.run"
	PermCycleCountPlan           Permission = "inventory.cyclecount.plan"
	PermCycleCountRecord         Permission = "inventory.cyclecount.record"
	PermCalendarWrite            Permission = "calendar.write"
	PermQualityRecord            Permission = "quality.record"
	PermSupplierQualityManage    Permission = "quality.supplier.manage"
	PermNCRCreate                Permission = "quality.ncr.create"
	PermNCRDisposition           Permission = "quality.ncr.disposition"
	PermShopFloorExecute         Permission = "shopfloor.execute"
	PermItemGroupWrite           Permission = "item-group.write"
	PermSOPWrite                 Permission = "planning.sop.write"
	PermRCCPWrite                Permission = "planning.rccp.write"
	PermECODraft                 Permission = "eco.draft"
	PermECOApproveApply          Permission = "eco.approve-apply"
	PermAuditRead                Permission = "audit.read"
	PermAgentUse                 Permission = "agent.use"
)

// rolePermissions is intentionally explicit. Admin is handled as an all-access
// role below; all other roles receive only the capabilities listed here.
var rolePermissions = map[domain.Role]map[Permission]struct{}{
	domain.RoleViewer: {
		PermAgentUse: {},
	},
	domain.RoleOperator: {
		PermWOExecute:        {},
		PermPOReceive:        {},
		PermSalesOrderShip:   {},
		PermCycleCountRecord: {},
		PermQualityRecord:    {},
		PermNCRCreate:        {},
		PermShopFloorExecute: {},
		PermAgentUse:         {},
	},
	domain.RolePlanner: {
		PermBOMWrite:                 {},
		PermDemandWrite:              {},
		PermMPSWrite:                 {},
		PermWOPlan:                   {},
		PermWOExecute:                {},
		PermPOPlan:                   {},
		PermPOReceive:                {},
		PermSupplierScheduleManage:   {},
		PermSupplierReliabilityRun:   {},
		PermInventoryPolicyManage:    {},
		PermInventoryPolicyRun:       {},
		PermMaintenanceManage:        {},
		PermProductionPerformanceRun: {},
		PermCapacityFeedbackManage:   {},
		PermDispatchManage:           {},
		PermDynamicReschedule:        {},
		PermSalesOrderManage:         {},
		PermSalesOrderShip:           {},
		PermSalesOrderPromise:        {},
		PermBackorderRun:             {},
		PermProductAllocation:        {},
		PermPeggingRun:               {},
		PermExceptionManage:          {},
		PermControlTowerRefresh:      {},
		PermControlTowerManage:       {},
		PermRecoveryScenarioManage:   {},
		PermRecoverySimulationRun:    {},
		PermRecoveryPublish:          {},
		PermMRPRun:                   {},
		PermCRPRun:                   {},
		PermForecastRun:              {},
		PermCycleCountPlan:           {},
		PermCycleCountRecord:         {},
		PermQualityRecord:            {},
		PermSupplierQualityManage:    {},
		PermNCRCreate:                {},
		PermNCRDisposition:           {},
		PermShopFloorExecute:         {},
		PermSOPWrite:                 {},
		PermRCCPWrite:                {},
		PermECODraft:                 {},
		PermAuditRead:                {},
		PermAgentUse:                 {},
	},
}

func roleHasPermission(role domain.Role, permission Permission) bool {
	if role == domain.RoleAdmin {
		return true
	}
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	_, ok = perms[permission]
	return ok
}

func permissionsForRole(role domain.Role) []Permission {
	all := []Permission{
		PermItemMasterWrite, PermBOMWrite, PermDemandWrite, PermMPSWrite,
		PermInventoryAdjust, PermWOPlan, PermWOExecute, PermPOPlan, PermPOReceive, PermSupplierScheduleManage, PermSupplierReliabilityRun, PermInventoryPolicyManage, PermInventoryPolicyRun, PermMaintenanceManage, PermProductionPerformanceRun, PermCapacityFeedbackManage, PermDispatchManage, PermDynamicReschedule, PermSalesOrderManage, PermSalesOrderShip, PermSalesOrderPromise, PermBackorderRun, PermProductAllocation, PermPeggingRun, PermExceptionManage, PermControlTowerRefresh, PermControlTowerManage, PermRecoveryScenarioManage, PermRecoverySimulationRun, PermRecoveryPublish,
		PermMRPRun, PermCapacityMaster, PermRoutingMaster, PermCRPRun, PermForecastRun,
		PermCycleCountPlan, PermCycleCountRecord, PermCalendarWrite, PermQualityRecord,
		PermSupplierQualityManage, PermNCRCreate, PermNCRDisposition, PermShopFloorExecute, PermItemGroupWrite, PermSOPWrite, PermRCCPWrite,
		PermECODraft, PermECOApproveApply, PermAuditRead, PermAgentUse,
	}
	out := make([]Permission, 0, len(all))
	for _, p := range all {
		if roleHasPermission(role, p) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// requirePermission rejects authenticated users whose current role does not
// carry the requested capability. authMiddleware refreshes the role from the
// users table before this middleware runs, so role changes take effect without
// waiting for the JWT to expire.
func requirePermission(permission Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
			if claims == nil {
				writeError(w, http.StatusUnauthorized, strErr("not authenticated"))
				return
			}
			if !roleHasPermission(claims.Role, permission) {
				writeError(w, http.StatusForbidden,
					strErr(fmt.Sprintf("forbidden: missing permission %s", permission)))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
