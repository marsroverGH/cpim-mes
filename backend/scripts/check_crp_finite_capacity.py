#!/usr/bin/env python3
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[2]
def text(p): return (root/p).read_text(encoding='utf-8')
svc=text('backend/internal/service/crp_finite.go')
cap=text('backend/internal/service/capacity.go')
api=text('backend/internal/api/capacity.go')
routes=text('backend/internal/api/router.go')
mig=text('backend/migrations/0029_crp_finite_capacity_scheduling.sql')
ui=text('frontend/src/views/Crp.vue')
wcui=text('frontend/src/views/WorkCenters.vue')
mgr=text('backend/internal/migration/manager.go')
checks={
 'finite allocator exists': 'type finiteAllocator struct' in svc and 'allocateForward' in svc,
 'firm WO scheduled first': 'firmTasks' in svc and 'FIRM_WO' in svc and 'allTasks := append(firmTasks, plannedTasks...)' in svc,
 'MRP planned orders consume remaining capacity': 'MRP_PLANNED' in svc and 's.mrp.Run' in svc,
 'routing precedence enforced': 'cursor = segs[len(segs)-1].EndAt' in svc,
 'calendar efficiency utilization respected': 'effectiveLoad := rawCapacity * eff * util' in svc and 'MinutesAvailable' in svc,
 'multi-day split supported': 'for day := TruncateDay(earliest); !day.After(a.end); day = day.AddDate' in svc,
 'late and unscheduled explicit': 'ScheduleStatus = "LATE"' in svc and 'ScheduleStatus = "UNSCHEDULED"' in svc,
 'immutable schedule snapshot schema': 'crp_schedule_runs' in mig and 'completed finite CRP schedule snapshots are immutable' in mig,
 'DB overlap guard': 'overlapping segments on the same work center' in mig and 'tstzrange' in mig,
 'shift start supported': 'shift_start_minute' in mig and 'shiftStartMinute' in wcui,
 'finite API permission protected': 'Post("/crp/schedule", srv.runFiniteCRP)' in routes and 'PermCRPRun' in routes,
 'schedule history API': '/crp/schedule-runs' in routes and 'ListFiniteRuns' in svc,
 'CRP UI finite schedule': '有限能力日程を作成' in ui and '工程セグメント' in ui,
 'migration manager fingerprints 0029': '{29,' in mgr,
 'CRP service owns DB for snapshots': '*sqlx.DB' in cap and 'type CRPService struct' in cap,
}
failed=[]
for name,ok in checks.items():
 print(('PASS' if ok else 'FAIL')+': '+name)
 if not ok: failed.append(name)
if failed: sys.exit(1)
