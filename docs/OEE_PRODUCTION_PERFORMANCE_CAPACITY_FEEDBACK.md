# 0038 OEE + Production Performance + Actual Capacity Feedback

## Purpose
0038 closes the planning/execution loop. It derives empirical Work Center performance from existing Shop Floor and Maintenance evidence and lets Planner/Admin explicitly activate a versioned capacity profile that CRP, Detailed Scheduling and CTP reuse.

## Evidence and formulas
- Shop Floor `operation_logs` remain the source for START/STOP/COMPLETE and now operationally expose SCRAP reporting. Migration 0038 makes the log append-only at DB level.
- Preventive Maintenance and Planned Downtime are reported as planned downtime and are excluded from the Availability loss denominator.
- Breakdown and Unplanned Downtime are Availability losses. BREAKDOWN events also produce MTBF/MTTR.
- Availability = active minutes / (active minutes + pause minutes + unplanned downtime minutes).
- Performance = ideal run minutes / actual run minutes excluding planned setup; raw performance is retained up to 150%, while OEE caps the factor at 100%.
- Quality = good quantity / (good quantity + scrap quantity).
- OEE = Availability × min(Performance, 1.0) × Quality.
- Setup loss is the standard setup estimate for started operations. Speed loss is max(actual run excluding setup - ideal run, 0).

## Capacity feedback
A completed performance run may create DRAFT feedback when a Work Center reaches the configured minimum completed-operation sample count. Recommended efficiency is bounded to 0.50–1.20 and utilization to 0.50–1.00. Nothing changes planning capacity until a Planner/Admin activates the version.

Activation never rewrites `work_centers.efficiency` or `work_centers.utilization`. The active version is substituted in-memory when CRP, finite CRP, Detailed Scheduling or CTP loads Work Centers. Known future 0037 maintenance is then subtracted separately, preventing double counting between historical reliability and known future downtime.

## Auditability
- `production_performance_runs` / `production_performance_results` are immutable calculation evidence.
- `capacity_feedback_versions` use DRAFT → ACTIVE → ARCHIVED with one ACTIVE version per Work Center.
- `detailed_schedule_capacity_feedback_snapshots` freezes the exact version used by a persisted detailed schedule.
- Full Pegging adds `CAPACITY_FEEDBACK` nodes and `CALIBRATED_BY` edges. When an impacted schedule uses feedback sourced from OEE below 85%, `OEE_CAPACITY_RISK` is emitted as an auditable root cause.

## Permissions
Planner/Admin may run performance calculations and manage feedback. Shop Floor SCRAP uses the existing Shop Floor execution permission and authenticated actor identity.
