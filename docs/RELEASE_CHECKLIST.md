# v0.42.0 Release Candidate / GA Checklist

## Source freeze

- [ ] Working tree clean at the candidate commit.
- [ ] `SHA256SUMS.txt` (generated at release time by `scripts/generate_source_checksums.py --require-clean`) verifies every distributed source file.
- [ ] Target is `v0.42.0`; candidate matches `v0.42.0-rc.N`.
- [ ] Migration files are exactly `0001` through `0042`; `0042` is Controlled Recovery Plan Execution.
- [ ] Migration 0041 checksum is unchanged from the v0.41.0 baseline contract.

## Runtime / build

- [ ] Go 1.26.10.
- [ ] Node.js 24.20.0 LTS.
- [ ] npm 11.19.0.
- [ ] PostgreSQL 16.15.
- [ ] `go mod verify`, `go vet`, `go test -race` PASS.
- [ ] Frontend `npm ci`, typecheck, build PASS.
- [ ] `/version` reports the candidate commit/build time and `migrationLatest=42`.

## Production topology / authentication

- [ ] DB host port is not published.
- [ ] Backend host port is not published.
- [ ] Nginx frontend is the only public application service.
- [ ] No demo credentials/default JWT secret.
- [ ] Browser JWT is not in Web Storage.
- [ ] Cookie/CSRF/CORS gates PASS.
- [ ] Login throttling/auth audit PASS.
- [ ] User Administration and last-active-admin protection PASS.

## Planning / MES regression

- [ ] All static business-integrity guards PASS.
- [ ] 0039 guard PASS.
- [ ] 0040 guard PASS.
- [ ] 0041 guard PASS.
- [ ] Recovery Simulation/Publish remains side-effect free.
- [ ] Forced recovery failure persists FAILED evidence and retry semantics.
- [ ] All 26 Playwright tests PASS against production Nginx topology.

## Observability / DR

- [ ] Backend-private `/metrics` scrape PASS.
- [ ] Public `/metrics` returns 404.
- [ ] Logical backup checksum/manifest generated.
- [ ] Live restore drill PASS.
- [ ] Organization has explicitly approved production RPO/RTO, retention, encryption, off-site destination, and restore-drill cadence before GA.

## Security / supply chain

- [ ] Source checksum gate PASS.
- [ ] `govulncheck` reachable-finding gate PASS.
- [ ] npm audit policy gates PASS.
- [ ] Trivy Backend image PASS.
- [ ] Trivy Frontend image PASS.
- [ ] Source + Backend + Frontend SPDX SBOMs generated.
- [ ] Security release bundle verifier PASS.
- [ ] Release Candidate bundle verifier PASS.

## Promotion

- [ ] `.github/workflows/release-candidate.yml` is green for the exact candidate commit.
- [ ] Verified RC bundle retained with its SHA-256 manifest.
- [ ] No source/config/documentation change after final RC verification.
- [ ] Tag/promote the exact verified commit as `v0.42.0`.
