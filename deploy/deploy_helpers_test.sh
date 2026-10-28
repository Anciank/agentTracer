#!/usr/bin/env bash
# Unit tests for deploy_helpers.sh (--sha resolution + rollback pin).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=deploy_helpers.sh
source "$ROOT/deploy/deploy_helpers.sh"

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
  if "$@" >/tmp/dh-test-out.txt 2>/tmp/dh-test-err.txt; then
    echo "PASS: $name"
    PASS=$((PASS + 1))
  else
    echo "FAIL: $name (expected ok, rc=$?)" >&2
    cat /tmp/dh-test-err.txt >&2 || true
    FAIL=$((FAIL + 1))
  fi
}
assert_fail() {
  local name="$1"; shift
  if "$@" >/tmp/dh-test-out.txt 2>/tmp/dh-test-err.txt; then
    echo "FAIL: $name (expected non-zero)" >&2
    cat /tmp/dh-test-out.txt /tmp/dh-test-err.txt >&2 || true
    FAIL=$((FAIL + 1))
  else
    echo "PASS: $name"
    PASS=$((PASS + 1))
  fi
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Bare upstream stays at tip C; work clone can reset without moving origin.
BARE="$TMP/upstream.git"
git init -q --bare "$BARE"
WORK="$TMP/seed"
git clone -q "$BARE" "$WORK"
git -C "$WORK" checkout -q -b go-rewrite
git -C "$WORK" config user.email "ops@test"
git -C "$WORK" config user.name "ops-test"
echo one >"$WORK/f"
git -C "$WORK" add f
git -C "$WORK" commit -q -m A
A="$(git -C "$WORK" rev-parse HEAD)"
echo two >"$WORK/f"
git -C "$WORK" commit -q -am B
B="$(git -C "$WORK" rev-parse HEAD)"
echo three >"$WORK/f"
git -C "$WORK" commit -q -am C
C="$(git -C "$WORK" rev-parse HEAD)"
git -C "$WORK" push -q origin go-rewrite

# Side commit D not on go-rewrite
git -C "$WORK" checkout -q -b side "$A"
echo side >"$WORK/f"
git -C "$WORK" commit -q -am D
D="$(git -C "$WORK" rev-parse HEAD)"
git -C "$WORK" push -q origin side
git -C "$WORK" checkout -q go-rewrite

# Worktree under test (fresh clone of bare)
REPO="$TMP/repo"
git clone -q "$BARE" "$REPO"
git -C "$REPO" checkout -q go-rewrite
git -C "$REPO" config user.email "ops@test"
git -C "$REPO" config user.name "ops-test"

export DEPLOY_STUB_GIT_DIR="$REPO"

assert_ok "checkout ancestor sha B" deploy_checkout_sha "$B" go-rewrite
assert_eq "HEAD is B" "$(git -C "$REPO" rev-parse HEAD)" "$B"

assert_ok "checkout tip sha C" deploy_checkout_sha "$C" go-rewrite
assert_eq "HEAD is C" "$(git -C "$REPO" rev-parse HEAD)" "$C"

assert_fail "refuse non-ancestor D" deploy_checkout_sha "$D" go-rewrite
grep -q 'not an ancestor' /tmp/dh-test-err.txt
assert_eq "refuse message" "$?" "0"

assert_fail "refuse garbage sha" deploy_checkout_sha "deadbeefdeadbeef" go-rewrite

assert_ok "short sha" deploy_checkout_sha "${B:0:7}" go-rewrite
assert_eq "short -> B" "$(git -C "$REPO" rev-parse HEAD)" "$B"

assert_ok "ff pull to tip" deploy_ff_pull_branch go-rewrite
assert_eq "ff at C" "$(git -C "$REPO" rev-parse HEAD)" "$C"

# --- docker stub ---
STUB="$TMP/docker_stub.sh"
cat >"$STUB" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
S="${DEPLOY_DOCKER_STATE:?}"
mkdir -p "$S/images" "$S/tags" "$S/keep"
resolve_id() {
  local ref="$1"
  if [[ -f "$S/images/$ref" ]]; then echo "$ref"; return 0; fi
  if [[ -f "$S/tags/$ref" ]]; then cat "$S/tags/$ref"; return 0; fi
  return 1
}
cmd="${1:-}"; shift || true
case "$cmd" in
  image)
    sub="${1:-}"; shift || true
    case "$sub" in
      inspect)
        format=""
        ref=""
        while [[ $# -gt 0 ]]; do
          case "$1" in
            --format) format="$2"; shift 2 ;;
            *) ref="$1"; shift ;;
          esac
        done
        id="$(resolve_id "$ref")" || { echo "Error: No such image: $ref" >&2; exit 1; }
        if [[ -n "$format" && "$format" == *agenttracer.keep* ]]; then
          if [[ -f "$S/keep/$id" ]]; then cat "$S/keep/$id"; else echo ""; fi
        else
          echo "$id"
        fi
        ;;
      *) echo "stub image $sub NYI" >&2; exit 99 ;;
    esac
    ;;
  tag)
    src="$1"; dst="$2"
    id="$(resolve_id "$src")" || { echo "Error: No such image: $src" >&2; exit 1; }
    echo "$id" >"$S/tags/$dst"
    echo "[stub] tagged $id -> $dst"
    ;;
  build)
    tag=""
    while [[ $# -gt 0 ]]; do
      case "$1" in -t) tag="$2"; shift 2 ;; -) shift ;; *) shift ;; esac
    done
    dockerfile="$(cat)"
    from="$(printf '%s\n' "$dockerfile" | awk '/^FROM /{print $2; exit}')"
    src_id="$(resolve_id "$from")" || { echo "Error: No such image: $from" >&2; exit 1; }
    new_id="labeled-${src_id}"
    echo "$new_id" >"$S/images/$new_id"
    echo true >"$S/keep/$new_id"
    echo "$new_id" >"$S/tags/$tag"
    echo "[stub] build labeled $tag -> $new_id"
    ;;
  *) echo "stub unsupported: $cmd $*" >&2; exit 99 ;;
esac
STUB
chmod +x "$STUB"
export DEPLOY_STUB_DOCKER="$STUB"
export DEPLOY_DOCKER_STATE="$TMP/docker"
mkdir -p "$DEPLOY_DOCKER_STATE/images"
echo "sha-prev" >"$DEPLOY_DOCKER_STATE/images/sha-prev"

assert_ok "pin existing image" deploy_pin_rollback_image "sha-prev" "agenttracer-app:rollback-prev"
if [[ -f "$DEPLOY_DOCKER_STATE/tags/agenttracer-app:rollback-prev" ]]; then tag_rc=0; else tag_rc=1; fi
assert_eq "tag file exists" "$tag_rc" "0"
keep="$(DEPLOY_STUB_DOCKER="$STUB" DEPLOY_DOCKER_STATE="$DEPLOY_DOCKER_STATE" \
  "$STUB" image inspect agenttracer-app:rollback-prev --format '{{index .Config.Labels "agenttracer.keep"}}')"
assert_eq "keep label true" "$keep" "true"

assert_fail "pin missing image" deploy_pin_rollback_image "sha-missing" "agenttracer-app:rollback-prev"
grep -q 'missing from the image store' /tmp/dh-test-err.txt
assert_eq "missing message" "$?" "0"

assert_ok "pin empty prev is noop" deploy_pin_rollback_image "" "agenttracer-app:rollback-prev"

# Arg parse smoke
set +e
"$ROOT/deploy/deploy.sh" --sha >/tmp/dh-sha-missing.txt 2>&1
rc=$?
set -e
assert_eq "deploy --sha without value fails" "$rc" "2"
grep -q 'requires a commit' /tmp/dh-sha-missing.txt
assert_eq "deploy --sha missing msg" "$?" "0"

set +e
"$ROOT/deploy/deploy.sh" --sha= >/tmp/dh-sha-empty.txt 2>&1
rc=$?
set -e
assert_eq "deploy --sha= empty fails" "$rc" "2"

# --check-only still works with helpers sourced
printf 'agenttracer\n' >"$TMP/compose_ls_clean"
printf 'MemAvailable:    %d kB\n' $((1500 * 1024)) >"$TMP/meminfo_hi"
# shellcheck source=oom_guards.sh
source "$ROOT/deploy/oom_guards.sh"
set +e
DEPLOY_STUB_COMPOSE_LS="$TMP/compose_ls_clean" \
  DEPLOY_MEMINFO_PATH="$TMP/meminfo_hi" \
  DEPLOY_MIN_AVAIL_MB=900 \
  DEPLOY_LOCK_FILE="$TMP/check.lock" \
  "$ROOT/deploy/deploy.sh" --check-only >/tmp/dh-check.txt 2>/tmp/dh-check.err
rc=$?
set -e
assert_eq "deploy --check-only still ok" "$rc" "0"

echo "----"
echo "passed=$PASS failed=$FAIL"
[[ "$FAIL" -eq 0 ]]
