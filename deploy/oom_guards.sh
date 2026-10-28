#!/usr/bin/env bash
# Shared OOM / test-stack / deploy-lock guards for deploy.sh and slot_check.sh.
# Sourced (not executed). Test overrides:
#   DEPLOY_LOCK_FILE, DEPLOY_MIN_AVAIL_MB, DEPLOY_MEMINFO_PATH,
#   DEPLOY_STUB_COMPOSE_LS (file whose contents replace `docker compose ls -q`),
#   DEPLOY_STUB_SS_LTN (file whose contents replace `ss -ltn` for :1985 checks).
# shellcheck shell=bash

DEPLOY_LOCK_FILE="${DEPLOY_LOCK_FILE:-/tmp/agenttracer-deploy.lock}"
DEPLOY_MIN_AVAIL_MB="${DEPLOY_MIN_AVAIL_MB:-900}"
DEPLOY_MEMINFO_PATH="${DEPLOY_MEMINFO_PATH:-/proc/meminfo}"
# FD used by flock; keep out of common stdin/out/err range.
DEPLOY_LOCK_FD="${DEPLOY_LOCK_FD:-200}"

# List running compose project names (one per line). Overridable for tests.
deploy_compose_ls_projects() {
  if [[ -n "${DEPLOY_STUB_COMPOSE_LS:-}" ]]; then
    cat "$DEPLOY_STUB_COMPOSE_LS"
    return 0
  fi
  docker compose ls -q 2>/dev/null || true
}

# Print at-* project names that are currently up (one per line).
deploy_list_at_projects() {
  local p
  while IFS= read -r p; do
    [[ -z "$p" ]] && continue
    case "$p" in
      at-*) printf '%s\n' "$p" ;;
    esac
  done < <(deploy_compose_ls_projects)
}

# Refuse when any at-* compose project is up, unless force=1.
# Usage: deploy_check_no_at_test_stacks <force 0|1>
deploy_check_no_at_test_stacks() {
  local force="${1:-0}"
  local bad=()
  local p
  while IFS= read -r p; do
    [[ -z "$p" ]] && continue
    bad+=("$p")
  done < <(deploy_list_at_projects)

  if ((${#bad[@]} > 0)); then
    if [[ "$force" == "1" ]]; then
      echo "[deploy] WARN: test stack(s) up (${bad[*]}); continuing due to --force-with-test-stack" >&2
      return 0
    fi
    echo "ERROR: refuse deploy while at-* test stack(s) are up: ${bad[*]}" >&2
    echo "ERROR: tear them down first, or pass --force-with-test-stack (OOM risk on this VPS)." >&2
    return 1
  fi
  echo "[deploy] no at-* test stacks running"
  return 0
}

# Abort if MemAvailable (MiB) is below DEPLOY_MIN_AVAIL_MB (default 900).
deploy_check_mem_available() {
  local min="${DEPLOY_MIN_AVAIL_MB:-900}"
  local path="${DEPLOY_MEMINFO_PATH:-/proc/meminfo}"
  local avail_kb avail_mb

  if [[ ! -r "$path" ]]; then
    echo "ERROR: cannot read meminfo at $path" >&2
    return 1
  fi
  avail_kb="$(awk '/^MemAvailable:/{print $2; exit}' "$path")"
  if [[ -z "$avail_kb" || ! "$avail_kb" =~ ^[0-9]+$ ]]; then
    echo "ERROR: MemAvailable missing or non-numeric in $path" >&2
    return 1
  fi
  avail_mb=$((avail_kb / 1024))
  echo "[deploy] MemAvailable=${avail_mb}MiB (threshold DEPLOY_MIN_AVAIL_MB=${min})"
  if (( avail_mb < min )); then
    echo "ERROR: MemAvailable ${avail_mb}MiB < ${min}MiB — abort before build (go build peaked ~1.06GiB above baseline)." >&2
    return 1
  fi
  return 0
}

# Non-blocking exclusive lock on DEPLOY_LOCK_FILE via flock on FD DEPLOY_LOCK_FD.
# Caller must keep the FD open; pair with deploy_release_deploy_lock on EXIT.
deploy_acquire_deploy_lock() {
  local lock="${DEPLOY_LOCK_FILE}"
  # Open/create lock file on the dedicated FD.
  eval "exec ${DEPLOY_LOCK_FD}>\"\$lock\""
  if ! flock -n "$DEPLOY_LOCK_FD"; then
    echo "ERROR: deploy lock held: $lock (another deploy.sh is running?)" >&2
    eval "exec ${DEPLOY_LOCK_FD}>&-"
    return 1
  fi
  # Record pid for humans inspecting the lock file.
  printf '%s\n' "$$" >"$lock" 2>/dev/null || true
  echo "[deploy] acquired lock $lock (fd=$DEPLOY_LOCK_FD)"
  return 0
}

deploy_release_deploy_lock() {
  local lock="${DEPLOY_LOCK_FILE}"
  # Unlock then close FD; remove the lock file if we still own the name.
  flock -u "$DEPLOY_LOCK_FD" 2>/dev/null || true
  eval "exec ${DEPLOY_LOCK_FD}>&-" 2>/dev/null || true
  rm -f "$lock" 2>/dev/null || true
}

# True (exit 0) if another process holds the deploy lock.
deploy_lock_is_held() {
  local lock="${DEPLOY_LOCK_FILE}"
  local fd="${DEPLOY_LOCK_FD}"
  # Use a different FD so we don't clobber an already-held lock in the same shell.
  local probe_fd=201
  eval "exec ${probe_fd}>\"\$lock\""
  if flock -n "$probe_fd"; then
    flock -u "$probe_fd" 2>/dev/null || true
    eval "exec ${probe_fd}>&-"
    return 1  # not held
  fi
  eval "exec ${probe_fd}>&-" 2>/dev/null || true
  return 0  # held
}

# ss -ltn (or stub file) for port checks.
deploy_ss_ltn() {
  if [[ -n "${DEPLOY_STUB_SS_LTN:-}" ]]; then
    cat "$DEPLOY_STUB_SS_LTN"
    return 0
  fi
  ss -ltn 2>/dev/null || netstat -ltn 2>/dev/null || true
}

# Fail if TCP :1985 is already listening (another at-* stack).
deploy_check_1985_free() {
  if deploy_ss_ltn | grep -E ':1985\s' >/dev/null 2>&1; then
    echo "ERROR: :1985 is already in use — another at-* stack (or listener) holds the port." >&2
    echo "ERROR: run slot_check after the owner tears down; do not start a second test stack." >&2
    return 1
  fi
  echo "[slot] :1985 is free"
  return 0
}

# Run all read-only deploy pre-checks (no backup/build). force=0|1 for at-* gate.
# Exits non-zero on failure; prints status either way.
deploy_run_check_only() {
  local force="${1:-0}"
  echo "[deploy] --check-only: test-stack + memory (lock probe, no hold)"
  if deploy_lock_is_held; then
    echo "[deploy] WARN: deploy lock currently held: $DEPLOY_LOCK_FILE"
  else
    echo "[deploy] deploy lock is free: $DEPLOY_LOCK_FILE"
  fi
  deploy_check_no_at_test_stacks "$force" || return 1
  deploy_check_mem_available || return 1
  echo "[deploy] --check-only OK"
  return 0
}
