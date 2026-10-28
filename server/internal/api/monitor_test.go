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
	nonzero := 0
	for _, raw := range series {
		b, _ := raw.(map[string]any)
		total := int(b["total"].(float64))
		if total == 0 {
			if int(b["success"].(float64)) != 0 || int(b["error"].(float64)) != 0 {
				t.Fatalf("zero-fill bucket not zero: %v", b)
			}
		} else {
			nonzero++
		}
	}
	if nonzero == 0 {
		t.Fatal("expected at least one non-empty bucket")
	}
	models, _ := body["by_model"].([]any)
	if len(models) < 1 {
		t.Fatalf("by_model %v", body["by_model"])
	}
	tools, _ := body["by_tool"].([]any)
	if len(tools) < 1 {
		t.Fatalf("by_tool %v", body["by_tool"])
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
	for _, id := range []string{"no-such-m36-project", uuid.NewString()} {
		req := httptest.NewRequest("GET", "/api/ui/projects/"+id+"/monitor?window=24h", nil)
		req.Header.Set("X-API-Key", key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 404 {
			t.Fatalf("want 404 for %q got %d %s", id, rr.Code, rr.Body.String())
		}
	}
}

// TestAPIMonitorBySessionUUID: /monitor accepts session UUID as {id} (same as name).
func TestAPIMonitorBySessionUUID(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	proj := "m36-api-mon-uuid"
	now := time.Now().UTC()
	root := uuid.New()
	start := now.Add(-10 * time.Minute)
	end := now.Add(-5 * time.Minute)
	b, _ := json.Marshal(map[string]any{
		"id": root.String(), "trace_id": root.String(), "name": "root", "run_type": "chain",
		"session_name": proj, "start_time": start.Format(time.RFC3339Nano),
		"end_time": end.Format(time.RFC3339Nano), "status": "success",
	})
	req := httptest.NewRequest("POST", "/runs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code >= 300 {
		t.Fatalf("post: %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/ui/projects", nil)
	req.Header.Set("X-API-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("projects: %d %s", rr.Code, rr.Body.String())
	}
	var projects []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &projects); err != nil {
		t.Fatal(err)
	}
	var sessionID string
	for _, p := range projects {
		if p["name"] == proj {
			sessionID, _ = p["id"].(string)
			break
		}
	}
	if sessionID == "" {
		t.Fatalf("session id for %q missing: %v", proj, projects)
	}

	req = httptest.NewRequest("GET", "/api/ui/projects/"+sessionID+"/monitor?window=24h", nil)
	req.Header.Set("X-API-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("monitor by UUID: %d %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["project_id"] != sessionID {
		t.Fatalf("project_id %v want %s", body["project_id"], sessionID)
	}
	if body["trace_count"].(float64) < 1 {
		t.Fatalf("trace_count %v", body["trace_count"])
	}
}
