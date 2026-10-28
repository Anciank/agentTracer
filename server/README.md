# agentTracer Go server (M0/M1)

LangSmith-compatible ingest + waterfall UI.

```bash
# from repo root
docker compose up -d --build
# API key: secrets/api_key (chmod 600)
curl -H "x-api-key: $(cat secrets/api_key)" http://127.0.0.1:1984/info
```

Env: `AGENTTRACER_API_KEY` or `AGENTTRACER_API_KEY_FILE`, `AGENTTRACER_DATABASE_URL`, MinIO/S3 vars.
