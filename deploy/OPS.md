# Ops (P0)

## Migrations
- Runner: `migrations.Up` — advisory lock, per-file transaction, `schema_migrations` table.
- Bootstrap: empty `schema_migrations` + existing `runs` → mark `001`–`004` applied without re-exec.
- Destructive SQL (`DROP COLUMN`, `ALTER … TYPE`, `DROP TABLE`, `TRUNCATE`, `RENAME COLUMN`) rejected unless file contains `-- agenttracer:expand-contract`.

## Backups
- `deploy/backup.sh` → `~/backups/postgres/{daily,weekly}` + `~/backups/silo/<stamp>/`
- Retention: 7 daily, 4 weekly, 7 silo mirrors.
- Cron: see `deploy/crontab.example` (do not install without approval).

## Deploy
- `deploy/deploy.sh`: backup → ff-only pull → build → `up -d --no-deps app` → health → rollback image on failure.
- Never `down -v` on prod.

## Resource limits (prod compose)
- postgres 512m, minio 256m, app 384m (+ GOMEMLIMIT 300MiB). Leaves ~room for one :1985 test stack.
