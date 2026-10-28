#!/usr/bin/env bash
# Post-up identity check: app runs agenttracer; minio/silo does not; app is not silo.
# Exit 0 on OK, 1 on mismatch. Read-only (inspect only).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

COMPOSE_PROJECT="${AGENTTRACER_COMPOSE_PROJECT:-agenttracer}"
COMPOSE_FILE_ARGS="${AGENTTRACER_COMPOSE_FILES:--f docker-compose.yml}"
# shellcheck disable=SC2086
compose() { docker compose -p "$COMPOSE_PROJECT" $COMPOSE_FILE_ARGS "$@"; }

fail() { echo "[startup_check] ERROR: $*" >&2; exit 1; }
info() { echo "[startup_check] $*"; }

cid_of() { compose ps -q "$1" 2>/dev/null || true; }

inspect_ep() {
  docker inspect "$1" --format 'Path={{.Path}} Entrypoint={{json .Config.Entrypoint}} Cmd={{json .Config.Cmd}} Image={{.Config.Image}}' 2>/dev/null
}

APP_CID="$(cid_of app)"
MINIO_CID="$(cid_of minio)"
[[ -n "$APP_CID" ]] || fail "app container not running"
[[ -n "$MINIO_CID" ]] || fail "minio/silo container not running"

APP_EP="$(inspect_ep "$APP_CID")"
MINIO_EP="$(inspect_ep "$MINIO_CID")"
info "app:   $APP_EP"
info "minio: $MINIO_EP"

echo "$APP_EP" | grep -F 'agenttracer' >/dev/null 2>&1 \
  || fail "app container is not running the agenttracer binary: $APP_EP"
echo "$APP_EP" | grep -Fi 'silo' >/dev/null 2>&1 \
  && fail "app container looks like silo: $APP_EP"

echo "$MINIO_EP" | grep -F 'agenttracer' >/dev/null 2>&1 \
  && fail "minio/silo container is running agenttracer (poisoned image): $MINIO_EP"
# Positive: silo/minio should be entrypoint or server path, not our Go binary
if echo "$MINIO_EP" | grep -E 'docker-entrypoint|silo|/usr/bin/docker-entrypoint' >/dev/null 2>&1; then
  info "minio/silo entrypoint looks legitimate"
else
  # soft: Path=server is also ok for some minio variants; still forbid agenttracer (above)
  info "minio/silo entrypoint did not match docker-entrypoint/silo pattern; agenttracer still absent — continuing"
fi

info "OK"
