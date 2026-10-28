#!/usr/bin/env bash
# Read-only image digest + entrypoint preflight for agentTracer.
# Aborts (exit 1) if a local/running image does not match the compose pin or
# has the wrong entrypoint/cmd (guards against poisoned tags that retagged
# agenttracer as silo, etc.).
#
# Usage (prod inspect-only):
#   AGENTTRACER_COMPOSE_PROJECT=agenttracer ./deploy/preflight.sh
#
# Never pulls, never recreates, never writes. Safe against :1984.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

COMPOSE_PROJECT="${AGENTTRACER_COMPOSE_PROJECT:-agenttracer}"
COMPOSE_FILE_ARGS="${AGENTTRACER_COMPOSE_FILES:--f docker-compose.yml}"
# shellcheck disable=SC2086
compose() { docker compose -p "$COMPOSE_PROJECT" $COMPOSE_FILE_ARGS "$@"; }

# Canonical pins (must match docker-compose.yml / at-* overrides). Verified via Hub.
EXPECTED_SILO_DIGEST="sha256:7bb2aff6b89efe1cb1303c10fc21d579447434ed06649ac9ec05a5e2dbf30ad7"
EXPECTED_POSTGRES_DIGEST="sha256:0cefaabd882e42bcb2a2f3dc56d3ce4770560ba62fd7f197f1a2e008fd5731ab"
FORBIDDEN_APP_NAMES_REGEX='^(docker\.io/)?(pgsty/silo|postgres|library/postgres|minio/minio)(:|@|$)'

fail() { echo "[preflight] ERROR: $*" >&2; exit 1; }
info() { echo "[preflight] $*"; }

# Resolve image id for a compose service: prefer running container, else compose image ref.
service_image_ref() {
  local svc="$1"
  compose config --images "$svc" 2>/dev/null | head -1
}

service_cid() {
  local svc="$1"
  compose ps -q "$svc" 2>/dev/null || true
}

# Collect RepoDigests + Id for an image (by id, digest ref, or name:tag).
image_digests() {
  local ref="$1"
  docker image inspect "$ref" --format '{{range .RepoDigests}}{{.}}{{println}}{{end}}IMAGE_ID={{.Id}}' 2>/dev/null || true
}

entrypoint_of() {
  # $1 = image ref or container id; $2 = image|container
  local target="$1" kind="$2"
  if [[ "$kind" == "container" ]]; then
    docker inspect "$target" --format '{{json .Config.Entrypoint}} {{json .Config.Cmd}} Path={{.Path}}' 2>/dev/null || true
  else
    docker image inspect "$target" --format '{{json .Config.Entrypoint}} {{json .Config.Cmd}}' 2>/dev/null || true
  fi
}

digest_matches() {
  # $1 = inspect blob from image_digests; $2 = expected sha256:hex
  local blob="$1" want="$2"
  [[ -z "$blob" ]] && return 1
  # RepoDigest lines look like repo@sha256:...
  echo "$blob" | grep -F "@${want}" >/dev/null 2>&1 && return 0
  # Also accept Id equality if someone pinned by image id (rare)
  echo "$blob" | grep -F "IMAGE_ID=${want}" >/dev/null 2>&1 && return 0
  return 1
}

require_pin_digest() {
  local svc="$1" ref="$2" want="$3"
  [[ "$ref" == *"@sha256:"* ]] || fail "$svc image is not digest-pinned: '$ref' (want ...@${want})"
  local pinned="${ref##*@}"
  [[ "$pinned" == "$want" ]] || fail "$svc compose pin digest is '$pinned' but expected '$want'"
}

check_third_party() {
  local svc="$1" want_digest="$2" expect_ep_substr="$3" forbid_ep_substr="$4"
  local ref cid blob ep kind target

  ref="$(service_image_ref "$svc")"
  [[ -n "$ref" ]] || fail "could not resolve compose image for service '$svc'"
  info "$svc compose ref: $ref"
  require_pin_digest "$svc" "$ref" "$want_digest"

  cid="$(service_cid "$svc")"
  if [[ -n "$cid" ]]; then
    kind=container
    target="$cid"
    local img_id
    img_id="$(docker inspect "$cid" --format '{{.Image}}')"
    blob="$(image_digests "$img_id")"
    info "$svc running cid=${cid:0:12} image_id=${img_id:0:19}..."
  else
    kind=image
    # Try exact ref, then name without digest
    if docker image inspect "$ref" >/dev/null 2>&1; then
      target="$ref"
    else
      local nodigest="${ref%@*}"
      target="$nodigest"
    fi
    blob="$(image_digests "$target")"
    info "$svc no running container; checking local image '$target'"
  fi

  [[ -n "$blob" ]] || fail "$svc: local image not found for '$ref' — pull the pinned digest before deploy (preflight is read-only and will not pull)"

  digest_matches "$blob" "$want_digest" \
    || fail "$svc: local RepoDigests do not include @$want_digest — possible poisoned/wrong tag. Digests seen:
$blob"

  if [[ "$kind" == "container" ]]; then
    ep="$(entrypoint_of "$cid" container)"
  else
    ep="$(entrypoint_of "$target" image)"
  fi
  info "$svc entrypoint/cmd: $ep"

  if [[ -n "$forbid_ep_substr" ]] && echo "$ep" | grep -F "$forbid_ep_substr" >/dev/null 2>&1; then
    fail "$svc: entrypoint/cmd contains forbidden '$forbid_ep_substr' (poisoned image?). Got: $ep"
  fi
  if [[ -n "$expect_ep_substr" ]] && ! echo "$ep" | grep -F "$expect_ep_substr" >/dev/null 2>&1; then
    fail "$svc: entrypoint/cmd missing expected '$expect_ep_substr'. Got: $ep"
  fi
  info "$svc OK"
}

check_app() {
  local ref cid ep img_name
  ref="$(service_image_ref app)"
  [[ -n "$ref" ]] || fail "could not resolve compose image for service 'app'"
  info "app compose ref: $ref"

  # Must not share a third-party repository name
  img_name="${ref%%@*}"
  img_name="${img_name##*/}"  # rough; also check full
  if echo "$ref" | grep -E "$FORBIDDEN_APP_NAMES_REGEX" >/dev/null 2>&1; then
    fail "app image ref '$ref' collides with a third-party repository name — set image: agenttracer-app:local"
  fi
  case "$ref" in
    agenttracer-app:*|agenttracer-app@*|*/agenttracer-app:*|*/agenttracer-app@*) ;;
    *)
      # Allow project-prefixed compose defaults only if explicitly agenttracer-app
      if [[ "$ref" != *"agenttracer-app"* ]]; then
        fail "app image must be named agenttracer-app:* (got '$ref') so builds cannot retag silo/postgres"
      fi
      ;;
  esac

  cid="$(service_cid app)"
  if [[ -n "$cid" ]]; then
    ep="$(entrypoint_of "$cid" container)"
    info "app running cid=${cid:0:12} entrypoint/cmd: $ep"
  else
    if docker image inspect "$ref" >/dev/null 2>&1; then
      ep="$(entrypoint_of "$ref" image)"
      info "app local image entrypoint/cmd: $ep"
    else
      info "app: no running container and no local image yet (OK before first build)"
      return 0
    fi
  fi

  echo "$ep" | grep -F 'agenttracer' >/dev/null 2>&1 \
    || fail "app: entrypoint/cmd must include 'agenttracer'. Got: $ep"
  echo "$ep" | grep -Fi 'silo' >/dev/null 2>&1 \
    && fail "app: entrypoint/cmd looks like silo. Got: $ep"
  # Path=/usr/bin/docker-entrypoint.sh would mean wrong image
  echo "$ep" | grep -F 'docker-entrypoint' >/dev/null 2>&1 \
    && fail "app: entrypoint looks like postgres/silo docker-entrypoint. Got: $ep"
  info "app OK"
}

info "project=$COMPOSE_PROJECT files=$COMPOSE_FILE_ARGS (read-only)"

check_third_party postgres "$EXPECTED_POSTGRES_DIGEST" "docker-entrypoint.sh" "agenttracer"
# silo: expect docker-entrypoint (pgsty/silo), forbid agenttracer binary
check_third_party minio "$EXPECTED_SILO_DIGEST" "docker-entrypoint" "agenttracer"
check_app

info "all checks passed"
