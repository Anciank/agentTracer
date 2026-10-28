#!/usr/bin/env bash
# Prod-safe deploy for agentTracer.
# Steps: pre-deploy backup → ff-only pull → build → migrate (via app start) → health → rollback image on failure.
# NEVER runs `down -v` on prod. Test with AGENTTRACER_COMPOSE_PROJECT=at-m2 and override files on :1985.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

COMPOSE_PROJECT="${AGENTTRACER_COMPOSE_PROJECT:-agenttracer}"
COMPOSE_FILES="${AGENTTRACER_COMPOSE_FILES:--f docker-compose.yml}"
# shellcheck disable=SC2086
compose() { docker compose -p "$COMPOSE_PROJECT" $COMPOSE_FILES "$@"; }

BRANCH="${AGENTTRACER_DEPLOY_BRANCH:-go-rewrite}"
HEALTH_URL="${AGENTTRACER_HEALTH_URL:-http://127.0.0.1:1984/health}"
HEALTH_RETRIES="${AGENTTRACER_HEALTH_RETRIES:-30}"
SKIP_PULL="${AGENTTRACER_SKIP_PULL:-0}"
SKIP_BACKUP="${AGENTTRACER_SKIP_BACKUP:-0}"
ALLOW_DOWN_V=0

for arg in "$@"; do
  case "$arg" in
    --skip-pull) SKIP_PULL=1 ;;
    --skip-backup) SKIP_BACKUP=1 ;;
    --allow-down-v) echo "ERROR: --allow-down-v refused; never down -v on deploy" >&2; exit 2 ;;
    *) echo "unknown arg: $arg" >&2; exit 2 ;;
  esac
done

echo "[deploy] project=$COMPOSE_PROJECT branch=$BRANCH health=$HEALTH_URL"

# Guard: refuse destructive compose args if somehow passed via COMPOSE_FILES misuse
if [[ "$*" == *"-v"* && "$*" == *"down"* ]]; then
  echo "ERROR: refusing down -v" >&2
  exit 2
fi

if [[ "$SKIP_BACKUP" != "1" ]]; then
  echo "[deploy] pre-deploy backup"
  AGENTTRACER_COMPOSE_PROJECT="$COMPOSE_PROJECT" \
    "$REPO_ROOT/deploy/backup.sh"
else
  echo "[deploy] skipping backup"
fi

PREV_IMAGE_ID="$(docker inspect "${COMPOSE_PROJECT}-app-1" -f '{{.Image}}' 2>/dev/null || true)"
PREV_CREATED="$(docker inspect "${COMPOSE_PROJECT}-app-1" -f '{{.Created}}' 2>/dev/null || true)"
echo "[deploy] previous app image=$PREV_IMAGE_ID created=$PREV_CREATED"

if [[ "$SKIP_PULL" != "1" ]]; then
  echo "[deploy] ff-only pull origin/$BRANCH"
  git fetch origin "$BRANCH"
  # Stay on current branch; only ff-merge the deploy branch tip into HEAD if we're on it,
  # otherwise checkout tracking branch with ff-only.
  cur="$(git rev-parse --abbrev-ref HEAD)"
  if [[ "$cur" == "$BRANCH" ]]; then
    git merge --ff-only "origin/$BRANCH"
  else
    echo "[deploy] WARN: HEAD is $cur, not $BRANCH; skipping checkout (set AGENTTRACER_SKIP_PULL=1 for test stacks)"
  fi
fi

echo "[deploy] build app"
# shellcheck disable=SC2086
compose build app

echo "[deploy] recreate app (no down -v)"
# shellcheck disable=SC2086
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
  echo "[deploy] HEALTH FAILED — rolling back app container to previous image" >&2
  if [[ -n "$PREV_IMAGE_ID" ]]; then
    # Tag rollback and force recreate from previous image id
    docker tag "$PREV_IMAGE_ID" "${COMPOSE_PROJECT}-app:rollback" || true
    # Force the running container back: stop current, run previous image with same config is hard
    # via compose; practical rollback = retag built image and up again from saved image.
    docker stop "${COMPOSE_PROJECT}-app-1" || true
    # Re-create using previous image by temporarily tagging it as the compose image name
    IMG_NAME="$(docker compose -p "$COMPOSE_PROJECT" $COMPOSE_FILES config --images app | head -1)"
    if [[ -n "$IMG_NAME" ]]; then
      docker tag "$PREV_IMAGE_ID" "$IMG_NAME"
      # shellcheck disable=SC2086
      compose up -d --no-deps --no-build app
    fi
    sleep 3
    if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
      echo "[deploy] rollback restored health" >&2
    else
      echo "[deploy] rollback attempted; health still failing — manual intervention needed" >&2
    fi
  else
    echo "[deploy] no previous image to roll back to" >&2
  fi
  exit 1
fi

echo "[deploy] success $(git rev-parse --short HEAD 2>/dev/null || true)"
# silence unused
: "$ALLOW_DOWN_V"
