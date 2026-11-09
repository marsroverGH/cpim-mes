# v0.44.0 Final Clean-room Release Gate

The v0.44.0 release candidate is validated from an empty environment. The gate preserves all v0.41.1 Production Hardening controls while adding Migration 0042 Controlled Recovery Plan Execution.

## Required database state

```text
migration files = exactly 0001 .. 0045
schema_migrations count = 45
schema_migrations min = 1
schema_migrations max = 45
schema_migrations sequence = 1,2,...,45
```

Migration 0041 remains the side-effect-free Recovery Planning boundary. Migration 0042 adds a separate controlled operational execution boundary.

## Required release sequence

```text
source checksum verification
  -> runtime + dependency lock verification
  -> Go vet + race tests
  -> all backend static guards, including 0041 and 0042
  -> dependency vulnerability gate
  -> clean production image build
  -> container scan + SBOM
  -> frontend typecheck/build
  -> E2E locked install
  -> empty PostgreSQL volume
  -> migration 0001 -> 0045
  -> production topology validation
  -> /version migrationLatest=45
  -> private observability boundary
  -> forced 0041 FAILED-run persistence test
  -> Playwright 26/26
  -> live pg_dump/pg_restore drill
  -> security release bundle verification
  -> v0.44.0 release-candidate bundle verification
```

## 0042-specific gate

The suite must prove:

- 0041 Simulation/Publish does not mutate the operational WO used by the test;
- a Planner can prepare and preflight a Published Scenario;
- the same Planner cannot approve;
- an Admin can approve and explicitly execute;
- operational state changes only after Execute;
- the action result is persisted as `SUCCEEDED` with a result hash;
- repeating Execute does not apply the action twice;
- wildcard simulation targets are blocked by execution preflight.

## Promotion rule

A code, migration, configuration, dependency, workflow or documentation change after the candidate is frozen requires a new RC and a complete clean-room rerun. GA promotion is allowed only when the exact candidate commit passes the authoritative workflow.
