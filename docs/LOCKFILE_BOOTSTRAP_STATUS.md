# Lockfile bootstrap status

The CI/lock infrastructure is implemented and the production Dockerfiles require committed locks. The analysis environment that produced this archive cannot reach `proxy.golang.org` or `registry.npmjs.org`, so it cannot safely manufacture the checksum-bearing generated files.

On the first Internet-connected checkout, run:

```bash
npm install --global npm@10.9.2
./scripts/generate-lockfiles.sh
python3 scripts/check_dependency_locks.py
```

Commit the resulting `backend/go.mod` (if `go mod tidy` adds indirect requirements), `backend/go.sum`, `frontend/package-lock.json`, and `e2e/package-lock.json`. After that, normal CI and Docker builds are fully lock-enforced and use no unlocked fallback.
