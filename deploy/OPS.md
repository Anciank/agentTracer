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


## Root cause — poisoned `pgsty/silo` tag (2026-10-28)

**Verdict (confirmed):** `deploy.sh` rollback did `docker tag $SRC $(docker compose config --images app | head -1)`.
On Docker Compose **2.40.3**, `config --images <service>` **ignores the service argument** and lists **all**
project images; **order is nondeterministic** (~1/3 chance `head -1` is `pgsty/silo:RELEASE…`).
Our :1985 forced-rollback drill at **05:32 EDT** pinned `at-m2-app:rollback-prev` then, five seconds later,
retagged the **same image id** onto `pgsty/silo:RELEASE.2026-10-26T00-00-00Z` (dockerd journal). That poisoned
the local silo tag; prod minio later ran the agenttracer binary and crash-looped until Hub re-pull.

**Fix in ops-pin:** hardcode `agenttracer-app:local` / `agenttracer-at-m2:local` (+ matching `:rollback-prev`);
never call `config --images` for retags. Guard: `deploy/check_no_dynamic_image_tags.sh`.

## Deploy
- `deploy/deploy.sh`: backup → ff-only pull **or** `--sha <commit>` → `compose pull` pinned images → `preflight.sh` → build → `up -d --no-deps app` → health → `startup_check.sh` → rollback app image on failure.
  - Default: `git merge --ff-only origin/go-rewrite` (tip). Tip must be the commit you intend — if tip moved past your audited SHA, use `--sha`.
  - Exact pin: `./deploy/deploy.sh --sha 2371a25` (must be ancestor of / equal to `origin/go-rewrite`).
- Never `down -v` on prod (`agenttracer` / :1984).
- Third-party images are digest-pinned in `docker-compose.yml` / `deploy/docker-compose.at-*.yml` (never `:latest`).
- App image names (never retag silo/postgres):
  - prod: `agenttracer-app:local`
  - at-m2 (:1985): `agenttracer-at-m2:local`
  - at-m3 (:1985): `agenttracer-at-m3:local`
- Read-only prod check: `AGENTTRACER_COMPOSE_PROJECT=agenttracer ./deploy/preflight.sh`
- Negative :1985 suite (after handoff, do not run on prod) — poison preflight +
  image-rename rollback dry-run:
  `AGENTTRACER_TAKEOVER_1985=1 ./deploy/preflight_negative_1985.sh`
  Sibling: `deploy/rename_rollback_1985.sh` (invoked by the suite).




## Deploy lock & :1985 slot (OOM guard)

Prod go builds peaked ~1.06GiB above baseline on this 1.9GiB VPS. Concurrent `at-*`
test stacks on :1985 plus a prod deploy will OOM. Guards:

1. **`deploy/deploy.sh`** (before backup / build):
   - Acquires `/tmp/agenttracer-deploy.lock` via `flock` (override: `DEPLOY_LOCK_FILE`);
     released on EXIT through `trap`.
   - Refuses if any compose project name starts with `at-` (`docker compose ls -q`),
     unless `--force-with-test-stack` is passed.
   - Before `compose build`, aborts if `MemAvailable` in `/proc/meminfo` is below
     **900 MiB** (override: `DEPLOY_MIN_AVAIL_MB`). Prints the observed value + threshold.
   - Read-only: `./deploy/deploy.sh --check-only` (alias `--preflight-only`) runs the
     test-stack + memory checks and a lock probe — **never** backup/build/up.

2. **`deploy/slot_check.sh`** — bots **must** run this before taking :1985:
   ```bash
   ./deploy/slot_check.sh
   ```
   Fails if the deploy lock is held, or if anything already listens on :1985.
   Shared helpers live in `deploy/oom_guards.sh`. Tests: `deploy/oom_guards_test.sh`.

## Release note — migration 006 + graph-cache backfill

- `006_graph_cache_lat_sentinel.sql` runs `DELETE FROM trace_graph_cache` (derived cache only) then
  relies on runtime rebuild. Safety checker does **not** flag bare `DELETE` (only DROP/TRUNCATE/TYPE/RENAME);
  the file also carries `-- agenttracer:expand-contract`.
- After 006, finished roots have no cache rows until rebuilt. New finishes write cache normally;
  **old finished traces stay empty on the map** until backfilled.
- **Prod deploy for this release must run once with backfill:**
  ```bash
  ./deploy/deploy.sh --backfill-graph-cache
  # or: AGENTTRACER_DEPLOY_BACKFILL_GRAPH_CACHE=1 ./deploy/deploy.sh
  ```
  That starts the app once with `AGENTTRACER_BACKFILL_GRAPH_CACHE=1`, waits for health (and the
  `graph cache backfill wrote N traces` log), then **force-recreates the app with the env cleared**.
  Do not leave the var set: on every start while `=1`, `main` runs `BackfillGraphCache(ctx, 0)`
  **before** listen (one-shot per process, not a loop). If the cache is already full it is a cheap
  empty SELECT; if many rows are missing it blocks startup for the full rebuild cost
  (`RefreshPathFingerprint` per finished root missing a cache row).
- Steady-state compose leaves `AGENTTRACER_BACKFILL_GRAPH_CACHE` empty via `${…:-}`.

- **Chat preview backfill** (migration `010` columns empty on legacy rows): same one-shot pattern —
  `./deploy/deploy.sh --backfill-chat-previews` or `AGENTTRACER_DEPLOY_BACKFILL_CHAT_PREVIEWS=1`.
  Starts once with `AGENTTRACER_BACKFILL_CHAT_PREVIEWS=1`, runs `BackfillChatPreviews` (batched, idempotent,
  resolves `_s3` cold pointers), then force-recreates with the env cleared. Prefer this over a SQL migration
  because preview extraction is Go (`chatPreviewsFromIO`) and may need the blob store.

## Resource limits (prod compose)
- postgres 512m, minio 256m, app 384m (+ GOMEMLIMIT 300MiB). Leaves room for one :1985 test stack.

## :1985 queue
CodeBro → TestingBro → DevOpsBro. Do not start on :1985 until TestingBro hands it off.
**Bots must run `./deploy/slot_check.sh` first** (fails if deploy lock held or :1985 busy).
When ours: copy this branch to the VPS (or push for TechLeadBro), then:

```bash
cd ~/src/agentTracer   # or a worktree checked out at ops-oom-guard / ops tip
./deploy/slot_check.sh
# after handoff + slot OK:
AGENTTRACER_TAKEOVER_1985=1 ./deploy/test_on_1985.sh
```

Scripts: `deploy/restore_drill.sh`, `deploy/test_on_1985.sh`, `deploy/backup.sh`, `deploy/deploy.sh`.

## Unit / integration tests (DB)

Never point `go test` at prod. DB tests use `AGENTTRACER_TEST_DATABASE_URL` only; names must end in `_test` or `scratch` (enforced by `internal/dbtest`). Example on the VPS throwaway PG:

```bash
docker run -d --name at-ops-mig-pg \
  -e POSTGRES_USER=agenttracer -e POSTGRES_PASSWORD=agenttracer \
  -e POSTGRES_DB=agenttracer_test postgres:16-alpine
# ... wait for ready, then:
export AGENTTRACER_TEST_DATABASE_URL="postgres://agenttracer:agenttracer@<pg-ip>:5432/agenttracer_test?sslmode=disable"
# Do NOT export AGENTTRACER_DATABASE_URL for tests.
go test ./...
```

## Image store / rollback protection (b1fee2c4 incident 2026-10-28)

**What happened:** Prod app container was still running digest `b1fee2c4…` while a concurrent
VPS build (BuildKit export ~08:00–08:09 EDT) retagged `agenttracer-app:local` onto a new id
(`439f5a…`). That left `b1fee2c4` **dangling**. Later `docker image inspect` / `docker tag`
of that id failed (`No such image`), and `docker commit` failed with a missing content
digest — so the image store had dropped blobs while the container still referenced the id.
`deploy.sh` then aborted at the rollback-pin `docker tag` step.

**Investigation (no culprit script found):**
- No ubuntu/root crontab; no systemd timer that prunes Docker.
- No `docker image prune` / `system prune` / `rmi -f` hits in `~/src/**/*.sh|md|py`,
  ubuntu bash_history, or (via sudo) root history / auth.log around the window.
- Docker journal shows BuildKit “exporting to image” / naming for app builds, then our
  recovery `docker import` — not an explicit prune log line.
- A running container **normally** keeps its image from `docker image prune` (with or
  without `-a`). So either something did `docker rmi -f` on the dangling id, BuildKit/
  content-store GC dropped blobs, or an intermediate recreate left a stale Image id.
  Concurrent **retag of prod `:local` without deploying** is the confirmed first step.

**Hard rules for every bot on this VPS:**
1. **Never** `docker image prune -a`, `docker system prune -a`, or `docker rmi -f` on
   `agenttracer-*` / `pgsty/silo` / `postgres` digests.
2. **Never** `docker compose build` / retag `agenttracer-app:local` on the VPS while prod
   is running that digest unless you are inside `deploy/deploy.sh` (which pins
   `:rollback-prev` first).
3. If you must reclaim disk, use only:
   ```bash
   docker image prune -a --filter 'label!=agenttracer.keep=true'
   ```
   (and still avoid touching anything a running container needs).

**Protections now in deploy:**
- `deploy_pin_rollback_image` refuses deploy if `PREV_IMAGE_ID` is missing from the store.
- Pins `agenttracer-*:rollback-prev` **and** labels it `agenttracer.keep=true` (via a
  no-op `FROM` rebuild) so label-filter prunes spare it.
- `slot_check.sh` prints the never-prune / never-retag reminder every time.

