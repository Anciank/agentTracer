#!/usr/bin/env bash
# :1985 slot gate — bots MUST run this before taking the test port.
# Fails if:
#   1) /tmp/agenttracer-deploy.lock is held (prod deploy in progress), or
#   2) another at-* stack (or any listener) already holds :1985.
# Also prints the never-prune / never-retag-prod-:local reminder (OPS.md).
# See OPS.md "Deploy lock & :1985 slot" and "Image store / rollback protection".
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=oom_guards.sh
source "$REPO_ROOT/deploy/oom_guards.sh"
# shellcheck source=deploy_helpers.sh
source "$REPO_ROOT/deploy/deploy_helpers.sh"

echo "[slot] checking deploy lock + :1985 availability"

if deploy_lock_is_held; then
  echo "ERROR: deploy lock held ($DEPLOY_LOCK_FILE) — wait for prod deploy to finish." >&2
  exit 1
fi
echo "[slot] deploy lock is free"

deploy_check_1985_free

# Also warn (but do not fail solely on this) if at-* projects are listed —
# the decisive signal for the slot is :1985 itself.
at_list="$(deploy_list_at_projects | tr '\n' ' ')"
if [[ -n "${at_list// }" ]]; then
  echo "[slot] note: at-* projects currently listed: $at_list"
fi

# Image-store hygiene reminder (b1fee2c4 incident 2026-10-28): never prune -a /
# rmi -f agenttracer images; don't retag prod :local while that digest still runs.
deploy_warn_dangerous_prune_docs

echo "[slot] OK — :1985 is yours to take (still respect the queue / handoff)."
exit 0
