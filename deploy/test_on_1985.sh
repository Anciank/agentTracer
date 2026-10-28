#!/usr/bin/env bash
# Run ONLY when TechLeadBro/TestingBro says :1985 is ours.
# Brings up at-m2 from THIS checkout, restore-drills prod dump, verifies migrations
# bootstrap, exercises deploy/backup, then `down -v` OUR stack only.
# NEVER touches compose project `agenttracer` (:1984).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

COMPOSE_PROJECT=at-m2
COMPOSE_FILES="-f docker-compose.yml -f deploy/docker-compose.at-m2.yml"
# shellcheck disable=SC2086
compose() { docker compose -p "$COMPOSE_PROJECT" $COMPOSE_FILES "$@"; }

cleanup_at_m2() {
  echo "[1985-test] cleanup trap: down -v OUR at-m2 only" >&2
  compose down -v || docker compose -p at-m2 down -v || true
}
trap cleanup_at_m2 EXIT


DUMP="${AGENTTRACER_DUMP:-$HOME/backups/drill/prod-for-drill-20261028-092601.sql.gz}"
PROD_COUNTS_EXPECT="${AGENTTRACER_PROD_COUNTS:-attachments=1
feedback=0
runs=175
sessions=7}"
HEALTH_URL="${AGENTTRACER_HEALTH_URL:-http://127.0.0.1:1985/health}"

echo "=== [1985-test] repo=$(git rev-parse --short HEAD) dump=$DUMP ==="
if [[ ! -f "$DUMP" ]]; then
  echo "ERROR: dump not found: $DUMP" >&2
  exit 1
fi

# Refuse if something else still owns :1985 and we weren't told to take over.
if docker ps --format '{{.Names}} {{.Ports}}' | grep -q '0.0.0.0:1985'; then
  OWNER="$(docker ps --format '{{.Names}} {{.Ports}}' | grep '0.0.0.0:1985' || true)"
  if [[ "${AGENTTRACER_TAKEOVER_1985:-0}" != "1" ]]; then
    echo "ERROR: :1985 already bound by: $OWNER" >&2
    echo "Set AGENTTRACER_TAKEOVER_1985=1 only after TestingBro hands us the port." >&2
    exit 2
  fi
  echo "[1985-test] takeover: down -v existing at-m2 (approved handoff)"
  compose down -v || docker compose -p at-m2 down -v || true
fi

# Ensure secrets for build/run
mkdir -p secrets
if [[ ! -f secrets/api_key ]]; then
  # Prefer copying from prod checkout without printing it
  if [[ -f "$HOME/src/agentTracer/secrets/api_key" ]]; then
    cp "$HOME/src/agentTracer/secrets/api_key" secrets/api_key
    chmod 600 secrets/api_key
  else
    echo "test-key-ops-1985" > secrets/api_key
    chmod 600 secrets/api_key
  fi
fi

echo "[1985-test] up stack"
compose up -d --build
for i in $(seq 1 60); do
  if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
    echo "[1985-test] healthy at ${i}s"
    break
  fi
  sleep 1
  if [[ "$i" == "60" ]]; then
    echo "ERROR: health failed" >&2
    compose logs app | tail -40 >&2 || true
    exit 1
  fi
done

echo "[1985-test] capture pre-restore counts (empty-ish OK)"
docker exec at-m2-postgres psql -U agenttracer -d agenttracer -c '\dt' || true

echo "[1985-test] stop app before restore so it cannot write during/after dump load"
compose stop app

echo "[1985-test] restore prod dump"
AGENTTRACER_DUMP="$DUMP" AGENTTRACER_PG_CONTAINER=at-m2-postgres \
  "$REPO_ROOT/deploy/restore_drill.sh" | tee /tmp/at-ops-restore-counts.txt

# Baseline = counts actually present in the restored dump (authoritative vs prod-at-dump-time)
PROD_COUNTS_EXPECT="$(docker exec at-m2-postgres psql -U agenttracer -d agenttracer -t -A -c \
  "SELECT tablename||'='||n FROM (
     SELECT 'sessions' tablename, count(*)::text n FROM sessions
     UNION ALL SELECT 'runs', count(*)::text FROM runs
     UNION ALL SELECT 'feedback', count(*)::text FROM feedback
     UNION ALL SELECT 'attachments', count(*)::text FROM attachments
   ) s ORDER BY 1")"
echo "[1985-test] restored dump baseline counts:"
echo "$PROD_COUNTS_EXPECT"

echo "[1985-test] force-recreate app so migrations.Up bootstraps schema_migrations on restored DB"
compose up -d --no-deps --force-recreate app
sleep 3
for i in $(seq 1 30); do
  curl -fsS "$HEALTH_URL" >/dev/null 2>&1 && break
  sleep 1
done

echo "[1985-test] schema_migrations (expect 001-005 after bootstrap+005)"
docker exec at-m2-postgres psql -U agenttracer -d agenttracer -v ON_ERROR_STOP=1 -c \
  "SELECT version, applied_at FROM schema_migrations ORDER BY 1"
SM_VERS="$(docker exec at-m2-postgres psql -U agenttracer -d agenttracer -t -A -c \
  "SELECT string_agg(version, ',' ORDER BY version) FROM schema_migrations")"
echo "[1985-test] schema_migrations versions: $SM_VERS"
WANT_SM="001_init,002_run_metrics,003_langgraph,004_agent_map,005_trace_graph_cache"
if [[ "$SM_VERS" != "$WANT_SM" ]]; then
  echo "ERROR: schema_migrations want $WANT_SM got $SM_VERS" >&2
  exit 1
fi
if ! docker exec at-m2-postgres psql -U agenttracer -d agenttracer -t -A -c \
  "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name='trace_graph_cache')" | grep -q t; then
  echo "ERROR: trace_graph_cache missing after Up" >&2
  exit 1
fi

COUNTS="$(docker exec at-m2-postgres psql -U agenttracer -d agenttracer -t -A -c \
  "SELECT tablename||'='||n FROM (
     SELECT 'sessions' tablename, count(*)::text n FROM sessions
     UNION ALL SELECT 'runs', count(*)::text FROM runs
     UNION ALL SELECT 'feedback', count(*)::text FROM feedback
     UNION ALL SELECT 'attachments', count(*)::text FROM attachments
   ) s ORDER BY 1")"
echo "[1985-test] post-migrate counts:"
echo "$COUNTS"
echo "[1985-test] expected counts (from restored dump / prod-at-dump-time):"
echo "$PROD_COUNTS_EXPECT"
if [[ "$(echo "$COUNTS" | tr -d '[:space:]')" != "$(echo "$PROD_COUNTS_EXPECT" | tr -d '[:space:]')" ]]; then
  echo "ERROR: row count mismatch vs prod" >&2
  exit 1
fi

echo "[1985-test] backup.sh against at-m2"
AGENTTRACER_COMPOSE_PROJECT=at-m2 \
  AGENTTRACER_PG_CONTAINER=at-m2-postgres \
  AGENTTRACER_MINIO_CONTAINER=at-m2-minio \
  AGENTTRACER_BACKUP_ROOT="$HOME/backups/drill-1985" \
  "$REPO_ROOT/deploy/backup.sh"

echo "[1985-test] deploy.sh dry (skip pull; exercise build/health/rollback path)"
AGENTTRACER_COMPOSE_PROJECT=at-m2 \
  AGENTTRACER_COMPOSE_FILES="-f docker-compose.yml -f deploy/docker-compose.at-m2.yml" \
  AGENTTRACER_HEALTH_URL="$HEALTH_URL" \
  AGENTTRACER_SKIP_PULL=1 \
  AGENTTRACER_PG_CONTAINER=at-m2-postgres \
  AGENTTRACER_MINIO_CONTAINER=at-m2-minio \
  AGENTTRACER_BACKUP_ROOT="$HOME/backups/drill-1985" \
  "$REPO_ROOT/deploy/deploy.sh"

echo "[1985-test] exercise rollback: force health failure via dead HEALTH_URL"
GOOD_HEALTH="$HEALTH_URL"
set +e
AGENTTRACER_COMPOSE_PROJECT=at-m2 \
  AGENTTRACER_COMPOSE_FILES="-f docker-compose.yml -f deploy/docker-compose.at-m2.yml" \
  AGENTTRACER_HEALTH_URL="http://127.0.0.1:1/health-should-fail" \
  AGENTTRACER_HEALTH_RETRIES=5 \
  AGENTTRACER_SKIP_PULL=1 \
  AGENTTRACER_SKIP_BACKUP=1 \
  AGENTTRACER_PG_CONTAINER=at-m2-postgres \
  AGENTTRACER_MINIO_CONTAINER=at-m2-minio \
  AGENTTRACER_BACKUP_ROOT="$HOME/backups/drill-1985" \
  "$REPO_ROOT/deploy/deploy.sh"
ROLL_RC=$?
set -e
if [[ "$ROLL_RC" -eq 0 ]]; then
  echo "ERROR: deploy.sh should have failed health check for rollback drill" >&2
  exit 1
fi
echo "[1985-test] deploy.sh exited $ROLL_RC as expected; verifying real :1985 health after rollback"
for i in $(seq 1 30); do
  if curl -fsS "$GOOD_HEALTH" >/dev/null 2>&1; then
    echo "[1985-test] rollback path left stack healthy on :1985"
    break
  fi
  sleep 1
  if [[ "$i" == "30" ]]; then
    echo "ERROR: :1985 unhealthy after rollback drill" >&2
    compose logs app | tail -40 >&2 || true
    exit 1
  fi
done

echo "[1985-test] final health + mem"
curl -fsS "$HEALTH_URL"; echo
docker stats --no-stream --format 'table {{.Name}}\t{{.MemUsage}}' | grep -E 'at-m2|NAME' || true

echo "[1985-test] tearing down OUR at-m2 stack (down -v)"
trap - EXIT
compose down -v

echo "[1985-test] verify no at-m2 leftovers"
LEFT="$(docker ps -a --format '{{.Names}}' | grep -E '^at-m2' || true)"
if [[ -n "$LEFT" ]]; then
  echo "ERROR: leftover containers: $LEFT" >&2
  exit 1
fi
VOLS="$(docker volume ls -q | grep -E 'at-m2' || true)"
if [[ -n "$VOLS" ]]; then
  echo "ERROR: leftover volumes: $VOLS" >&2
  exit 1
fi
if docker ps --format '{{.Ports}}' | grep -q '0.0.0.0:1985'; then
  echo "ERROR: :1985 still bound after teardown" >&2
  docker ps --format '{{.Names}} {{.Ports}}' | grep 1985 >&2 || true
  exit 1
fi
# prod still up
if ! docker ps --format '{{.Names}}' | grep -qx 'agenttracer-app-1'; then
  echo "ERROR: prod agenttracer-app-1 missing after test — investigate" >&2
  exit 1
fi

echo "=== [1985-test] PASS $(git rev-parse --short HEAD) ==="
