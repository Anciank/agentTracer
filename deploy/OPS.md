# Ops (P0)

## Migrations
- Runner: `migrations.Up` — advisory lock, per-file transaction, `schema_migrations` table.
- Bootstrap: empty `schema_migrations` + existing `runs` → mark `001`–`003` applied without re-exec; `004+` still run (004 is IF NOT EXISTS).
- Destructive SQL (`DROP COLUMN`, `ALTER … TYPE`, `DROP TABLE`, `TRUNCATE`, `RENAME COLUMN`) rejected unless file contains `-- agenttracer:expand-contract`.

## Backups (VPS-local for now — no offsite yet)
- `deploy/backup.sh` → `~/backups/postgres/{daily,weekly}` + `~/backups/silo/<stamp>/`
- Retention: 7 daily, 4 weekly, 7 silo mirrors.
- Silo mirror uses cached `amazon/aws-cli` image (`minio/mc` is not pullable on this VPS).
- Cron: see `deploy/crontab.example` — **do not install without TechLeadBro approval**.

## Deploy
- `deploy/deploy.sh`: backup → ff-only pull → build → `up -d --no-deps app` → health → rollback image on failure.
- Never `down -v` on prod (`agenttracer` / :1984).

## Resource limits (prod compose)
- postgres 512m, minio 256m, app 384m (+ GOMEMLIMIT 300MiB). Leaves room for one :1985 test stack.

## :1985 queue
CodeBro → TestingBro → DevOpsBro. Do not start on :1985 until TestingBro hands it off.
When ours: copy this branch to the VPS (or push for TechLeadBro), then:

```bash
cd ~/src/agentTracer   # or a worktree checked out at ops-migrations tip
# after handoff:
AGENTTRACER_TAKEOVER_1985=1 ./deploy/test_on_1985.sh
```

Scripts: `deploy/restore_drill.sh`, `deploy/test_on_1985.sh`, `deploy/backup.sh`, `deploy/deploy.sh`.
