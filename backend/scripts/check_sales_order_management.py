#!/usr/bin/env python3
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[1]
svc = (root/'internal/service/sales_orders.go').read_text()
router = (root/'internal/api/router.go').read_text()
rbac = (root/'internal/api/rbac.go').read_text()
forecast = (root/'internal/service/forecasting.go').read_text()
atp = (root/'internal/service/atp_quality.go').read_text()
mig = (root/'migrations/0031_sales_order_management.sql').read_text()
ui = (root.parent/'frontend/src/views/SalesOrders.vue').read_text()
api = (root.parent/'frontend/src/api/index.ts').read_text()
manager = (root/'internal/migration/manager.go').read_text()

checks = {
    '0031 migration exists': (root/'migrations/0031_sales_order_management.sql').exists(),
    'formal customer and sales-order tables': all(x in mig for x in ['CREATE TABLE IF NOT EXISTS customers','CREATE TABLE IF NOT EXISTS sales_orders','CREATE TABLE IF NOT EXISTS sales_order_lines']),
    'allocation and shipment evidence are immutable': 'sales_order_allocation_events' in mig and 'sales_order_shipments' in mig and 'reject_sales_order_evidence_mutation' in mig,
    'legacy ORDER demand is reconstructed': "FROM demand_forecasts WHERE source='ORDER'" in mig and 'LEGACY_MIGRATION' in mig,
    'legacy overdue demand satisfies order-date constraint': 'LEAST(r.created_at::date,r.due_date)' in mig,
    'legacy demand table becomes read-only': 'forbid_legacy_demand_forecast_mutation' in mig,
    'shipment uses unified inventory ledger': 's.ledger.PostTx' in svc and 'TxnType: "ISSUE"' in svc,
    'shipment releases reservation atomically': "'UNRESERVE'" in svc and "'SHIP_RELEASE'" in svc,
    'allocation serializes on item': 'SELECT id FROM items WHERE id=$1 FOR UPDATE' in svc,
    'idempotency ids are enforced': all(x in svc for x in ['allocationId was already used','releaseId was already used','shipmentId was already used']),
    'forecast consumption reads sales orders': 'FROM sales_order_lines l JOIN sales_orders so' in forecast and 'repos.Demand.List' not in forecast,
    'ATP reads sales orders': 'FROM sales_order_lines l JOIN sales_orders so' in atp and 'repos.Demand.List' not in atp,
    'planner/admin management permission routed': 'PermSalesOrderManage' in router and 'PermSalesOrderManage' in rbac,
    'operator shipment permission routed': 'PermSalesOrderShip' in router and 'PermSalesOrderShip' in rbac,
    'sales-order UI exists': 'Sales Order / Customer Order Management' in ui,
    'frontend API exists': 'SalesOrdersApi' in api,
    'migration manager fingerprints 0031': '{31,' in manager and "public.sales_orders" in manager,
    'DB reconciles line/event quantities': 'assert_sales_order_line_reconciled' in mig,
    'DB binds shipment to inventory ISSUE': 'assert_sales_order_shipment_ledger' in mig,
    'DB binds reservation events to inventory ledger': 'assert_sales_order_allocation_ledger' in mig,
    'DB blocks linked inventory evidence mutation': 'guard_linked_sales_order_inventory_txn' in mig,
    'allocation inventory transaction cannot be reused': 'inventory_txn_id    uuid NOT NULL UNIQUE REFERENCES inventory_txns' in mig,
    'shipment order and line must match': 'order/line mismatch' in mig and 'ln.sales_order_id <> sh.sales_order_id' in mig,
    'DB binds status to immutable status history': 'assert_sales_order_status_history' in mig and 'sales_order_status_audit_history_trg' in mig,
    'DB restricts sellable item types': "sales order item must be FG or SA" in mig,
    'legacy demand frontend is read-only': "create: (d: Demand)" not in api,
}
failed = [name for name, ok in checks.items() if not ok]
for name, ok in checks.items():
    print(('PASS' if ok else 'FAIL') + ': ' + name)
if failed:
    sys.exit(1)
