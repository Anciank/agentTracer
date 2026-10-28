#!/usr/bin/env bash
# :1985 negative suite (after CodeBro/TestingBro handoff). Two cases:
#   1) Poison-tag preflight abort (throwaway tag only — never real silo/postgres).
#   2) Image rename → rollback dry-run (old at-m2-app name → agenttracer-at-m2:local,
#      forced post-up identity mismatch, verify rollback restores old image + health).
#
# Ready-to-run (single command):
#
#   cd /path/to/agentTracer-ops-pin   # worktree / clone at ops-pin tip
#   AGENTTRACER_TAKEOVER_1985=1 ./deploy/preflight_negative_1985.sh
#
# NEVER touches compose project agenttracer / :1984.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

if [[ "${AGENTTRACER_TAKEOVER_1985:-}" != "1" ]]; then
  echo "Refusing: set AGENTTRACER_TAKEOVER_1985=1 after :1985 handoff (test slot only)." >&2
  exit 2
fi

echo "========== [1985-neg] case 1/2: poison-tag preflight =========="

POISON_TAG="agenttracer-preflight-poison:test"
PROJECT="at-preflight-neg"
OVERRIDE="/tmp/${PROJECT}-compose.override.yml"
REAL_SILO_DIGEST="sha256:7bb2aff6b89efe1cb1303c10fc21d579447434ed06649ac9ec05a5e2dbf30ad7"

cleanup_poison() {
  rm -f "$OVERRIDE"
  docker image rm -f "$POISON_TAG" >/dev/null 2>&1 || true
}
trap cleanup_poison EXIT

# Prefer an existing app image for the throwaway poison tag. If none, build
# agenttracer-app:local once (does not start :1985).
if docker image inspect agenttracer-at-m2:local >/dev/null 2>&1; then
  docker tag agenttracer-at-m2:local "$POISON_TAG"
elif docker image inspect agenttracer-app:local >/dev/null 2>&1; then
  docker tag agenttracer-app:local "$POISON_TAG"
elif docker image inspect agenttracer-app:latest >/dev/null 2>&1; then
  docker tag agenttracer-app:latest "$POISON_TAG"
elif docker image inspect at-m2-app >/dev/null 2>&1; then
  docker tag at-m2-app "$POISON_TAG"
else
  echo "[negative] building agenttracer-app:local for poison tag source"
  docker compose -p at-preflight-build -f docker-compose.yml build app
  docker tag agenttracer-app:local "$POISON_TAG"
fi

cat > "$OVERRIDE" <<YAML
services:
  postgres:
    image: postgres:16-alpine@sha256:0cefaabd882e42bcb2a2f3dc56d3ce4770560ba62fd7f197f1a2e008fd5731ab
  minio:
    # Throwaway name only — NOT pgsty/silo. Claims real digest; local content is agenttracer.
    image: agenttracer-preflight-poison:test@${REAL_SILO_DIGEST}
  app:
    image: agenttracer-app:local
YAML

echo "[negative] running preflight against throwaway project $PROJECT (expect FAIL)"
set +e
AGENTTRACER_COMPOSE_PROJECT="$PROJECT" \
  AGENTTRACER_COMPOSE_FILES="-f $OVERRIDE" \
  "$REPO_ROOT/deploy/preflight.sh"
rc=$?
set -e

if [[ "$rc" -eq 0 ]]; then
  echo "[negative] UNEXPECTED PASS — preflight should have aborted on poison tag" >&2
  exit 1
fi
echo "[negative] OK — preflight aborted as expected (rc=$rc)"

cleanup_poison
trap - EXIT

echo "========== [1985-neg] case 2/2: rename → rollback dry-run =========="
AGENTTRACER_TAKEOVER_1985=1 "$REPO_ROOT/deploy/rename_rollback_1985.sh"

echo "========== [1985-neg] all cases passed =========="
