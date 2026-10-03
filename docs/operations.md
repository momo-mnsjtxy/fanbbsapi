# Operations rehearsal

This document is an offline-safe deployment checklist. It contains no credentials and does not authorize deployment.

## Configuration gates

- Use PostgreSQL 17 and inject `FANBBS_DATABASE_URL` from the platform secret store
- Keep `FANBBS_SEED_DEMO=false` outside local development
- Bind the Go process to a private interface and expose `/api/` through the same-origin reverse proxy
- Replace the local blob adapter only after object-store, malware scanning, retention and deletion behavior have acceptance tests
- Treat `/healthz` as dependency readiness and `/metrics` plus structured request logs as internal-only endpoints

## Backup and restore

Run `scripts/rehearse_backup_restore.sh` against synthetic rehearsal databases before release. The PostgreSQL mode uses a custom-format logical backup and restores into a distinct pre-created database. It compares migration counts after restore and never contacts a production system by itself.

Record the exact source commit, PostgreSQL major version, backup checksum, restore duration and integrity queries. A production launch still requires encrypted backup storage, retention policy, periodic restore drills and an approved recovery-time/recovery-point objective.

Before a committed synthetic import rehearsal, run `cmd/importlegacy` with
`--dry-run` against a disposable migrated target. The returned report includes
the same source hash, reconciliation counts and quarantine reasons while the
transaction is rolled back. A dry run commits no application, import-run or
quarantine rows, although PostgreSQL sequence values can advance because
sequences are not transactional.

## Rollback

Database migrations are forward-only and checksummed. Roll back application code only when it remains compatible with the already-applied schema. For incompatible changes, restore into a new database from the last verified backup, validate it, then switch traffic using the deployment platform's controlled procedure. Never rewrite `schema_migrations` manually.
