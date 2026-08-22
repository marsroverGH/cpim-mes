#!/usr/bin/env python3

from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[2]

def read(path: str) -> str:
    p = ROOT / path
    if not p.exists():
        raise SystemExit(f"FAIL: missing file: {path}")
    return p.read_text()

def allin(text: str, *values: str) -> bool:
    return all(v in text for v in values)

migration = read(
    "backend/migrations/"
    "0041_scenario_based_recovery_planning_what_if_simulation.sql"
)

models = read("backend/internal/domain/models.go")
simulation = read(
    "backend/internal/service/recovery_simulation.go"
)
hashing = read(
    "backend/internal/service/recovery_planning_hash.go"
)
planning = read(
    "backend/internal/service/recovery_planning.go"
)
workflow = read(
    "backend/internal/service/recovery_scenario_workflow.go"
)

service_container = read(
    "backend/internal/service/service.go"
)

api = read(
    "backend/internal/api/recovery_planning.go"
)
rbac = read(
    "backend/internal/api/rbac.go"
)
rbac_test = read(
    "backend/internal/api/rbac_test.go"
)
router = read(
    "backend/internal/api/router.go"
)
openapi = read(
    "backend/internal/api/openapi.json"
)

frontend_api = read(
    "frontend/src/api/index.ts"
)
frontend_page = read(
    "frontend/src/views/RecoveryPlanning.vue"
)
frontend_router = read(
    "frontend/src/router/index.ts"
)
frontend_app = read(
    "frontend/src/App.vue"
)

recovery_doc = read(
    "docs/0041-scenario-based-recovery-planning.md"
)
readme = read("README.md")
ci = read(".github/workflows/ci.yml")

e2e_path = ROOT / "e2e/tests/recovery-planning.spec.ts"
e2e = (
    e2e_path.read_text()
    if e2e_path.exists()
    else ""
)

migration_files = list(
    (ROOT / "backend/migrations").glob(
        "[0-9][0-9][0-9][0-9]_*.sql"
    )
)

protected_tables = [
    "purchase_orders",
    "purchase_order_lines",
    "work_orders",
    "work_order_operations",
    "work_centers",
    "sales_orders",
    "sales_order_lines",
    "inventory_lots",
    "inventory_transactions",
]

mutation_text = planning + "\n" + workflow

forbidden_mutation = False

for table in protected_tables:
    pattern = (
        rf"\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)"
        rf"\s+{re.escape(table)}\b"
    )
    if re.search(pattern, mutation_text, re.IGNORECASE):
        forbidden_mutation = True

checks = {
    "41 ordered SQL migrations exist":
        len(migration_files) == 41,

    "0041 migration exists":
        "0041: Scenario-Based Recovery Planning" in migration
        or "Scenario-Based Recovery Planning" in migration,

    "scenario schema exists":
        allin(
            migration,
            "CREATE TABLE recovery_scenarios",
            "CREATE TABLE recovery_scenario_actions",
        ),

    "simulation run schema exists":
        allin(
            migration,
            "CREATE TABLE recovery_scenario_runs",
            "baseline_hash",
            "request_hash",
            "result_hash",
        ),

    "immutable case evidence exists":
        "CREATE TABLE recovery_scenario_case_results"
        in migration,

    "immutable action evidence exists":
        "CREATE TABLE recovery_scenario_action_results"
        in migration,

    "summary evidence exists":
        "CREATE TABLE recovery_scenario_summaries"
        in migration,

    "publication evidence exists":
        "CREATE TABLE recovery_scenario_publications"
        in migration,

    "comparison view exists":
        "CREATE VIEW v_recovery_scenario_comparison"
        in migration,

    "latest run view exists":
        "v_latest_recovery_scenario_runs"
        in migration,

    "successful request partial unique index exists":
        allin(
            migration,
            "recovery_scenario_runs_success_request_uidx",
            "WHERE status = 'SUCCEEDED'",
        ),

    "old all-status request uniqueness removed":
        "recovery_scenario_runs_request_unique"
        not in migration,

    "failed run vocabulary exists":
        allin(
            migration,
            "'RUNNING'",
            "'SUCCEEDED'",
            "'FAILED'",
        ),

    "published scenarios immutable":
        "PUBLISHED" in migration
        and "guard_recovery_scenario" in migration,

    "simulation evidence immutable":
        "reject_recovery_simulation_evidence_mutation"
        in migration,

    "publication DB guard exists":
        "guard_recovery_scenario_publication"
        in migration,

    "domain scenario exists":
        "type RecoveryScenario struct" in models,

    "domain action exists":
        "type RecoveryScenarioAction struct" in models,

    "domain run exists":
        "type RecoveryScenarioRun struct" in models,

    "domain summary exists":
        "type RecoveryScenarioSummary struct" in models,

    "domain publication exists":
        "type RecoveryScenarioPublication struct" in models,

    "domain comparison exists":
        "type RecoveryScenarioComparison struct" in models,

    "pure simulation engine exists":
        "func SimulateRecoveryScenario" in simulation,

    "pure engine has no database/sql":
        '"database/sql"' not in simulation,

    "pure engine has no sqlx":
        "sqlx" not in simulation,

    "expedite PO supported":
        "EXPEDITE_PO" in simulation,

    "alternate WC supported":
        "ALTERNATE_WORK_CENTER" in simulation,

    "overtime supported":
        "ADD_OVERTIME_CAPACITY" in simulation,

    "reschedule WO supported":
        "RESCHEDULE_WO" in simulation,

    "release WO supported":
        "RELEASE_WO" in simulation,

    "frozen conflict protected":
        "FROZEN_HORIZON_CONFLICT" in simulation,

    "execution commitment conflict protected":
        "EXECUTION_COMMITMENT_CONFLICT" in simulation,

    "canonical engine version exists":
        "recoverySimulationEngineVersion" in hashing,

    "canonical baseline hash exists":
        "func recoveryBaselineHash" in hashing,

    "canonical request hash exists":
        "func recoveryRequestHash" in hashing,

    "canonical case hash exists":
        "func recoveryCaseResultHash" in hashing,

    "canonical action hash exists":
        "func recoveryActionResultHash" in hashing,

    "canonical summary hash exists":
        "func recoverySummaryHash" in hashing,

    "canonical run hash exists":
        "func recoveryRunResultHash" in hashing,

    "application simulation service exists":
        "func (s *RecoveryPlanningService) Simulate"
        in planning,

    "simulation uses repeatable read":
        "sql.LevelRepeatableRead" in planning,

    "simulation reads Control Tower projection":
        "FROM v_current_control_tower_cases"
        in planning,

    "simulation supports canonical reuse":
        allin(
            planning,
            "request_hash=$2",
            "status='SUCCEEDED'",
            "Reused",
        ),

    "simulation writes RUNNING evidence":
        "status='RUNNING'" in planning
        or 'Status: "RUNNING"' in planning,

    "simulation terminalizes SUCCEEDED":
        "status='SUCCEEDED'" in planning,

    "simulation advances scenario to SIMULATED":
        "status='SIMULATED'" in planning,

    "What-if has no operational table mutations":
        not forbidden_mutation,

    "scenario create service exists":
        "CreateScenario(" in workflow,

    "scenario update service exists":
        "UpdateScenario(" in workflow,

    "scenario archive service exists":
        "ArchiveScenario(" in workflow,

    "action add service exists":
        "AddScenarioAction(" in workflow,

    "action update service exists":
        "UpdateScenarioAction(" in workflow,

    "action delete service exists":
        "DeleteScenarioAction(" in workflow,

    "comparison service exists":
        "CompareScenarios(" in workflow,

    "publication hash exists":
        "recoveryPublicationHash(" in workflow,

    "publish service exists":
        "PublishScenario(" in workflow,

    "publish requires latest successful run":
        allin(
            workflow,
            "latestRunID",
            "status='SUCCEEDED'",
        ),

    "publish changes only recovery scenario lifecycle":
        "status='PUBLISHED'" in workflow,

    "service container exposes Recovery Planning":
        "RecoveryPlanning" in service_container,

    "service container constructs Recovery Planning":
        "NewRecoveryPlanningService(db)"
        in service_container,

    "API handlers exist":
        allin(
            api,
            "createRecoveryScenario",
            "addRecoveryScenarioAction",
            "simulateRecoveryScenario",
            "compareRecoveryScenarios",
            "publishRecoveryScenario",
        ),

    "API actor uses authenticated claims":
        allin(
            api,
            "authenticatedRecoveryPlanningActor",
            "string(claims.Role)",
        ),

    "RBAC manage permission exists":
        "planning.recovery_scenario.manage" in rbac,

    "RBAC simulate permission exists":
        "planning.recovery_scenario.simulate" in rbac,

    "RBAC publish permission exists":
        "planning.recovery_scenario.publish" in rbac,

    "planner receives Recovery Planning permissions":
        allin(
            rbac,
            "PermRecoveryScenarioManage:",
            "PermRecoverySimulationRun:",
            "PermRecoveryPublish:",
        ),

    "RBAC tests cover Recovery Planning":
        allin(
            rbac_test,
            "planner can manage recovery scenario",
            "planner can simulate recovery scenario",
            "planner can publish recovery scenario",
            "operator cannot simulate recovery scenario",
        ),

    "scenario routes exist":
        allin(
            router,
            '"/recovery-scenarios"',
            '"/recovery-scenarios/{id}"',
        ),

    "action routes exist":
        allin(
            router,
            '"/recovery-scenarios/{id}/actions"',
            '"/recovery-scenarios/{id}/actions/{actionId}"',
        ),

    "simulate route protected":
        allin(
            router,
            "PermRecoverySimulationRun",
            '"/recovery-scenarios/{id}/simulate"',
        ),

    "publish route protected":
        allin(
            router,
            "PermRecoveryPublish",
            '"/recovery-scenarios/{id}/publish"',
        ),

    "comparison route exists":
        '"/recovery-scenario-comparison"'
        in router,

    "OpenAPI scenario paths exist":
        allin(
            openapi,
            '"/recovery-scenarios"',
            '"/recovery-scenarios/{id}"',
        ),

    "OpenAPI simulation path exists":
        '"/recovery-scenarios/{id}/simulate"'
        in openapi,

    "OpenAPI comparison path exists":
        '"/recovery-scenario-comparison"'
        in openapi,

    "OpenAPI publish path exists":
        '"/recovery-scenarios/{id}/publish"'
        in openapi,

    "frontend Recovery Planning API exists":
        "export const RecoveryPlanningApi"
        in frontend_api,

    "frontend scenario types exist":
        "export interface RecoveryScenario"
        in frontend_api,

    "frontend simulation types exist":
        "RecoverySimulationExecution"
        in frontend_api,

    "frontend comparison types exist":
        "RecoveryScenarioComparison"
        in frontend_api,

    "frontend Recovery Planning page exists":
        "Scenario-Based Recovery Planning"
        in frontend_page,

    "frontend page exposes Recovery Score":
        "Recovery Score" in frontend_page,

    "frontend page exposes P1 reduction":
        "P1 Reduction" in frontend_page,

    "frontend page exposes recovered revenue":
        "Revenue Recovered" in frontend_page
        or "Recovered Revenue" in frontend_page,

    "frontend page exposes Net Value":
        "Net Value" in frontend_page,

    "frontend page supports simulation":
        "RecoveryPlanningApi.simulate"
        in frontend_page,

    "frontend page supports publication":
        "RecoveryPlanningApi.publish"
        in frontend_page,

    "frontend route exists":
        "/recovery-planning" in frontend_router,

    "frontend navigation exists":
        allin(
            frontend_app,
            "/recovery-planning",
            "Recovery Planning",
        ),

    "0041 operations document exists":
        allin(
            recovery_doc,
            "0041 Scenario-Based Recovery Planning",
            "Operational Safety Boundary",
            "Recovery Score",
            "Immutable Evidence",
        ),

    "README documents 0041":
        allin(
            readme,
            "0041 Scenario-Based Recovery Planning",
            "What-if",
            "Recovery Score",
            "Net Value",
        ),

    "0041 E2E exists":
        bool(e2e),

    "E2E verifies canonical reuse":
        "reused" in e2e,

    "E2E verifies comparison":
        "recovery-scenario-comparison"
        in e2e,

    "E2E verifies publication":
        "/publish" in e2e,

    "E2E verifies operational side-effect boundary":
        allin(
            e2e,
            "/api/work-orders",
            "/api/purchase-orders",
            "/api/work-centers",
            "/api/sales-orders",
            "operationalSnapshot",
        ),

    "E2E verifies Recovery Planning UI":
        allin(
            e2e,
            "/recovery-planning",
            "Recovery Planning",
        ),

    "CI runs 0041 guard":
        "python3 backend/scripts/check_recovery_planning.py"
        in ci,
}

failed = []

for name, ok in checks.items():
    if ok:
        print(f"PASS: {name}")
    else:
        print(f"FAIL: {name}")
        failed.append(name)

if failed:
    print()
    print(
        f"Scenario-Based Recovery Planning static guard: "
        f"{len(checks) - len(failed)}/{len(checks)} checks PASS"
    )
    sys.exit(1)

print()
print(
    f"Scenario-Based Recovery Planning static guard: "
    f"{len(checks)}/{len(checks)} checks PASS"
)
