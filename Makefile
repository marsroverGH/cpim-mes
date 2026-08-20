.PHONY: lock check-locks backend-test frontend-build e2e ci-local

lock:
	./scripts/generate-lockfiles.sh

check-locks:
	python3 scripts/check_dependency_locks.py

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
	python3 scripts/check_ci_dependency_policy.py
	cd frontend && npm ci && npm run build
