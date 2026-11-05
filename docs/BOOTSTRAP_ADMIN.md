# Initial Administrator Bootstrap

Target: **v0.41.1 Production Hardening — Step 3**

Step 3 removes all runtime fixed demo accounts and passwords. A clean CPIM-MES database now requires an explicitly supplied initial administrator before the backend will start serving HTTP.

## Security contract

There are no built-in usernames or passwords in Development, Test, or Production.

When `users` is empty, configure both:

```text
BOOTSTRAP_ADMIN_USERNAME=<initial administrator username>
BOOTSTRAP_ADMIN_PASSWORD_FILE=<path to secret file>
```

The password itself must **not** be placed in an environment variable. `BOOTSTRAP_ADMIN_PASSWORD` is explicitly rejected so process listings, crash reports, deployment manifests, and environment dumps do not become the primary secret transport.

The password file must contain one password of **15-72 bytes**. One trailing LF/CRLF is accepted and removed. Embedded line breaks and NUL bytes are rejected. The 72-byte ceiling matches bcrypt's input limit.

## Startup behavior

```text
configuration validation
  -> password-file readability/strength validation (when configured)
  -> PostgreSQL connection
  -> Migration Manager (0001..0041)
  -> count users
       -> users > 0: bootstrap skipped, no existing account changed
       -> users = 0 and bootstrap absent: startup FAIL
       -> users = 0 and bootstrap present:
            advisory transaction lock
            -> re-check users count
            -> bcrypt hash password
            -> create exactly one active admin
            -> commit
  -> services/router
  -> HTTP listen
```

`pg_advisory_xact_lock` protects the bootstrap critical section. If multiple backend instances start concurrently against the same empty database, only one creates the initial account; the others observe the newly created user and continue without inserting another bootstrap account.

The plaintext password bytes are zeroed in application memory immediately after bcrypt hashing returns. Password contents are never written to application logs; bootstrap status logging contains only the configured username and outcome.

## Docker Compose clean-database bootstrap

Create a local secret file that is excluded by `.gitignore`:

```bash
mkdir -p .secrets
openssl rand -base64 36 | tr -d '\n' > .secrets/bootstrap-admin-password
chmod 600 .secrets/bootstrap-admin-password
```

Choose the initial username and start with the bootstrap override:

```bash
export BOOTSTRAP_ADMIN_USERNAME=initial-admin
# Development bootstrap:
docker compose \
  -f docker-compose.yml \
  -f docker-compose.dev.yml \
  -f docker-compose.bootstrap.yml \
  up -d --build

# Production bootstrap uses docker-compose.prod.yml instead of docker-compose.dev.yml
# and requires the production environment described in PRODUCTION_FRONTEND_CONTAINER.md.
```

The override mounts `.secrets/bootstrap-admin-password` as `/run/secrets/bootstrap_admin_password` and exposes only the file path to the backend.

After login is verified:

1. Store the password in the organization's approved password manager/secret manager.
2. Stop the Compose stack **without deleting the database volume**.
3. Delete `.secrets/bootstrap-admin-password`.
4. Restart using normal Compose without the bootstrap override.

Once any user exists, bootstrap does not overwrite, reset, promote, or otherwise mutate existing users.

## Native/container deployment

For a non-Compose deployment, mount a secret file using the platform's normal secret mechanism and point `BOOTSTRAP_ADMIN_PASSWORD_FILE` at that in-container path. A common production path is under `/run/secrets`, but CPIM-MES does not require a specific directory.

Example configuration shape:

```text
BOOTSTRAP_ADMIN_USERNAME=initial-admin
BOOTSTRAP_ADMIN_PASSWORD_FILE=/run/secrets/cpim_bootstrap_admin_password
```

Do not commit the secret file and do not bake it into a container image.

## Existing v0.41.0 / Step 2 databases

Step 3 intentionally does **not** auto-delete or auto-reset existing users. Automatic credential mutation would make an application startup unexpectedly destructive and could lock operators out.

Therefore:

- do not promote a development database that still contains old demo accounts into Production;
- prefer a clean Production database plus explicit bootstrap;
- if an existing Production-like database must be retained, perform a controlled credential migration/rotation outside this bootstrap path before deployment.

Migration `0003_users.sql` remains byte-for-byte unchanged because its checksum is part of the migration ledger contract. Its historical comments are not executable and are the only retained copy of the former demo credential text in the release bundle.

## E2E / CI

Playwright has no fallback credentials. It requires:

```text
E2E_ADMIN_USERNAME
E2E_ADMIN_PASSWORD
```

GitHub Actions generates a fresh random password for every E2E run, masks it in logs, writes it to the one-time Compose secret file, bootstraps the database, and supplies the same ephemeral value only to the Playwright process.

## Validation

Run:

```bash
python3 backend/scripts/check_demo_credentials_removed.py
python3 backend/scripts/check_production_config.py
```

Backend tests additionally cover bootstrap configuration pairing, secret-file validation, password length limits, and removal of the legacy demo-seed environment setting.
