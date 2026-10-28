#!/usr/bin/env bash
# Nightly (or pre-deploy) backup: gzip pg_dump + silo/minio bucket mirror.
# Layout under ~/backups (override with AGENTTRACER_BACKUP_ROOT):
#   postgres/daily/agenttracer-YYYYMMDD-HHMMSS.sql.gz
#   postgres/weekly/agenttracer-YYYYMMDD.sql.gz  (Sunday copies)
#   silo/YYYYMMDD-HHMMSS/…  (mc mirror)
# Retention: 7 daily dumps, 4 weekly dumps. Silo mirrors: keep last 7.
set -euo pipefail

ROOT="${AGENTTRACER_BACKUP_ROOT:-$HOME/backups}"
COMPOSE_PROJECT="${AGENTTRACER_COMPOSE_PROJECT:-agenttracer}"
PG_CONTAINER="${AGENTTRACER_PG_CONTAINER:-${COMPOSE_PROJECT}-postgres-1}"
MINIO_CONTAINER="${AGENTTRACER_MINIO_CONTAINER:-${COMPOSE_PROJECT}-minio-1}"
PG_USER="${AGENTTRACER_PG_USER:-agenttracer}"
PG_DB="${AGENTTRACER_PG_DB:-agenttracer}"
S3_BUCKET="${AGENTTRACER_S3_BUCKET:-agenttracer}"
S3_ACCESS="${AGENTTRACER_S3_ACCESS_KEY:-minioadmin}"
S3_SECRET="${AGENTTRACER_S3_SECRET_KEY:-minioadmin}"
STAMP="$(date -u +%Y%m%d-%H%M%S)"
DAY="$(date -u +%Y%m%d)"
DOW="$(date -u +%u)" # 1=Mon … 7=Sun

mkdir -p "$ROOT/postgres/daily" "$ROOT/postgres/weekly" "$ROOT/silo" "$ROOT/logs"
LOG="$ROOT/logs/backup-$STAMP.log"
exec > >(tee -a "$LOG") 2>&1

echo "[backup] start $STAMP project=$COMPOSE_PROJECT pg=$PG_CONTAINER"

if ! docker inspect "$PG_CONTAINER" >/dev/null 2>&1; then
  echo "[backup] ERROR: postgres container $PG_CONTAINER not found" >&2
  exit 1
fi

DUMP="$ROOT/postgres/daily/agenttracer-$STAMP.sql.gz"
docker exec "$PG_CONTAINER" pg_dump -U "$PG_USER" -d "$PG_DB" --no-owner --no-acl | gzip -c > "$DUMP.tmp"
mv "$DUMP.tmp" "$DUMP"
echo "[backup] pg_dump -> $DUMP ($(wc -c < "$DUMP") bytes)"

if [[ "$DOW" == "7" ]]; then
  WEEKLY="$ROOT/postgres/weekly/agenttracer-$DAY.sql.gz"
  cp -f "$DUMP" "$WEEKLY"
  echo "[backup] weekly copy -> $WEEKLY"
fi

prune_keep() {
  # $1=dir glob pattern, $2=keep count
  local dir="$1" keep="$2"
  mapfile -t files < <(find "$dir" -maxdepth 1 -type f -name 'agenttracer-*.sql.gz' -printf '%T@ %p\n' 2>/dev/null | sort -rn | awk '{print $2}')
  local i=0
  for f in "${files[@]:-}"; do
    [[ -z "$f" ]] && continue
    i=$((i + 1))
    if (( i > keep )); then
      echo "[backup] prune $f"
      rm -f "$f"
    fi
  done
}

prune_keep "$ROOT/postgres/daily" 7
prune_keep "$ROOT/postgres/weekly" 4

# Silo / MinIO mirror via ephemeral mc client on the docker network
SILO_DEST="$ROOT/silo/$STAMP"
mkdir -p "$SILO_DEST"
if docker inspect "$MINIO_CONTAINER" >/dev/null 2>&1; then
  NET="$(docker inspect "$MINIO_CONTAINER" -f '{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}' | awk '{print $1}')"
  MINIO_IP="$(docker inspect "$MINIO_CONTAINER" -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' | awk '{print $1}')"
  if [[ -n "${NET:-}" && -n "${MINIO_IP:-}" ]]; then
    echo "[backup] mirroring s3://$S3_BUCKET from $MINIO_IP via network $NET (aws-cli)"
    # amazon/aws-cli is already cached on the VPS; minio/mc is not pullable here.
    if ! docker run --rm --network "$NET" \
      -e AWS_ACCESS_KEY_ID="$S3_ACCESS" \
      -e AWS_SECRET_ACCESS_KEY="$S3_SECRET" \
      -e AWS_DEFAULT_REGION=us-east-1 \
      -v "$SILO_DEST:/out" \
      amazon/aws-cli:2.15.0 \
      s3 sync "s3://${S3_BUCKET}" /out --endpoint-url "http://${MINIO_IP}:9000"; then
      echo "[backup] WARN: silo mirror failed (bucket empty or aws-cli issue); continuing"
    fi
  else
    echo "[backup] WARN: could not resolve minio network/ip; skipped silo mirror"
  fi
else
  echo "[backup] WARN: minio container $MINIO_CONTAINER not found; skipped silo mirror"
fi

# Retention: keep last 7 silo stamp dirs
mapfile -t sdirs < <(find "$ROOT/silo" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' 2>/dev/null | sort -rn | awk '{print $2}')
si=0
for d in "${sdirs[@]:-}"; do
  [[ -z "$d" ]] && continue
  si=$((si + 1))
  if (( si > 7 )); then
    echo "[backup] prune silo $d"
    rm -rf "$d"
  fi
done

echo "[backup] done $STAMP"
echo "$DUMP"
