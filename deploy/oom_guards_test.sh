#!/usr/bin/env bash
# Unit tests for oom_guards.sh / deploy.sh check-only paths.
# Stubs: DEPLOY_STUB_COMPOSE_LS, DEPLOY_MEMINFO_PATH, DEPLOY_LOCK_FILE, DEPLOY_STUB_SS_LTN.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=oom_guards.sh
source "$ROOT/deploy/oom_guards.sh"

PASS=0
FAIL=0
assert_eq() {
  local name="$1" got="$2" want="$3"
  if [[ "$got" == "$want" ]]; then
    echo "PASS: $name"
    PASS=$((PASS + 1))
  else
    echo "FAIL: $name (got=$got want=$want)" >&2
    FAIL=$((FAIL + 1))
  fi
}
assert_ok() {
  local name="$1"; shift
  if "$@" >/tmp/oom-test-out.txt 2>/tmp/oom-test-err.txt; then
    echo "PASS: $name"
    PASS=$((PASS + 1))
  else
    echo "FAIL: $name (expected ok, rc=$?)" >&2
    cat /tmp/oom-test-err.txt >&2 || true
    FAIL=$((FAIL + 1))
  fi
}
assert_fail() {
  local name="$1"; shift
  if "$@" >/tmp/oom-test-out.txt 2>/tmp/oom-test-err.txt; then
    echo "FAIL: $name (expected non-zero)" >&2
    cat /tmp/oom-test-out.txt /tmp/oom-test-err.txt >&2 || true
    FAIL=$((FAIL + 1))
  else
    echo "PASS: $name"
    PASS=$((PASS + 1))
  fi
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- refuse-on-test-stack ---
printf 'agenttracer\nat-m2\n' >"$TMP/compose_ls_at"
DEPLOY_STUB_COMPOSE_LS="$TMP/compose_ls_at"
assert_fail "refuse when at-m2 up" deploy_check_no_at_test_stacks 0
grep -q 'at-m2' /tmp/oom-test-err.txt
assert_eq "refuse message mentions at-m2" "$?" "0"

# --- force flag allows test stack ---
assert_ok "force-with-test-stack allows at-m2" deploy_check_no_at_test_stacks 1
grep -q 'force-with-test-stack' /tmp/oom-test-err.txt
assert_eq "force warns" "$?" "0"

# --- clean compose ls ---
printf 'agenttracer\n' >"$TMP/compose_ls_clean"
DEPLOY_STUB_COMPOSE_LS="$TMP/compose_ls_clean"
assert_ok "ok when only agenttracer up" deploy_check_no_at_test_stacks 0

# --- low memory ---
# 500 MiB available = 500*1024 kB
printf 'MemTotal:       1900000 kB\nMemAvailable:    %d kB\n' $((500 * 1024)) >"$TMP/meminfo_low"
DEPLOY_MEMINFO_PATH="$TMP/meminfo_low"
DEPLOY_MIN_AVAIL_MB=900
assert_fail "abort on low MemAvailable" deploy_check_mem_available
grep -q 'MemAvailable=500MiB' /tmp/oom-test-out.txt || grep -q 'MemAvailable=500MiB' /tmp/oom-test-err.txt
assert_eq "low-mem prints value" "$?" "0"

# --- enough memory ---
printf 'MemTotal:       1900000 kB\nMemAvailable:    %d kB\n' $((1200 * 1024)) >"$TMP/meminfo_ok"
DEPLOY_MEMINFO_PATH="$TMP/meminfo_ok"
DEPLOY_MIN_AVAIL_MB=900
assert_ok "ok when MemAvailable above threshold" deploy_check_mem_available
grep -q 'DEPLOY_MIN_AVAIL_MB=900' /tmp/oom-test-out.txt
assert_eq "prints threshold" "$?" "0"

# --- lock held ---
DEPLOY_LOCK_FILE="$TMP/deploy.lock"
DEPLOY_LOCK_FD=200
# Holder subprocess keeps flock until we kill it.
(
  eval "exec 200>\"$DEPLOY_LOCK_FILE\""
  flock -n 200
  printf 'holder\n' >"$DEPLOY_LOCK_FILE"
  # signal ready
  touch "$TMP/holder_ready"
  sleep 30
) &
HOLDER_PID=$!
for i in $(seq 1 50); do
  [[ -f "$TMP/holder_ready" ]] && break
  sleep 0.05
done
assert_ok "lock_is_held detects holder" deploy_lock_is_held
assert_fail "acquire fails while held" deploy_acquire_deploy_lock
kill "$HOLDER_PID" 2>/dev/null || true
wait "$HOLDER_PID" 2>/dev/null || true
# After holder exits, lock should be free
sleep 0.1
if deploy_lock_is_held; then
  # stale file without holder — flock should succeed (not held)
  echo "FAIL: lock still reported held after holder exit" >&2
  FAIL=$((FAIL + 1))
else
  echo "PASS: lock free after holder exit"
  PASS=$((PASS + 1))
fi
assert_ok "acquire after holder exit" deploy_acquire_deploy_lock
deploy_release_deploy_lock

# --- slot_check :1985 busy ---
printf 'LISTEN 0 4096 0.0.0.0:1985 0.0.0.0:*\n' >"$TMP/ss_busy"
DEPLOY_STUB_SS_LTN="$TMP/ss_busy"
assert_fail "1985 busy fails" deploy_check_1985_free
printf 'LISTEN 0 4096 0.0.0.0:1984 0.0.0.0:*\n' >"$TMP/ss_free"
DEPLOY_STUB_SS_LTN="$TMP/ss_free"
assert_ok "1985 free ok" deploy_check_1985_free

# --- deploy.sh --check-only with stubs (no real docker/build) ---
printf 'agenttracer\nat-lgtest\n' >"$TMP/compose_ls_at2"
printf 'MemAvailable:    %d kB\n' $((1500 * 1024)) >"$TMP/meminfo_hi"
set +e
DEPLOY_STUB_COMPOSE_LS="$TMP/compose_ls_at2" \
  DEPLOY_MEMINFO_PATH="$TMP/meminfo_hi" \
  DEPLOY_MIN_AVAIL_MB=900 \
  DEPLOY_LOCK_FILE="$TMP/checkonly.lock" \
  "$ROOT/deploy/deploy.sh" --check-only >/tmp/oom-checkonly-out.txt 2>/tmp/oom-checkonly-err.txt
rc=$?
set -e
assert_eq "deploy --check-only refuses at-lgtest" "$rc" "1"
grep -q 'at-lgtest' /tmp/oom-checkonly-err.txt
assert_eq "check-only names at-lgtest" "$?" "0"

set +e
DEPLOY_STUB_COMPOSE_LS="$TMP/compose_ls_clean" \
  DEPLOY_MEMINFO_PATH="$TMP/meminfo_hi" \
  DEPLOY_MIN_AVAIL_MB=900 \
  DEPLOY_LOCK_FILE="$TMP/checkonly2.lock" \
  "$ROOT/deploy/deploy.sh" --check-only --force-with-test-stack >/tmp/oom-checkonly2-out.txt 2>/tmp/oom-checkonly2-err.txt
rc=$?
set -e
# clean list + force → should pass even without at-*
assert_eq "deploy --check-only clean passes" "$rc" "0"

set +e
DEPLOY_STUB_COMPOSE_LS="$TMP/compose_ls_at2" \
  DEPLOY_MEMINFO_PATH="$TMP/meminfo_hi" \
  DEPLOY_MIN_AVAIL_MB=900 \
  DEPLOY_LOCK_FILE="$TMP/checkonly3.lock" \
  "$ROOT/deploy/deploy.sh" --check-only --force-with-test-stack >/tmp/oom-checkonly3-out.txt 2>/tmp/oom-checkonly3-err.txt
rc=$?
set -e
assert_eq "deploy --check-only --force allows at-*" "$rc" "0"

set +e
DEPLOY_STUB_COMPOSE_LS="$TMP/compose_ls_clean" \
  DEPLOY_MEMINFO_PATH="$TMP/meminfo_low" \
  DEPLOY_MIN_AVAIL_MB=900 \
  DEPLOY_LOCK_FILE="$TMP/checkonly4.lock" \
  "$ROOT/deploy/deploy.sh" --check-only >/tmp/oom-checkonly4-out.txt 2>/tmp/oom-checkonly4-err.txt
rc=$?
set -e
assert_eq "deploy --check-only fails low mem" "$rc" "1"

echo "----"
echo "passed=$PASS failed=$FAIL"
[[ "$FAIL" -eq 0 ]]
