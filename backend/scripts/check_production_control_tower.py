#!/usr/bin/env python3
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[2]

def read(path):
    p = root / path
    if not p.exists():
        return ""
    return p.read_text(errors="replace")

def allin(text, *needles):
    return all(x in text for x in needles)

migration = read(
    "backend/migrations/"
    "0040_production_control_tower_constraint_exception_prioritization.sql"
)
models = read("backend/internal/domain/models.go")
manager = read("backend/internal/migration/manager.go")
manager_test = read("backend/internal/migration/manager_test.go")
manager_guard = read("backend/scripts/check_migration_manager.py")

scoring = read("backend/internal/service/control_tower_scoring.go")
scoring_test = read("backend/internal/service/control_tower_scoring_test.go")
recs = read("backend/internal/service/control_tower_recommendations.go")
recs_test = read("backend/internal/service/control_tower_recommendations_test.go")
tower = read("backend/internal/service/control_tower.go")
tower_test = read("backend/internal/service/control_tower_test.go")
dashboard = read("backend/internal/service/control_tower_dashboard.go")
workflow = read("backend/internal/service/control_tower_workflow.go")
workflow_test = read("backend/internal/service/control_tower_workflow_test.go")
e2e = read("e2e/tests/production-control-tower.spec.ts")
services = read("backend/internal/service/service.go")

api = read("backend/internal/api/control_tower.go")
router = read("backend/internal/api/router.go")
rbac = read("backend/internal/api/rbac.go")
rbac_test = read("backend/internal/api/rbac_test.go")
openapi = read("backend/internal/api/openapi.json")

frontend_api = read("frontend/src/api/index.ts")
frontend_view = read("frontend/src/views/ProductionControlTower.vue")
frontend_router = read("frontend/src/router/index.ts")
frontend_app = read("frontend/src/App.vue")
pegging_view = read("frontend/src/views/PeggingExceptions.vue")

readme = read("README.md")
ops_doc = read("docs/0040-production-control-tower.md")
ci = read(".github/workflows/ci.yml")

checks = {
    # Migration / persistence
    "0040 migration exists":
        bool(migration),

    "stable Control Tower case schema exists":
        allin(
            migration,
            "CREATE TABLE control_tower_cases",
            "case_key",
            "first_exception_id",
            "first_detected_at",
        ),

    "immutable scored snapshot schema exists":
        allin(
            migration,
            "CREATE TABLE control_tower_case_snapshots",
            "planning_exception_id",
            "pegging_run_id",
            "priority_score",
            "priority_band",
            "result_hash",
        ),

    "snapshot captures business impact":
        allin(
            migration,
            "order_value",
            "open_order_value",
            "revenue_at_risk",
            "order_priority",
            "service_class_code",
        ),

    "snapshot preserves explainable score dimensions":
        allin(
            migration,
            "severity_score",
            "lateness_score",
            "revenue_score",
            "customer_score",
            "material_score",
            "capacity_score",
            "supplier_score",
            "execution_score",
            "aging_score",
        ),

    "recommendation evidence schema exists":
        allin(
            migration,
            "CREATE TABLE control_tower_recommendations",
            "rank_no",
            "action_type",
            "estimated_effect",
            "requires_approval",
        ),

    "case workflow evidence schema exists":
        allin(
            migration,
            "CREATE TABLE control_tower_case_actions",
            "ACKNOWLEDGE",
            "ASSIGN",
            "START",
            "RESOLVE",
            "REOPEN",
            "CLOSE",
        ),

    "Control Tower evidence immutable":
        allin(
            migration,
            "reject_control_tower_evidence_mutation",
            "control_tower_cases_immutable_trg",
            "control_tower_snapshots_immutable_trg",
            "control_tower_recommendations_immutable_trg",
            "control_tower_actions_immutable_trg",
        ),

    "case identity bound to first planning exception":
        allin(
            migration,
            "guard_control_tower_case_insert",
            "first_exception_id",
            "control tower case identity does not match first planning exception",
        ),

    "snapshot bound to current planning exception":
        allin(
            migration,
            "guard_control_tower_snapshot_insert",
            "v_current_planning_exceptions",
            "operationally current planning exception",
        ),

    "case lifecycle state function exists":
        allin(
            migration,
            "control_tower_case_current_status",
            "control_tower_case_actions",
        ),

    "case lifecycle transition guard exists":
        allin(
            migration,
            "guard_control_tower_case_action",
            "ACKNOWLEDGE",
            "ASSIGN",
            "START",
            "RESOLVE",
            "REOPEN",
            "CLOSE",
        ),

    "case action serializes concurrent mutations":
        "pg_advisory_xact_lock" in migration,

    "case actions require planner admin":
        allin(
            migration,
            "control tower actions require planner/admin actor",
            "admin",
            "planner",
        ),

    "current Control Tower projection exists":
        allin(
            migration,
            "CREATE OR REPLACE VIEW v_current_control_tower_cases",
            "latest_snapshot",
            "latest_action",
            "latest_assignment",
        ),

    # Migration manager
    "migration manager fingerprints 0040":
        allin(
            manager,
            "{40,",
            "control_tower_cases",
            "control_tower_case_snapshots",
            "control_tower_recommendations",
            "v_current_control_tower_cases",
        ),

    "migration manager tests expect 40":
        allin(
            manager_test,
            "len(migs) != 40",
            "expected 40 migrations",
        ),

    "migration guard expects 40 ordered migrations":
        allin(
            manager_guard,
            "40 ordered SQL migrations exist",
            "range(1,41)",
        ),

    # Domain
    "domain Control Tower case exists":
        "type ControlTowerCase struct" in models,

    "domain immutable snapshot exists":
        "type ControlTowerCaseSnapshot struct" in models,

    "domain recommendation exists":
        "type ControlTowerRecommendation struct" in models,

    "domain workflow action exists":
        "type ControlTowerCaseAction struct" in models,

    "domain current case read model exists":
        "type ControlTowerCurrentCase struct" in models,

    "domain dashboard summary exists":
        "type ControlTowerDashboardSummary struct" in models,

    "domain dashboard exists":
        "type ControlTowerDashboard struct" in models,

    # Scoring
    "priority scoring engine exists":
        "func ScoreControlTowerPriority" in scoring,

    "score uses strongest constraint":
        allin(
            scoring,
            "constraintScore := math.Max",
            "MaterialScore",
            "CapacityScore",
            "SupplierScore",
            "ExecutionScore",
        ),

    "score weights cover business impact":
        allin(
            scoring,
            "SeverityScore*0.20",
            "LatenessScore*0.15",
            "RevenueScore*0.20",
            "CustomerScore*0.10",
            "constraintScore*0.25",
            "AgingScore*0.10",
        ),

    "hard operational constraints force P1":
        allin(
            scoring,
            "forcedControlTowerP1",
            "MATERIAL_SHORTAGE",
            "SUPPLIER_BLOCKED",
            "CAPACITY_UNSCHEDULED",
            "DISPATCH_BLOCKED",
            "FROZEN_HORIZON_CONFLICT",
            "EXECUTION_COMMITMENT_CONFLICT",
        ),

    "P1/P2/P3/P4 bands exist":
        allin(
            scoring,
            'return "P1"',
            'return "P2"',
            'return "P3"',
            'return "P4"',
        ),

    # Recommendation engine
    "recommendation engine exists":
        "func RecommendControlTowerActions" in recs,

    "supplier intervention recommends expedite":
        "EXPEDITE_PO" in recs,

    "capacity intervention recommends reschedule":
        "RESCHEDULE_WO" in recs,

    "alternate work center recommendation exists":
        "ALTERNATE_WORK_CENTER" in recs,

    "quality hold intervention exists":
        "REVIEW_QUALITY_HOLD" in recs,

    "promise recalculation recommendation exists":
        "RECALCULATE_PROMISE" in recs,

    "customer communication recommendation exists":
        "CONTACT_CUSTOMER" in recs,

    "frozen commitment recommendation exists":
        "REVIEW_FROZEN_CONFLICT" in recs,

    "unknown exception has manual fallback":
        "MANUAL_REVIEW" in recs,

    # Aggregation / snapshot
    "Control Tower refresh service exists":
        "func (s *ControlTowerService) Refresh" in tower,

    "refresh consumes current planning exceptions":
        "FROM v_current_planning_exceptions" in tower,

    "open order value derives from physical Sales Order quantities":
        allin(
            tower,
            "quantity -",
            "shipped_qty",
            "cancelled_qty",
            "open_order_value",
            "revenue_at_risk",
        )
        and ".open_qty" not in tower,


    "refresh uses repeatable-read snapshot":
        allin(
            tower,
            "sql.LevelRepeatableRead",
            "BeginTxx",
        ),

    "stable case key exists":
        "func controlTowerCaseKey" in tower,

    "root cause extraction exists":
        "func controlTowerRootRef" in tower,

    "canonical snapshot hash exists":
        "func canonicalControlTowerSnapshotHash" in tower,

    "snapshot insert is idempotent":
        allin(
            tower,
            "ON CONFLICT(case_id,result_hash) DO NOTHING",
            "RowsAffected",
        ),

    "refresh produces recommendations only for new snapshots":
        allin(
            tower,
            "if !created",
            "RecommendControlTowerActions",
            "insertControlTowerRecommendations",
        ),

    # Dashboard / workflow
    "dashboard query service exists":
        "func (s *ControlTowerService) Dashboard" in dashboard,

    "dashboard supports status and priority filters":
        allin(
            dashboard,
            "ControlTowerDashboardFilter",
            "PriorityBand",
            "current_status",
            "priority_band",
        ),

    "dashboard orders P1 before lower bands":
        allin(
            dashboard,
            "WHEN 'P1' THEN 1",
            "priority_score DESC",
            "revenue_at_risk DESC",
        ),

    "dashboard summarizes revenue at risk":
        allin(
            dashboard,
            "RevenueAtRisk",
            "UnassignedCases",
            "P1Cases",
            "P2Cases",
        ),

    "case detail API service exists":
        "func (s *ControlTowerService) GetCase" in dashboard,

    "latest recommendation service exists":
        "func (s *ControlTowerService) Recommendations" in dashboard,

    "workflow action service exists":
        "func (s *ControlTowerService) ActOnCase" in workflow,

    "workflow validates planner admin":
        allin(
            workflow,
            "validateControlTowerActor",
            '"admin"',
            '"planner"',
        ),

    "workflow supports assignment":
        allin(
            workflow,
            "AssignedToUserID",
            "ASSIGN requires assignedToUserId",
        ),

    "case action history service exists":
        "func (s *ControlTowerService) CaseActions" in workflow,

    # Container wiring
    "service container exposes Control Tower":
        allin(
            services,
            "ControlTower",
            "*ControlTowerService",
        ),

    "service container constructs Control Tower":
        allin(
            services,
            "NewControlTowerService(db)",
            "ControlTower:",
        ),

    # API / RBAC
    "Control Tower API handlers exist":
        allin(
            api,
            "refreshControlTower",
            "controlTowerDashboard",
            "getControlTowerCase",
            "controlTowerRecommendations",
            "controlTowerCaseActions",
            "actOnControlTowerCase",
        ),

    "Control Tower actor uses authenticated claims":
        allin(
            api,
            "authenticatedControlTowerActor",
            "ctxKeyClaims",
            "claims.UserID",
            "claims.Username",
            "claims.Role",
        ),

    "Control Tower refresh RBAC exists":
        allin(
            rbac,
            "PermControlTowerRefresh",
            "planning.control_tower.refresh",
        ),

    "Control Tower manage RBAC exists":
        allin(
            rbac,
            "PermControlTowerManage",
            "planning.control_tower.manage",
        ),

    "planner receives Control Tower permissions":
        allin(
            rbac,
            "PermControlTowerRefresh:",
            "PermControlTowerManage:",
        ),

    "RBAC tests cover Control Tower":
        allin(
            rbac_test,
            "operator cannot refresh Control Tower",
            "planner can refresh Control Tower",
            "operator cannot manage Control Tower",
            "planner can manage Control Tower",
        ),

    "Control Tower routes exist":
        allin(
            router,
            'Post("/control-tower/refresh"',
            'Get("/control-tower"',
            'Get("/control-tower/cases/{id}"',
            'Get("/control-tower/cases/{id}/recommendations"',
            'Get("/control-tower/cases/{id}/actions"',
            'Post("/control-tower/cases/{id}/actions"',
        ),

    "Control Tower mutation routes protected":
        allin(
            router,
            "PermControlTowerRefresh",
            "PermControlTowerManage",
        ),

    # OpenAPI
    "OpenAPI Control Tower paths exist":
        allin(
            openapi,
            '"/control-tower"',
            '"/control-tower/refresh"',
            '"/control-tower/cases/{id}"',
            '"/control-tower/cases/{id}/recommendations"',
            '"/control-tower/cases/{id}/actions"',
        ),

    "OpenAPI Control Tower schemas exist":
        allin(
            openapi,
            '"ControlTowerDashboard"',
            '"ControlTowerCurrentCase"',
            '"ControlTowerRecommendation"',
            '"ControlTowerCaseAction"',
            '"ControlTowerRefreshResult"',
        ),

    # Frontend
    "frontend Control Tower API exists":
        allin(
            frontend_api,
            "ControlTowerApi",
            "'/control-tower/refresh'",
            "'/control-tower'",
        ),

    "frontend Control Tower dashboard types exist":
        allin(
            frontend_api,
            "ControlTowerDashboard",
            "ControlTowerCurrentCase",
            "ControlTowerRecommendation",
            "ControlTowerCaseAction",
        ),

    "frontend Production Control Tower page exists":
        allin(
            frontend_view,
            "Production Control Tower",
            "Intervention Priority",
            "Revenue at Risk",
            "Recommended Interventions",
            "Case History",
        ),

    "frontend page supports lifecycle actions":
        allin(
            frontend_view,
            "ACKNOWLEDGE",
            "ASSIGN",
            "START",
            "RESOLVE",
            "REOPEN",
            "CLOSE",
        ),

    "frontend route exists":
        "/production-control-tower" in frontend_router,

    "frontend navigation exists":
        allin(
            frontend_app,
            "/production-control-tower",
            "Production Control Tower",
        ),

    "Planning Exception frontend vocabulary is extensible":
        "exceptionType: string" in frontend_api,

    "Planning Exception type filter is extensible":
        "<v-combobox" in pegging_view,

    # Unit tests
    "unit tests cover forced P1":
        allin(
            scoring_test,
            "TestControlTowerMaterialShortageForcedP1",
            "TestControlTowerDispatchBlockedForcedP1",
        ),

    "unit tests cover high revenue supplier delay":
        "TestControlTowerHighRevenueSupplierDelay" in scoring_test,

    "unit tests cover recommendation mappings":
        allin(
            recs_test,
            "TestControlTowerRecommendSupplierDelay",
            "TestControlTowerRecommendCapacity",
            "TestControlTowerRecommendFrozenConflictRequiresApproval",
        ),

    "unit tests cover stable case identity":
        allin(
            tower_test,
            "TestControlTowerCaseKeyIsStable",
            "TestControlTowerCaseKeySeparatesRootCauses",
        ),

    "unit tests cover canonical snapshot hash":
        "TestCanonicalControlTowerSnapshotHashIsStable" in tower_test,

    "unit tests cover workflow authorization":
        allin(
            workflow_test,
            "TestControlTowerWorkflowRejectsOperator",
            "TestControlTowerWorkflowAllowsPlanner",
        ),

    "unit tests cover workflow assignment":
        allin(
            workflow_test,
            "TestControlTowerWorkflowAssignRequiresUser",
            "TestControlTowerWorkflowAssignKeepsUser",
        ),

    # CI is added in next step
    # Documentation
    "0040 operations document exists":
        allin(
            ops_doc,
            "0040 Production Control Tower",
            "Revenue at Risk",
            "Priority Scoring",
            "Immutable Snapshot",
            "Case Lifecycle",
        ),

    "README documents 0040 Production Control Tower":
        allin(
            readme,
            "0040 Production Control Tower",
            "Constraint & Exception Prioritization",
            "Revenue at Risk",
        ),

    # Dedicated 0040 E2E
    "E2E verifies Control Tower refresh":
        allin(
            e2e,
            "/api/control-tower/refresh",
            "exceptionsEvaluated",
            "casesTouched",
        ),

    "E2E verifies business impact prioritization":
        allin(
            e2e,
            "priorityBand",
            "priorityScore",
            "revenueAtRisk",
            "6000000",
        ),

    "E2E verifies recommendations":
        allin(
            e2e,
            "RECALCULATE_PROMISE",
            "CONTACT_CUSTOMER",
            "/recommendations",
        ),

    "E2E verifies canonical refresh idempotency":
        allin(
            e2e,
            "firstSnapshotId",
            "caseAfterRefresh.snapshotId",
            "toBe(firstSnapshotId)",
        ),

    "E2E verifies Control Tower lifecycle":
        allin(
            e2e,
            "ACKNOWLEDGE",
            "ASSIGN",
            "START",
            "RESOLVE",
            "CLOSE",
        ),

    "E2E verifies unauthenticated mutation blocked":
        allin(
            e2e,
            "anonymousMutation",
            "toBe(401)",
        ),

    "E2E verifies Production Control Tower UI":
        allin(
            e2e,
            "/production-control-tower",
            "Production Control Tower",
            "Recommended Interventions",
            "Case History",
            "control-tower-status-filter",
            "control-tower-filter-button",
        )
        and allin(
            frontend_view,
            'data-testid="control-tower-status-filter"',
            'data-testid="control-tower-filter-button"',
        ),

    "CI runs 0040 guard":
        "check_production_control_tower.py" in ci,
}

failed = []

for name, ok in checks.items():
    if ok:
        print(f"PASS: {name}")
    else:
        print(f"FAIL: {name}")
        failed.append(name)

print(
    f"Production Control Tower static guard: "
    f"{len(checks)-len(failed)}/{len(checks)} checks PASS"
)

if failed:
    sys.exit(1)
