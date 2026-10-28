#!/usr/bin/env bash
# Restore a gzipped pg_dump into the at-m2 scratch stack (:1985) and print row counts.
# Usage: AGENTTRACER_DUMP=~/backups/drill/prod-for-drill-....sql.gz ./deploy/restore_drill.sh
# Assumes at-m2 stack is already up. Does NOT touch prod.
set -euo pipefail
DUMP="${AGENTTRACER_DUMP:?set AGENTTRACER_DUMP to a .sql.gz}"
PG_CONTAINER="${AGENTTRACER_PG_CONTAINER:-at-m2-postgres}"
PG_USER="${AGENTTRACER_PG_USER:-agenttracer}"
PG_DB="${AGENTTRACER_PG_DB:-agenttracer}"

echo "[drill] restoring $DUMP into $PG_CONTAINER"
docker exec -i "$PG_CONTAINER" psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 \
  -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
gunzip -c "$DUMP" | docker exec -i "$PG_CONTAINER" psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 >/tmp/restore_drill.log
echo "[drill] counts:"
docker exec "$PG_CONTAINER" psql -U "$PG_USER" -d "$PG_DB" -t -A -c \
  "SELECT tablename||'='||n FROM (
     SELECT 'sessions' tablename, count(*)::text n FROM sessions
     UNION ALL SELECT 'runs', count(*)::text FROM runs
     UNION ALL SELECT 'feedback', count(*)::text FROM feedback
     UNION ALL SELECT 'attachments', count(*)::text FROM attachments
   ) s ORDER BY 1"
