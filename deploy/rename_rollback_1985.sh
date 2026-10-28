#!/usr/bin/env bash
# :1985 dry-run for the first prod deploy image rename:
#   old compose default name (at-m2-app) → agenttracer-app:local
# Exercises deploy.sh rollback-prev pin + post-up identity failure → restore.
#
# Called from preflight_negative_1985.sh after the poison-tag case.
# NEVER touches compose project agenttracer / :1984.
# NEVER retags real silo/postgres names.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

if [[ "${AGENTTRACER_TAKEOVER_1985:-}" != "1" ]]; then
  echo "Refusing: set AGENTTRACER_TAKEOVER_1985=1 after :1985 handoff." >&2
  exit 2
fi

COMPOSE_PROJECT=at-m2
COMPOSE_FILES="-f docker-compose.yml -f deploy/docker-compose.at-m2.yml"
OLD_IMAGE_OVERRIDE="/tmp/at-m2-old-app-image.yml"
HEALTH_URL="${AGENTTRACER_HEALTH_URL:-http://127.0.0.1:1985/health}"
OLD_APP_IMAGE="at-m2-app" # pre-ops-pin style: project-namespaced, not agenttracer-app:local

# shellcheck disable=SC2086
compose() { docker compose -p "$COMPOSE_PROJECT" $COMPOSE_FILES "$@"; }
compose_old() {
  # shellcheck disable=SC2086
  docker compose -p "$COMPOSE_PROJECT" $COMPOSE_FILES -f "$OLD_IMAGE_OVERRIDE" "$@"
}

cleanup() {
  echo "[rename-rb] cleanup: down -v OUR at-m2 only" >&2
  compose down -v 2>/dev/null || docker compose -p at-m2 down -v 2>/dev/null || true
  rm -f "$OLD_IMAGE_OVERRIDE"
  docker image rm -f "${COMPOSE_PROJECT}-app:rollback-prev" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "[rename-rb] === image rename → rollback dry-run (project=$COMPOSE_PROJECT) ==="

# Refuse accidental prod
if [[ "$COMPOSE_PROJECT" == "agenttracer" ]]; then
  echo "ERROR: refusing prod project name" >&2
  exit 2
fi

# Take over :1985 if bound
if docker ps --format '{{.Names}} {{.Ports}}' | grep -q '0.0.0.0:1985'; then
  OWNER="$(docker ps --format '{{.Names}} {{.Ports}}' | grep '0.0.0.0:1985' || true)"
  echo "[rename-rb] takeover: releasing :1985 ($OWNER)"
  compose down -v || docker compose -p at-m2 down -v || true
fi

mkdir -p secrets
if [[ ! -f secrets/api_key ]]; then
  if [[ -f "$HOME/src/agentTracer/secrets/api_key" ]]; then
    cp "$HOME/src/agentTracer/secrets/api_key" secrets/api_key
    chmod 600 secrets/api_key
  else
    echo "test-key-ops-1985-rename-rb" > secrets/api_key
    chmod 600 secrets/api_key
  fi
fi

# --- Phase A: stack running under OLD app image name ---
cat > "$OLD_IMAGE_OVERRIDE" <<YAML
# Temporary override: simulate pre-ops-pin image name (project-namespaced).
services:
  app:
    image: ${OLD_APP_IMAGE}
YAML

echo "[rename-rb] phase A: build+up with old image name '$OLD_APP_IMAGE'"
compose_old pull postgres minio
compose_old build app
compose_old up -d

ok=0
for i in $(seq 1 60); do
  if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
    ok=1
    echo "[rename-rb] phase A healthy after ${i}s"
    break
  fi
  sleep 1
done
[[ "$ok" == "1" ]] || { echo "ERROR: phase A health failed" >&2; compose_old logs app 2>&1 | tail -30 >&2; exit 1; }

APP_CID="$(compose_old ps -q app)"
OLD_IMAGE_ID="$(docker inspect "$APP_CID" -f '{{.Image}}')"
OLD_CONFIG_IMAGE="$(docker inspect "$APP_CID" -f '{{.Config.Image}}')"
echo "[rename-rb] phase A running image_id=$OLD_IMAGE_ID config_image=$OLD_CONFIG_IMAGE"
[[ "$OLD_CONFIG_IMAGE" == "$OLD_APP_IMAGE" || "$OLD_CONFIG_IMAGE" == "$OLD_APP_IMAGE:"* ]] \
  || echo "[rename-rb] WARN: expected Config.Image ~ $OLD_APP_IMAGE, got $OLD_CONFIG_IMAGE"

# --- Phase B: deploy.sh renames to agenttracer-app:local, then forced mismatch → rollback ---
echo "[rename-rb] phase B: deploy.sh with AGENTTRACER_FORCE_STARTUP_MISMATCH=1 (expect rollback)"
set +e
AGENTTRACER_COMPOSE_PROJECT="$COMPOSE_PROJECT" \
  AGENTTRACER_COMPOSE_FILES="$COMPOSE_FILES" \
  AGENTTRACER_HEALTH_URL="$HEALTH_URL" \
  AGENTTRACER_ROLLBACK_HEALTH_URL="$HEALTH_URL" \
  AGENTTRACER_FORCE_STARTUP_MISMATCH=1 \
  "$REPO_ROOT/deploy/deploy.sh" --skip-pull --skip-backup
deploy_rc=$?
set -e

if [[ "$deploy_rc" -eq 0 ]]; then
  echo "[rename-rb] UNEXPECTED: deploy.sh succeeded; expected rollback exit 1" >&2
  exit 1
fi
echo "[rename-rb] deploy.sh exited $deploy_rc (expected non-zero after forced mismatch)"

# --- Verify rollback restored old image content + health ---
sleep 2
if ! curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
  echo "[rename-rb] ERROR: health not restored after rollback" >&2
  exit 1
fi
echo "[rename-rb] health OK after rollback"

NEW_CID="$(compose ps -q app)"
[[ -n "$NEW_CID" ]] || { echo "[rename-rb] ERROR: app container missing after rollback" >&2; exit 1; }
NEW_IMAGE_ID="$(docker inspect "$NEW_CID" -f '{{.Image}}')"
NEW_CONFIG_IMAGE="$(docker inspect "$NEW_CID" -f '{{.Config.Image}}')"
echo "[rename-rb] after rollback image_id=$NEW_IMAGE_ID config_image=$NEW_CONFIG_IMAGE"

if [[ "$NEW_IMAGE_ID" != "$OLD_IMAGE_ID" ]]; then
  echo "[rename-rb] ERROR: image id after rollback ($NEW_IMAGE_ID) != phase A old id ($OLD_IMAGE_ID)" >&2
  exit 1
fi

# Compose pin is now agenttracer-app:local; rollback retags old content onto that name.
case "$NEW_CONFIG_IMAGE" in
  agenttracer-app:local|agenttracer-app:*)
    echo "[rename-rb] Config.Image is new-style name with old content — rename+rollback path OK"
    ;;
  *)
    echo "[rename-rb] WARN: Config.Image=$NEW_CONFIG_IMAGE (still OK if image id matched)"
    ;;
esac

# Confirm rollback-prev was created during deploy
if docker image inspect "${COMPOSE_PROJECT}-app:rollback-prev" >/dev/null 2>&1; then
  echo "[rename-rb] rollback-prev tag present"
else
  echo "[rename-rb] WARN: ${COMPOSE_PROJECT}-app:rollback-prev not found after deploy (may have been unused if PREV used by id)"
fi

echo "[rename-rb] OK — old image restored and healthy after forced identity mismatch"
