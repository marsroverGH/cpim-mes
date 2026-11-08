.PHONY: lock check-runtime check-locks check-production-config check-demo-credentials check-production-frontend check-cookie-auth check-login-security check-user-admin check-recovery-failure-audit check-controlled-recovery-execution check-version-metadata check-observability check-backup-dr check-security-supply-chain check-release-candidate verify-source-checksums security-gate final-clean-room backup restore-drill backend-test frontend-build e2e ci-local

lock:
	./scripts/generate-lockfiles.sh

check-runtime:
	python3 scripts/check_runtime_versions.py

check-locks: check-runtime
	python3 scripts/check_dependency_locks.py

check-production-config:
	python3 backend/scripts/check_production_config.py

check-demo-credentials:
	python3 backend/scripts/check_demo_credentials_removed.py

check-production-frontend:
	python3 backend/scripts/check_production_frontend_container.py

check-cookie-auth:
	python3 backend/scripts/check_cookie_auth_csrf.py

check-login-security:
	python3 backend/scripts/check_login_rate_limit_auth_audit.py

check-user-admin:
	python3 backend/scripts/check_user_administration.py

check-recovery-failure-audit:
	python3 backend/scripts/check_recovery_failed_persistence.py

check-controlled-recovery-execution:
	python3 backend/scripts/check_controlled_recovery_execution.py

check-version-metadata:
	python3 backend/scripts/check_version_build_metadata.py

check-observability:
	python3 backend/scripts/check_observability.py

check-backup-dr:
	python3 backend/scripts/check_backup_restore_dr.py
	python3 scripts/test_postgres_dr_unit.py

check-security-supply-chain:
	python3 backend/scripts/check_security_supply_chain.py
	python3 backend/scripts/check_final_release_candidate.py
	python3 scripts/test_security_supply_chain_unit.py
	python3 scripts/test_release_candidate_unit.py

check-release-candidate:
	python3 backend/scripts/check_final_release_candidate.py
	python3 scripts/test_release_candidate_unit.py

final-clean-room: check-release-candidate
	./scripts/final_clean_room_release_gate.sh

verify-source-checksums:
	python3 scripts/generate_source_checksums.py
	python3 scripts/verify_source_checksums.py

security-gate: check-security-supply-chain verify-source-checksums
	./scripts/install_security_tools.sh dependency
	./scripts/run_dependency_security_gate.sh

backup:
	./scripts/backup_postgres.sh

restore-drill:
	./scripts/restore_drill.sh

backend-test: check-locks
	cd backend && go mod verify && go test -mod=readonly -race -count=1 ./...

frontend-build: check-locks
	cd frontend && npm ci && npm run build

e2e: check-locks
	cd e2e && npm ci && npm test

ci-local: check-locks
	cd backend && go mod verify && go vet -mod=readonly ./... && go test -mod=readonly -race -count=1 ./...
	python3 backend/scripts/check_rbac_routes.py
	python3 backend/scripts/check_bom_integrity.py
	python3 backend/scripts/check_final_operation_guard.py
	python3 backend/scripts/check_shopfloor_state_machine.py
	python3 backend/scripts/check_partial_purchase_receipts.py
	python3 backend/scripts/check_eco_effective_guard.py
	python3 backend/scripts/check_quality_transaction.py
	python3 backend/scripts/check_forecast_version_consumption.py
	python3 backend/scripts/check_sop_mps_disaggregation.py
	python3 backend/scripts/check_migration_manager.py
	python3 backend/scripts/check_production_config.py
	python3 backend/scripts/check_demo_credentials_removed.py
	python3 backend/scripts/check_production_frontend_container.py
	python3 backend/scripts/check_cookie_auth_csrf.py
	python3 backend/scripts/check_login_rate_limit_auth_audit.py
	python3 backend/scripts/check_user_administration.py
	python3 backend/scripts/check_recovery_failed_persistence.py
	python3 backend/scripts/check_controlled_recovery_execution.py
	python3 backend/scripts/check_version_build_metadata.py
	python3 backend/scripts/check_observability.py
	python3 backend/scripts/check_backup_restore_dr.py
	python3 backend/scripts/check_security_supply_chain.py
	python3 backend/scripts/check_final_release_candidate.py
	python3 backend/scripts/check_abc_annual_usage.py
	python3 backend/scripts/check_advanced_order_promising.py
	python3 backend/scripts/check_backorder_processing.py
	python3 backend/scripts/check_crp_finite_capacity.py
	python3 backend/scripts/check_detailed_scheduling.py
	python3 backend/scripts/check_full_pegging_exception_management.py
	python3 backend/scripts/check_inventory_policy.py
	python3 backend/scripts/check_maintenance_capacity.py
	python3 backend/scripts/check_production_control_tower.py
	python3 backend/scripts/check_production_performance.py
	python3 backend/scripts/check_realtime_dispatch_rescheduling.py
	python3 backend/scripts/check_recovery_planning.py
	python3 backend/scripts/check_sales_order_management.py
	python3 backend/scripts/check_supplier_quality_ncr.py
	python3 backend/scripts/check_supplier_scheduling_reliability.py
	python3 scripts/test_security_supply_chain_unit.py
	python3 scripts/test_release_candidate_unit.py
	python3 scripts/check_ci_dependency_policy.py
	cd frontend && npm ci && npm run build
