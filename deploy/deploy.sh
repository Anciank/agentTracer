#!/usr/bin/env bash
# Prod-safe deploy for agentTracer.
# Steps: pre-deploy backup → ff-only pull → pull pinned images → preflight →
#        build → up app → health → startup identity check → rollback on failure.
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

for arg in "$@"; do
  case "$arg" in
    --skip-pull) SKIP_PULL=1 ;;
    --skip-backup) SKIP_BACKUP=1 ;;
    --skip-preflight) SKIP_PREFLIGHT=1 ;;
    --allow-down-v) echo "ERROR: --allow-down-v refused; never down -v on deploy" >&2; exit 2 ;;
    *) echo "unknown arg: $arg" >&2; exit 2 ;;
  esac
done

echo "[deploy] project=$COMPOSE_PROJECT branch=$BRANCH health=$HEALTH_URL"

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
ROLLBACK_TAG="${COMPOSE_PROJECT}-app:rollback-prev"
if [[ -n "$PREV_IMAGE_ID" ]]; then
  docker tag "$PREV_IMAGE_ID" "$ROLLBACK_TAG"
  echo "[deploy] pinned rollback tag $ROLLBACK_TAG"
fi

rollback_app() {
  local reason="$1"
  echo "[deploy] FAILED ($reason) — rolling back app container to previous image" >&2
  local IMG_NAME SRC=""
  IMG_NAME="$(compose config --images app 2>/dev/null | head -1 || true)"
  # Prefer explicit local name; never retag onto pgsty/silo or postgres.
  if [[ -z "$IMG_NAME" || "$IMG_NAME" == *"pgsty/silo"* || "$IMG_NAME" == *"postgres"* ]]; then
    IMG_NAME="agenttracer-app:local"
  fi
  if docker image inspect "$ROLLBACK_TAG" >/dev/null 2>&1; then
    SRC="$ROLLBACK_TAG"
  elif [[ -n "${PREV_IMAGE_ID:-}" ]] && docker image inspect "$PREV_IMAGE_ID" >/dev/null 2>&1; then
    SRC="$PREV_IMAGE_ID"
  fi
  if [[ -n "$SRC" ]]; then
    docker tag "$SRC" "$IMG_NAME"
    compose up -d --no-deps --no-build app
    sleep 3
    local VERIFY_URL="${AGENTTRACER_ROLLBACK_HEALTH_URL:-$HEALTH_URL}"
    if curl -fsS "$VERIFY_URL" >/dev/null 2>&1; then
      echo "[deploy] rollback restored health ($VERIFY_URL)" >&2
    else
      echo "[deploy] rollback attempted; health still failing on $VERIFY_URL — manual intervention needed" >&2
    fi
  else
    echo "[deploy] no previous image to roll back to (src=${SRC:-none} img=${IMG_NAME:-none})" >&2
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

echo "[deploy] build app (image=agenttracer-app:local)"
compose build app

echo "[deploy] recreate app (no down -v)"
compose up -d --no-deps app

echo "[deploy] waiting for health $HEALTH_URL"
ok=0
for i in $(seq 1 "$HEALTH_RETRIES"); do
  if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
    ok=1
    echo "[deploy] healthy after ${i}s"
    break
  fi
  sleep 1
done

if [[ "$ok" != "1" ]]; then
  rollback_app "health check"
fi

echo "[deploy] startup identity check"
if ! AGENTTRACER_COMPOSE_PROJECT="$COMPOSE_PROJECT" \
    AGENTTRACER_COMPOSE_FILES="$COMPOSE_FILE_ARGS" \
    "$REPO_ROOT/deploy/startup_check.sh"; then
  rollback_app "startup identity check"
fi

echo "[deploy] success $(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
