#!/usr/bin/env bash
# Prod-safe deploy for agentTracer.
# Steps: pre-deploy backup → ff-only pull → pull pinned images → preflight →
#        build → up app [optional one-shot graph-cache backfill] → health →
#        startup identity check → rollback on failure.
# NEVER runs `down -v` on prod. Test with AGENTTRACER_COMPOSE_PROJECT=at-m2 and override files on :1985.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

COMPOSE_PROJECT="${AGENTTRACER_COMPOSE_PROJECT:-agenttracer}"
# Space-separated -f args, e.g. "-f docker-compose.yml -f deploy/docker-compose.at-m2.yml"
COMPOSE_FILE_ARGS="${AGENTTRACER_COMPOSE_FILES:--f docker-compose.yml}"
# shellcheck disable=SC2086
compose() { docker compose -p "$COMPOSE_PROJECT" $COMPOSE_FILE_ARGS "$@"; }

BRANCH="${AGENTTRACER_DEPLOY_BRANCH:-go-rewrite}"
HEALTH_URL="${AGENTTRACER_HEALTH_URL:-http://127.0.0.1:1984/health}"
HEALTH_RETRIES="${AGENTTRACER_HEALTH_RETRIES:-30}"
SKIP_PULL="${AGENTTRACER_SKIP_PULL:-0}"
SKIP_BACKUP="${AGENTTRACER_SKIP_BACKUP:-0}"
SKIP_PREFLIGHT="${AGENTTRACER_SKIP_PREFLIGHT:-0}"
# One-shot post-migrate: start once with AGENTTRACER_BACKFILL_GRAPH_CACHE=1, then clear.
BACKFILL_GRAPH_CACHE="${AGENTTRACER_DEPLOY_BACKFILL_GRAPH_CACHE:-0}"

for arg in "$@"; do
  case "$arg" in
    --skip-pull) SKIP_PULL=1 ;;
    --skip-backup) SKIP_BACKUP=1 ;;
    --skip-preflight) SKIP_PREFLIGHT=1 ;;
    --backfill-graph-cache) BACKFILL_GRAPH_CACHE=1 ;;
    --allow-down-v) echo "ERROR: --allow-down-v refused; never down -v on deploy" >&2; exit 2 ;;
    *) echo "unknown arg: $arg" >&2; exit 2 ;;
  esac
done

echo "[deploy] project=$COMPOSE_PROJECT branch=$BRANCH health=$HEALTH_URL"

# HARDCODED app image + rollback tag. NEVER derive from `docker compose config --images`
# (Compose 2.40.x ignores the service arg; lists ALL images in nondeterministic order;
# head -1 can be pgsty/silo → retag poisons MinIO). See OPS.md root-cause note.
case "$COMPOSE_PROJECT" in
  agenttracer)
    APP_IMAGE="agenttracer-app:local"
    ROLLBACK_TAG="agenttracer-app:rollback-prev"
    ;;
  at-m2)
    APP_IMAGE="agenttracer-at-m2:local"
    ROLLBACK_TAG="agenttracer-at-m2:rollback-prev"
    ;;
  at-m3)
    APP_IMAGE="agenttracer-at-m3:local"
    ROLLBACK_TAG="agenttracer-at-m3:rollback-prev"
    ;;
  *)
    echo "ERROR: unknown COMPOSE_PROJECT='$COMPOSE_PROJECT' — refuse to guess image tags" >&2
    exit 2
    ;;
esac
if [[ -n "${AGENTTRACER_APP_IMAGE:-}" ]]; then
  case "$AGENTTRACER_APP_IMAGE" in
    agenttracer-app:*|agenttracer-at-m2:*|agenttracer-at-m3:*) APP_IMAGE="$AGENTTRACER_APP_IMAGE" ;;
    *) echo "ERROR: AGENTTRACER_APP_IMAGE='$AGENTTRACER_APP_IMAGE' not an allowed agenttracer-* name" >&2; exit 2 ;;
  esac
fi
if [[ -n "${AGENTTRACER_ROLLBACK_TAG:-}" ]]; then
  case "$AGENTTRACER_ROLLBACK_TAG" in
    agenttracer-app:*|agenttracer-at-m2:*|agenttracer-at-m3:*) ROLLBACK_TAG="$AGENTTRACER_ROLLBACK_TAG" ;;
    *) echo "ERROR: AGENTTRACER_ROLLBACK_TAG='$AGENTTRACER_ROLLBACK_TAG' not an allowed agenttracer-* name" >&2; exit 2 ;;
  esac
fi
echo "[deploy] app_image=$APP_IMAGE rollback_tag=$ROLLBACK_TAG"

if [[ "$SKIP_BACKUP" != "1" ]]; then
  echo "[deploy] pre-deploy backup"
  AGENTTRACER_COMPOSE_PROJECT="$COMPOSE_PROJECT" \
    AGENTTRACER_PG_CONTAINER="${AGENTTRACER_PG_CONTAINER:-}" \
    AGENTTRACER_MINIO_CONTAINER="${AGENTTRACER_MINIO_CONTAINER:-}" \
    "$REPO_ROOT/deploy/backup.sh"
else
  echo "[deploy] skipping backup"
fi

# Resolve app container id via compose (works with container_name overrides like at-m2-app).
APP_CID="$(compose ps -q app 2>/dev/null || true)"
PREV_IMAGE_ID=""
if [[ -n "$APP_CID" ]]; then
  PREV_IMAGE_ID="$(docker inspect "$APP_CID" -f '{{.Image}}' 2>/dev/null || true)"
fi
if [[ -z "$PREV_IMAGE_ID" ]]; then
  PREV_IMAGE_ID="$(docker inspect "${COMPOSE_PROJECT}-app-1" -f '{{.Image}}' 2>/dev/null || true)"
fi
echo "[deploy] previous app image=${PREV_IMAGE_ID:-none} (cid=${APP_CID:-none})"
if [[ -n "$PREV_IMAGE_ID" ]]; then
  docker tag "$PREV_IMAGE_ID" "$ROLLBACK_TAG"
  echo "[deploy] pinned rollback tag $ROLLBACK_TAG"
fi

rollback_app() {
  local reason="$1"
  echo "[deploy] FAILED ($reason) — rolling back app container to previous image" >&2
  local SRC=""
  if docker image inspect "$ROLLBACK_TAG" >/dev/null 2>&1; then
    SRC="$ROLLBACK_TAG"
  elif [[ -n "${PREV_IMAGE_ID:-}" ]] && docker image inspect "$PREV_IMAGE_ID" >/dev/null 2>&1; then
    SRC="$PREV_IMAGE_ID"
  fi
  if [[ -n "$SRC" ]]; then
    echo "[deploy] rollback: docker tag $SRC → $APP_IMAGE" >&2
    docker tag "$SRC" "$APP_IMAGE"
    compose up -d --no-deps --no-build app
    sleep 3
    local VERIFY_URL="${AGENTTRACER_ROLLBACK_HEALTH_URL:-$HEALTH_URL}"
    if curl -fsS "$VERIFY_URL" >/dev/null 2>&1; then
      echo "[deploy] rollback restored health ($VERIFY_URL)" >&2
    else
      echo "[deploy] rollback attempted; health still failing on $VERIFY_URL — manual intervention needed" >&2
    fi
  else
    echo "[deploy] no previous image to roll back to (src=${SRC:-none} img=$APP_IMAGE)" >&2
  fi
  exit 1
}

if [[ "$SKIP_PULL" != "1" ]]; then
  echo "[deploy] ff-only pull origin/$BRANCH"
  git fetch origin "$BRANCH"
  cur="$(git rev-parse --abbrev-ref HEAD)"
  if [[ "$cur" == "$BRANCH" ]]; then
    git merge --ff-only "origin/$BRANCH"
  else
    echo "[deploy] WARN: HEAD is $cur, not $BRANCH; not switching branches (use SKIP_PULL for test stacks)"
  fi
fi

# Ensure pinned third-party images are present locally (preflight itself never pulls).
echo "[deploy] pull pinned postgres + minio/silo digests"
compose pull postgres minio

if [[ "$SKIP_PREFLIGHT" != "1" ]]; then
  echo "[deploy] preflight (digest + entrypoint)"
  AGENTTRACER_COMPOSE_PROJECT="$COMPOSE_PROJECT" \
    AGENTTRACER_COMPOSE_FILES="$COMPOSE_FILE_ARGS" \
    "$REPO_ROOT/deploy/preflight.sh"
else
  echo "[deploy] skipping preflight"
fi

echo "[deploy] build app (image=$APP_IMAGE)"
compose build app

wait_health() {
  local label="$1"
  local ok=0
  echo "[deploy] waiting for health $HEALTH_URL ($label)"
  for i in $(seq 1 "$HEALTH_RETRIES"); do
    if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
      ok=1
      echo "[deploy] healthy after ${i}s ($label)"
      break
    fi
    sleep 1
  done
  [[ "$ok" == "1" ]] || return 1
  return 0
}

if [[ "$BACKFILL_GRAPH_CACHE" == "1" ]]; then
  echo "[deploy] one-shot graph-cache backfill start (AGENTTRACER_BACKFILL_GRAPH_CACHE=1)"
  echo "[deploy] NOTE: blocks listen until BackfillGraphCache finishes; then we clear the env"
  AGENTTRACER_BACKFILL_GRAPH_CACHE=1 compose up -d --no-deps --force-recreate app
  if ! wait_health "backfill start"; then
    rollback_app "health check (during backfill start)"
  fi
  # Best-effort: wait for the backfill log line (app already listening after it).
  APP_CID_BF="$(compose ps -q app 2>/dev/null || true)"
  if [[ -n "$APP_CID_BF" ]]; then
    for i in $(seq 1 120); do
      if docker logs "$APP_CID_BF" 2>&1 | grep -q 'graph cache backfill wrote'; then
        echo "[deploy] backfill log observed after ${i}s"
        break
      fi
      sleep 1
    done
  fi
  echo "[deploy] clearing AGENTTRACER_BACKFILL_GRAPH_CACHE and recreating app (steady state)"
  AGENTTRACER_BACKFILL_GRAPH_CACHE= compose up -d --no-deps --force-recreate app
  if ! wait_health "post-backfill clear"; then
    rollback_app "health check (after clearing backfill env)"
  fi
else
  echo "[deploy] recreate app (no down -v)"
  # Ensure backfill env is not left over from a previous one-shot deploy.
  AGENTTRACER_BACKFILL_GRAPH_CACHE= compose up -d --no-deps app
  if ! wait_health "normal"; then
    rollback_app "health check"
  fi
fi

echo "[deploy] startup identity check"
if ! AGENTTRACER_COMPOSE_PROJECT="$COMPOSE_PROJECT" \
    AGENTTRACER_COMPOSE_FILES="$COMPOSE_FILE_ARGS" \
    "$REPO_ROOT/deploy/startup_check.sh"; then
  rollback_app "startup identity check"
fi

echo "[deploy] success $(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
