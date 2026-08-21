package api

import (
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
)

func TestRBACCriticalPermissions(t *testing.T) {
	tests := []struct {
		name string
		role domain.Role
		perm Permission
		want bool
	}{
		{"viewer cannot edit BOM", domain.RoleViewer, PermBOMWrite, false},
		{"operator cannot edit BOM", domain.RoleOperator, PermBOMWrite, false},
		{"planner can edit BOM", domain.RolePlanner, PermBOMWrite, true},
		{"planner cannot manual-adjust inventory", domain.RolePlanner, PermInventoryAdjust, false},
		{"operator cannot manual-adjust inventory", domain.RoleOperator, PermInventoryAdjust, false},
		{"operator can complete WO", domain.RoleOperator, PermWOExecute, true},
		{"operator cannot release WO", domain.RoleOperator, PermWOPlan, false},
		{"planner can release WO", domain.RolePlanner, PermWOPlan, true},
		{"operator can receive PO", domain.RoleOperator, PermPOReceive, true},
		{"operator cannot create PO", domain.RoleOperator, PermPOPlan, false},
		{"planner can create PO", domain.RolePlanner, PermPOPlan, true},
		{"operator cannot manage supplier schedule", domain.RoleOperator, PermSupplierScheduleManage, false},
		{"planner can manage supplier schedule", domain.RolePlanner, PermSupplierScheduleManage, true},
		{"operator cannot run supplier reliability", domain.RoleOperator, PermSupplierReliabilityRun, false},
		{"planner can run supplier reliability", domain.RolePlanner, PermSupplierReliabilityRun, true},
		{"operator cannot manage inventory policy", domain.RoleOperator, PermInventoryPolicyManage, false},
		{"planner can manage inventory policy", domain.RolePlanner, PermInventoryPolicyManage, true},
		{"operator cannot refresh inventory policy", domain.RoleOperator, PermInventoryPolicyRun, false},
		{"planner can refresh inventory policy", domain.RolePlanner, PermInventoryPolicyRun, true},
		{"operator cannot manage maintenance", domain.RoleOperator, PermMaintenanceManage, false},
		{"planner can manage maintenance", domain.RolePlanner, PermMaintenanceManage, true},
		{"operator cannot manage sales orders", domain.RoleOperator, PermSalesOrderManage, false},
		{"operator can ship sales orders", domain.RoleOperator, PermSalesOrderShip, true},
		{"planner can manage sales orders", domain.RolePlanner, PermSalesOrderManage, true},
		{"operator cannot promise sales orders", domain.RoleOperator, PermSalesOrderPromise, false},
		{"planner can promise sales orders", domain.RolePlanner, PermSalesOrderPromise, true},
		{"operator cannot run BOP", domain.RoleOperator, PermBackorderRun, false},
		{"planner can run BOP", domain.RolePlanner, PermBackorderRun, true},
		{"operator cannot manage product allocation", domain.RoleOperator, PermProductAllocation, false},
		{"planner can manage product allocation", domain.RolePlanner, PermProductAllocation, true},
		{"operator cannot run full pegging", domain.RoleOperator, PermPeggingRun, false},
		{"planner can run full pegging", domain.RolePlanner, PermPeggingRun, true},
		{"operator cannot manage planning exceptions", domain.RoleOperator, PermExceptionManage, false},
		{"planner can manage planning exceptions", domain.RolePlanner, PermExceptionManage, true},
		{"operator can create NCR", domain.RoleOperator, PermNCRCreate, true},
		{"operator cannot disposition NCR", domain.RoleOperator, PermNCRDisposition, false},
		{"planner can disposition NCR", domain.RolePlanner, PermNCRDisposition, true},
		{"planner can manage supplier quality", domain.RolePlanner, PermSupplierQualityManage, true},
		{"planner can draft ECO", domain.RolePlanner, PermECODraft, true},
		{"planner cannot approve/apply ECO", domain.RolePlanner, PermECOApproveApply, false},
		{"admin can approve/apply ECO", domain.RoleAdmin, PermECOApproveApply, true},
		{"admin can adjust inventory", domain.RoleAdmin, PermInventoryAdjust, true},
		{"unknown role has no permission", domain.Role("unknown"), PermAgentUse, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := roleHasPermission(tt.role, tt.perm); got != tt.want {
				t.Fatalf("roleHasPermission(%q, %q)=%v want %v", tt.role, tt.perm, got, tt.want)
			}
		})
	}
}

func TestAdminHasEveryDeclaredPermission(t *testing.T) {
	perms := permissionsForRole(domain.RoleAdmin)
	if len(perms) == 0 {
		t.Fatal("admin permissions must not be empty")
	}
	for _, p := range perms {
		if !roleHasPermission(domain.RoleAdmin, p) {
			t.Fatalf("admin missing %s", p)
		}
	}
}

func TestViewerHasNoMutationPermission(t *testing.T) {
	mutationPerms := []Permission{
		PermItemMasterWrite, PermBOMWrite, PermDemandWrite, PermMPSWrite,
		PermInventoryAdjust, PermWOPlan, PermWOExecute, PermPOPlan, PermPOReceive, PermSupplierScheduleManage, PermSupplierReliabilityRun, PermInventoryPolicyManage, PermInventoryPolicyRun, PermMaintenanceManage, PermProductionPerformanceRun, PermCapacityFeedbackManage, PermDispatchManage, PermDynamicReschedule, PermSalesOrderManage, PermSalesOrderShip, PermSalesOrderPromise, PermBackorderRun, PermProductAllocation, PermPeggingRun, PermExceptionManage,
		PermMRPRun, PermCapacityMaster, PermRoutingMaster, PermCRPRun, PermForecastRun,
		PermCycleCountPlan, PermCycleCountRecord, PermCalendarWrite, PermQualityRecord,
		PermSupplierQualityManage, PermNCRCreate, PermNCRDisposition, PermShopFloorExecute, PermItemGroupWrite, PermSOPWrite, PermRCCPWrite,
		PermECODraft, PermECOApproveApply,
	}
	for _, p := range mutationPerms {
		if roleHasPermission(domain.RoleViewer, p) {
			t.Fatalf("viewer unexpectedly has mutation permission %s", p)
		}
	}
}
