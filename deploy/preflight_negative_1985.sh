#!/usr/bin/env bash
# NEGATIVE demo for :1985 only — shows preflight aborting when a LOCAL throwaway
# tag is poisoned (agenttracer binary tagged as if it were silo). NEVER retags
# real silo/postgres names (pgsty/silo:RELEASE..., postgres:16-alpine, etc.).
#
# Ready-to-run after CodeBro frees :1985 and this branch is on the VPS:
#
#   cd /path/to/agentTracer-ops-pin   # worktree / clone at ops-pin tip
#   AGENTTRACER_TAKEOVER_1985=1 ./deploy/preflight_negative_1985.sh
#
# Flow:
#   1. docker tag agenttracer-app:local → agenttracer-preflight-poison:test
#   2. Throwaway compose override points minio at that poison tag while claiming
#      the real silo digest in the pin (simulates a poisoned local cache).
#   3. preflight.sh against project at-preflight-neg must exit non-zero.
#   4. Cleanup removes throwaway tag + override. Never touches :1984 / agenttracer.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

if [[ "${AGENTTRACER_TAKEOVER_1985:-}" != "1" ]]; then
  echo "Refusing: set AGENTTRACER_TAKEOVER_1985=1 after :1985 handoff (test slot only)." >&2
  exit 2
fi

POISON_TAG="agenttracer-preflight-poison:test"
PROJECT="at-preflight-neg"
OVERRIDE="/tmp/${PROJECT}-compose.override.yml"
# Claim the real silo digest while the local tag content is agenttracer (poison).
REAL_SILO_DIGEST="sha256:7bb2aff6b89efe1cb1303c10fc21d579447434ed06649ac9ec05a5e2dbf30ad7"

cleanup() {
  rm -f "$OVERRIDE"
  docker image rm -f "$POISON_TAG" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if docker image inspect agenttracer-app:local >/dev/null 2>&1; then
  docker tag agenttracer-app:local "$POISON_TAG"
elif docker image inspect agenttracer-app:latest >/dev/null 2>&1; then
  docker tag agenttracer-app:latest "$POISON_TAG"
else
  echo "Need local agenttracer-app:local (or :latest) to build the throwaway poison tag; compose build app first." >&2
  exit 1
fi

# Minimal compose so we do not inherit prod bind mounts / ports from the main file
# for this negative-only project. Postgres/app pins are correct; only minio is poisoned.
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
