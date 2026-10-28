package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAPIMonitorETagAndShape(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()

	proj := "m36-api-mon"
	now := time.Now().UTC()
	root := uuid.New()
	llm := uuid.New()
	tool := uuid.New()
	end := now.Add(-5 * time.Minute)
	start := now.Add(-10 * time.Minute)

	mustPost := func(body map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/runs", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code >= 300 {
			t.Fatalf("post %v: %d %s", body["name"], rr.Code, rr.Body.String())
		}
	}
	mustPatch := func(id string, body map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("PATCH", "/runs/"+id, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code >= 300 {
			t.Fatalf("patch: %d %s", rr.Code, rr.Body.String())
		}
	}

	mustPost(map[string]any{
		"id": root.String(), "trace_id": root.String(), "name": "root", "run_type": "chain",
		"session_name": proj, "start_time": start.Format(time.RFC3339Nano),
	})
	mustPost(map[string]any{
		"id": llm.String(), "trace_id": root.String(), "parent_run_id": root.String(),
		"name": "chat", "run_type": "llm", "model": "gpt-4o-mini", "session_name": proj,
		"start_time":   start.Add(time.Second).Format(time.RFC3339Nano),
		"end_time":     end.Format(time.RFC3339Nano),
		"input_tokens": 10, "output_tokens": 5, "total_tokens": 15,
	})
	mustPost(map[string]any{
		"id": tool.String(), "trace_id": root.String(), "parent_run_id": root.String(),
		"name": "web_search", "run_type": "tool", "session_name": proj,
		"start_time": start.Add(2 * time.Second).Format(time.RFC3339Nano),
		"end_time":   end.Format(time.RFC3339Nano),
	})
	mustPatch(root.String(), map[string]any{
		"end_time": end.Format(time.RFC3339Nano), "status": "success",
	})

	req := httptest.NewRequest("GET", "/api/ui/projects/"+proj+"/monitor?window=24h", nil)
	req.Header.Set("X-API-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("monitor: %d %s", rr.Code, rr.Body.String())
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	if rr.Header().Get("Cache-Control") == "" {
		t.Fatal("missing Cache-Control")
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["cost"] != nil {
		t.Fatalf("cost %v", body["cost"])
	}
	if body["window"] != "24h" || body["bucket"] != "1h" {
		t.Fatalf("window/bucket %v %v", body["window"], body["bucket"])
	}
	series, ok := body["series_traces"].([]any)
	if !ok || len(series) != 24 {
		t.Fatalf("series len %v ok=%v", len(series), ok)
	}

	req2 := httptest.NewRequest("GET", "/api/ui/projects/"+proj+"/monitor?window=24h", nil)
	req2.Header.Set("X-API-Key", key)
	req2.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("want 304 got %d body=%s", rr2.Code, rr2.Body.String())
	}

	req3 := httptest.NewRequest("GET", "/api/ui/stats?project="+proj, nil)
	req3.Header.Set("X-API-Key", key)
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, req3)
	if rr3.Code != 200 {
		t.Fatalf("stats: %d", rr3.Code)
	}

	req4 := httptest.NewRequest("GET", "/api/ui/traces?project="+proj+"&tool=web_search", nil)
	req4.Header.Set("X-API-Key", key)
	rr4 := httptest.NewRecorder()
	h.ServeHTTP(rr4, req4)
	if rr4.Code != 200 {
		t.Fatalf("traces tool: %d %s", rr4.Code, rr4.Body.String())
	}
}

func TestAPIMonitorUnknown404(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	req := httptest.NewRequest("GET", "/api/ui/projects/no-such-m36-project/monitor?window=24h", nil)
	req.Header.Set("X-API-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Fatalf("want 404 got %d %s", rr.Code, rr.Body.String())
	}
}
