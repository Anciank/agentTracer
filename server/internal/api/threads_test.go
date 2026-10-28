package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAPIUIThreads(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	proj := "api-threads-" + uuid.New().String()[:8]
	now := time.Now().UTC()
	r1 := uuid.New()
	r2 := uuid.New()

	mustPost := func(body map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/runs", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code >= 300 {
			t.Fatalf("post: %d %s", rr.Code, rr.Body.String())
		}
	}
	mustPost(map[string]any{
		"id": r1.String(), "trace_id": r1.String(), "name": "LangGraph", "run_type": "chain",
		"session_name": proj,
		"start_time":   now.Add(-2 * time.Minute).Format(time.RFC3339Nano),
		"end_time":     now.Add(-90 * time.Second).Format(time.RFC3339Nano),
		"status":       "error", "error": "old fail",
		"extra": map[string]any{"metadata": map[string]any{"thread_id": "api-th-1"}},
	})
	mustPost(map[string]any{
		"id": r2.String(), "trace_id": r2.String(), "name": "LangGraph", "run_type": "chain",
		"session_name": proj,
		"start_time":   now.Add(-30 * time.Second).Format(time.RFC3339Nano),
		"end_time":     now.Format(time.RFC3339Nano),
		"status":       "success",
		"extra":        map[string]any{"metadata": map[string]any{"thread_id": "api-th-1"}},
	})

	req := httptest.NewRequest("GET", "/api/ui/threads?project="+proj, nil)
	req.Header.Set("X-API-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("list status=%d body=%s", rr.Code, rr.Body.String())
	}
	var listResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	threads, _ := listResp["threads"].([]any)
	if len(threads) != 1 {
		t.Fatalf("threads=%v", listResp)
	}
	th := threads[0].(map[string]any)
	if th["thread_id"] != "api-th-1" || th["status"] != "success" || th["error_count"] != float64(1) {
		t.Fatalf("thread item=%v", th)
	}
	if th["thread_key_source"] != "thread_id" {
		t.Fatalf("key_source=%v", th["thread_key_source"])
	}
	if th["turn_count"] != float64(2) {
		t.Fatalf("turn_count=%v", th["turn_count"])
	}

	req = httptest.NewRequest("GET", "/api/ui/threads/api-th-1?project="+proj, nil)
	req.Header.Set("X-API-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("detail status=%d body=%s", rr.Code, rr.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["thread_id"] != "api-th-1" {
		t.Fatalf("detail=%v", detail)
	}
	traces, _ := detail["traces"].([]any)
	if len(traces) != 2 {
		t.Fatalf("detail traces=%v", detail)
	}
	if traces[0].(map[string]any)["trace_id"] != r1.String() {
		t.Fatalf("want oldest first, got %v", traces[0])
	}
}

func TestAPIUIThreadsHideUnthreaded(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	proj := "api-threads-hide-" + uuid.New().String()[:8]
	now := time.Now().UTC()
	u := uuid.New()
	b, _ := json.Marshal(map[string]any{
		"id": u.String(), "trace_id": u.String(), "name": "chain", "run_type": "chain",
		"session_name": proj,
		"start_time":   now.Format(time.RFC3339Nano),
		"end_time":     now.Add(time.Second).Format(time.RFC3339Nano),
		"status":       "success",
	})
	req := httptest.NewRequest("POST", "/runs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code >= 300 {
		t.Fatalf("post: %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/ui/threads?project="+proj, nil)
	req.Header.Set("X-API-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var listResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &listResp)
	threads, _ := listResp["threads"].([]any)
	if len(threads) != 0 {
		t.Fatalf("unthreaded must be hidden, got %v", listResp)
	}
}
