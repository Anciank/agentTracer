package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"

	"github.com/Anciank/agentTracer/server/internal/config"
	"github.com/Anciank/agentTracer/server/internal/dbtest"
	"github.com/Anciank/agentTracer/server/internal/store"
	"github.com/Anciank/agentTracer/server/migrations"
)

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	dsn := dbtest.TestDatabaseURL(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// isolate: wipe tables
	_, _ = pool.Exec(ctx, `TRUNCATE attachments, feedback, runs, sessions, trace_graph_cache CASCADE`)
	key := "test-key-" + uuid.NewString()
	cfg := config.Config{APIKey: key, CORSOrigins: []string{"*"}}
	st := store.New(pool, store.NewMemoryBlob())
	return New(cfg, st), key
}

func encodeMP(parts []struct {
	name, ct string
	data     []byte
}) []byte {
	const b = "boundary123"
	var out bytes.Buffer
	for _, p := range parts {
		fmt.Fprintf(&out, "--%s\r\n", b)
		fmt.Fprintf(&out, "Content-Disposition: form-data; name=\"%s\"\r\n", p.name)
		fmt.Fprintf(&out, "Content-Type: %s\r\n\r\n", p.ct)
		out.Write(p.data)
		out.WriteString("\r\n")
	}
	fmt.Fprintf(&out, "--%s--\r\n", b)
	return out.Bytes()
}

func TestMultipartIngestAndUI(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	runID := uuid.NewString()
	traceID := runID
	childID := uuid.NewString()
	main, _ := json.Marshal(map[string]any{
		"id": runID, "trace_id": traceID, "name": "root", "run_type": "chain",
		"session_name": "compat-proj", "start_time": time.Now().UTC().Format(time.RFC3339Nano),
		"dotted_order": "20260101T000000000000Z" + runID[:8],
	})
	child, _ := json.Marshal(map[string]any{
		"id": childID, "trace_id": traceID, "parent_run_id": runID, "name": "child", "run_type": "llm",
		"session_name": "compat-proj", "start_time": time.Now().UTC().Format(time.RFC3339Nano),
		"dotted_order": "20260101T000000000000Z" + runID[:8] + ".20260101T000000100000Z" + childID[:8],
	})
	patch, _ := json.Marshal(map[string]any{
		"id": runID, "outputs": map[string]any{"ok": true}, "end_time": time.Now().UTC().Format(time.RFC3339Nano),
		"name": nil,
	})
	blob := []byte{0, 1, 2, 3, 255}
	body := encodeMP([]struct {
		name, ct string
		data     []byte
	}{
		{"post." + runID, "application/json", main},
		{"post." + childID, "application/json", child},
		{"patch." + runID, "application/json", patch},
		{"attachment." + runID + ".chart", "image/png", blob},
	})

	req := httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
	req.Header.Set("x-api-key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("multipart %d %s", rr.Code, rr.Body.String())
	}

	// gzip body
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(body)
	_ = zw.Close()
	req = httptest.NewRequest("POST", "/runs/multipart", &gz)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("gzip %d %s", rr.Code, rr.Body.String())
	}

	// zstd
	enc, _ := zstd.NewWriter(nil)
	zst := enc.EncodeAll(body, nil)
	_ = enc.Close()
	req = httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(zst))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
	req.Header.Set("Content-Encoding", "zstd")
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("zstd %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/ui/traces", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("traces %d", rr.Code)
	}
	var summaries []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &summaries)
	if len(summaries) < 1 {
		t.Fatalf("no summaries: %s", rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/ui/traces/"+traceID, nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("detail %d %s", rr.Code, rr.Body.String())
	}
	var detail map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &detail)
	rows, _ := detail["rows"].([]any)
	if len(rows) < 2 {
		t.Fatalf("rows=%v", detail)
	}

	// attachments landed
	atts, err := srv.store.ListAttachments(context.Background(), runID)
	if err != nil || len(atts) < 1 {
		t.Fatalf("atts=%v err=%v", atts, err)
	}

	// auth required
	req = httptest.NewRequest("GET", "/info", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("expected 401 got %d", rr.Code)
	}
	req = httptest.NewRequest("GET", "/info", nil)
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("info %d", rr.Code)
	}
}

func TestBatchEndpoint(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	id := uuid.NewString()
	payload := map[string]any{
		"post": []map[string]any{{
			"id": id, "trace_id": id, "name": "batch", "run_type": "chain",
			"session_name": "batch-proj", "start_time": time.Now().UTC().Format(time.RFC3339),
		}},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/runs/batch", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 202 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	run, err := srv.store.GetRun(context.Background(), id)
	if err != nil || run["name"] != "batch" {
		t.Fatalf("%v %v", run, err)
	}
	_ = io.Discard
}

func TestWireQueryFeedbackSessionsHealth(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	id := uuid.NewString()
	payload := map[string]any{
		"post": []map[string]any{{
			"id": id, "trace_id": id, "name": "qroot", "run_type": "chain",
			"session_name": "q-proj", "start_time": time.Now().UTC().Format(time.RFC3339Nano),
			"extra": map[string]any{"metadata": map[string]any{
				"ls_model_name": "gpt-4o-mini",
				"usage":         map[string]any{"input_tokens": 3, "output_tokens": 2},
			}},
		}},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/runs/batch", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 202 {
		t.Fatalf("batch %d %s", rr.Code, rr.Body.String())
	}

	qbody, _ := json.Marshal(map[string]any{"trace": id, "limit": 10})
	req = httptest.NewRequest("POST", "/runs/query", bytes.NewReader(qbody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("query %d %s", rr.Code, rr.Body.String())
	}
	var qres map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &qres)
	if _, ok := qres["cursors"].(map[string]any); !ok {
		t.Fatalf("missing cursors: %v", qres)
	}
	runs, _ := qres["runs"].([]any)
	if len(runs) < 1 {
		t.Fatalf("no runs: %v", qres)
	}

	fbID := uuid.NewString()
	fb, _ := json.Marshal(map[string]any{"id": fbID, "run_id": id, "key": "thumbs", "score": 1.0, "comment": "nice"})
	req = httptest.NewRequest("POST", "/feedback", bytes.NewReader(fb))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("feedback %d %s", rr.Code, rr.Body.String())
	}
	var fbOut map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &fbOut)
	if fbOut["key"] != "thumbs" || fbOut["created_at"] == nil {
		t.Fatalf("fb shape %v", fbOut)
	}

	req = httptest.NewRequest("GET", "/feedback?run_id="+id, nil)
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("list fb %d", rr.Code)
	}

	req = httptest.NewRequest("GET", "/health", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var health map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &health)
	for _, leak := range []string{"runs", "sessions", "feedback", "attachments"} {
		if _, ok := health[leak]; ok {
			t.Fatalf("health leak %s", leak)
		}
	}

	req = httptest.NewRequest("GET", "/api/ui/projects", nil)
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("ui projects %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/ui/traces?project=q-proj", nil)
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("ui traces %d %s", rr.Code, rr.Body.String())
	}
	var tr map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &tr)
	if _, ok := tr["traces"]; !ok {
		t.Fatalf("shape %v", tr)
	}

	req = httptest.NewRequest("GET", "/api/ui/traces/"+id+"/summary", nil)
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("summary %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/ui/runs/"+id+"/messages", nil)
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("messages %d", rr.Code)
	}

	req = httptest.NewRequest("GET", "/api/ui/stats?project=q-proj", nil)
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("stats %d %s", rr.Code, rr.Body.String())
	}
	var stats map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &stats)
	if _, ok := stats["cost_usd"]; ok {
		t.Fatal("cost leaked")
	}
	if _, ok := stats["tokens"]; !ok {
		t.Fatalf("no tokens: %v", stats)
	}

	// PATCH null must not wipe name
	patch, _ := json.Marshal(map[string]any{"name": nil, "outputs": map[string]any{"done": true}})
	req = httptest.NewRequest("PATCH", "/runs/"+id, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 202 {
		t.Fatalf("patch %d", rr.Code)
	}
	run, err := srv.store.GetRun(context.Background(), id)
	if err != nil || run["name"] != "qroot" {
		t.Fatalf("null wipe? %v %v", run, err)
	}
	if run["model"] != "gpt-4o-mini" {
		t.Fatalf("model not extracted: %v", run["model"])
	}
}
