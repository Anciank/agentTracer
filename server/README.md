# agentTracer Go server (M0/M1)

LangSmith-compatible ingest + waterfall UI.

```bash
# from repo root
docker compose up -d --build
# API key: secrets/api_key (chmod 600)
curl -H "x-api-key: $(cat secrets/api_key)" http://127.0.0.1:1984/info
```

Env: `AGENTTRACER_API_KEY` or `AGENTTRACER_API_KEY_FILE`, `AGENTTRACER_DATABASE_URL`, MinIO/S3 vars.

## Tests

DB-backed tests read **only** `AGENTTRACER_TEST_DATABASE_URL` (never `AGENTTRACER_DATABASE_URL`).
The database name must end in `_test` or `scratch` or the suite fatals. If the test var is unset, those tests skip.

```bash
# throwaway Postgres with a safe DB name
createdb agenttracer_test   # or POSTGRES_DB=agenttracer_test in docker
export AGENTTRACER_TEST_DATABASE_URL='postgres://agenttracer:agenttracer@127.0.0.1:5432/agenttracer_test?sslmode=disable'
go test ./...
```
