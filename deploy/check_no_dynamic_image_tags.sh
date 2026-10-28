#!/usr/bin/env bash
# Fails if deploy scripts use `compose config --images` (Compose 2.40.x ignores the
# service filter; order is nondeterministic; head -1 can be silo → poisoned retag).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
bad=0
while IFS= read -r f; do
  if sed 's/#.*//' "$f" | grep -nE 'config[[:space:]]+--images' >/dev/null 2>&1; then
    echo "FAIL: $f uses 'config --images':" >&2
    grep -nE 'config[[:space:]]+--images' "$f" >&2 || true
    bad=1
  fi
done < <(find "$ROOT" -maxdepth 1 -type f \( -name '*.sh' -o -name '*.bash' \) ! -name 'check_no_dynamic_image_tags.sh' | sort)
if [[ "$bad" -ne 0 ]]; then
  exit 1
fi
echo "OK: no deploy script uses config --images"
