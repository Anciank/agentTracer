#!/usr/bin/env bash
# Helpers for deploy.sh: --sha resolution and rollback-image pinning.
# Sourced (not executed). Test overrides:
#   DEPLOY_STUB_GIT_DIR  — if set, git commands run with -C "$DEPLOY_STUB_GIT_DIR"
#   DEPLOY_STUB_DOCKER   — if set to a script/binary, used instead of docker
# shellcheck shell=bash

deploy_git() {
  if [[ -n "${DEPLOY_STUB_GIT_DIR:-}" ]]; then
    git -C "$DEPLOY_STUB_GIT_DIR" "$@"
  else
    git "$@"
  fi
}

deploy_docker() {
  if [[ -n "${DEPLOY_STUB_DOCKER:-}" ]]; then
    "$DEPLOY_STUB_DOCKER" "$@"
  else
    docker "$@"
  fi
}

# Resolve and check out DEPLOY_SHA on BRANCH.
# Requires: DEPLOY_SHA non-empty, BRANCH set (e.g. go-rewrite).
# Verifies the commit is an ancestor of (or equal to) origin/$BRANCH, then
# checks out BRANCH and reset --hard to that commit (exact pin; may move
# HEAD backward relative to a prior tip checkout).
deploy_checkout_sha() {
  local sha="$1"
  local branch="$2"
  local tip target

  if [[ -z "$sha" || -z "$branch" ]]; then
    echo "ERROR: deploy_checkout_sha requires <sha> <branch>" >&2
    return 2
  fi

  echo "[deploy] --sha: fetch origin/$branch and pin to $sha"
  deploy_git fetch origin "$branch"

  if ! target="$(deploy_git rev-parse --verify "${sha}^{commit}" 2>/dev/null)"; then
    echo "ERROR: --sha '$sha' is not a commit object (fetch origin/$branch first / check the hash)" >&2
    return 2
  fi
  tip="$(deploy_git rev-parse --verify "origin/${branch}")"

  if ! deploy_git merge-base --is-ancestor "$target" "$tip"; then
    echo "ERROR: --sha $target is not an ancestor of origin/$branch ($tip)" >&2
    echo "ERROR: refuse to deploy a commit that is not on $branch" >&2
    return 2
  fi

  local cur
  cur="$(deploy_git rev-parse --abbrev-ref HEAD)"
  if [[ "$cur" != "$branch" ]]; then
    echo "[deploy] checking out $branch (was $cur)"
    deploy_git checkout "$branch"
  fi

  local head
  head="$(deploy_git rev-parse HEAD)"
  if [[ "$head" == "$target" ]]; then
    echo "[deploy] already at $target"
    return 0
  fi

  echo "[deploy] reset --hard to $target (was $head)"
  deploy_git reset --hard "$target"
  echo "[deploy] HEAD=$(deploy_git rev-parse --short HEAD)"
  return 0
}

# Default tip pull: ff-only merge origin/BRANCH when already on BRANCH.
deploy_ff_pull_branch() {
  local branch="$1"
  echo "[deploy] ff-only pull origin/$branch"
  deploy_git fetch origin "$branch"
  local cur
  cur="$(deploy_git rev-parse --abbrev-ref HEAD)"
  if [[ "$cur" == "$branch" ]]; then
    deploy_git merge --ff-only "origin/$branch"
  else
    echo "[deploy] WARN: HEAD is $cur, not $branch; not switching branches (use --sha or SKIP_PULL for test stacks)"
  fi
}

# Pin PREV_IMAGE_ID to ROLLBACK_TAG and ensure it carries agenttracer.keep=true
# so a documented label-filter prune will not delete it. Also refuses if the
# previous image id is missing from the image store (retag/prune race).
deploy_pin_rollback_image() {
  local prev_id="$1"
  local rollback_tag="$2"

  if [[ -z "$prev_id" ]]; then
    echo "[deploy] no previous app image to pin as rollback"
    return 0
  fi
  if [[ -z "$rollback_tag" ]]; then
    echo "ERROR: deploy_pin_rollback_image requires rollback tag" >&2
    return 2
  fi

  if ! deploy_docker image inspect "$prev_id" >/dev/null 2>&1; then
    echo "ERROR: previous image $prev_id is missing from the image store" >&2
    echo "ERROR: a concurrent build likely moved :local, or prune/rmi removed it while still referenced." >&2
    echo "ERROR: refuse deploy — restore via docker commit/export of the running app, or retag a known-good image first." >&2
    return 1
  fi

  # Always keep a named tag so plain `docker image prune` (no -a) cannot drop it.
  deploy_docker tag "$prev_id" "$rollback_tag"
  echo "[deploy] pinned rollback tag $rollback_tag -> $prev_id"

  # Re-tag through a no-op FROM build to attach keep label without changing layers' files.
  # Skip when the image already has the label (idempotent re-pin).
  local has_keep
  has_keep="$(deploy_docker image inspect "$rollback_tag" --format '{{index .Config.Labels "agenttracer.keep"}}' 2>/dev/null || true)"
  if [[ "$has_keep" == "true" ]]; then
    echo "[deploy] rollback image already labeled agenttracer.keep=true"
    return 0
  fi

  if [[ "${DEPLOY_SKIP_ROLLBACK_LABEL:-0}" == "1" ]]; then
    echo "[deploy] WARN: DEPLOY_SKIP_ROLLBACK_LABEL=1 — tagged but not labeled"
    return 0
  fi

  echo "[deploy] labeling $rollback_tag with agenttracer.keep=true"
  # shellcheck disable=SC2086
  if ! printf 'FROM %s\nLABEL agenttracer.keep=true\nLABEL agenttracer.role=rollback-prev\n' "$rollback_tag" \
    | deploy_docker build -t "$rollback_tag" -; then
    echo "ERROR: failed to label rollback image $rollback_tag" >&2
    return 1
  fi
  echo "[deploy] rollback keep-label applied on $rollback_tag"
  return 0
}

# Warn/refuse if a dangerous prune pattern is requested via env (bots/scripts).
# Slot check calls the warn mode; deploy can refuse when DEPLOY_REFUSE_PRUNE_HINT=1.
deploy_warn_dangerous_prune_docs() {
  echo "[slot] NEVER run: docker image prune -a | docker system prune -a | docker rmi -f on agenttracer-*"
  echo "[slot] Safe reclaim (optional): docker image prune -a --filter 'label!=agenttracer.keep=true'"
  echo "[slot] Do not rebuild/retag agenttracer-app:local on the VPS while prod is running that digest."
}
