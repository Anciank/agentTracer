package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestProjectLookupByUUIDAndName: LookupSessionID accepts session UUID or name;
// unknown id/name returns 404 (not empty 200).
func TestProjectLookupByUUIDAndName(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	project := "uuid-lookup-" + uuid.NewString()[:8]
	runID := uuid.NewString()
	start := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	end := time.Now().UTC().Format(time.RFC3339Nano)
	body, _ := json.Marshal(map[string]any{
		"id": runID, "trace_id": runID, "name": "LangGraph", "run_type": "chain",
		"session_name": project, "start_time": start, "end_time": end,
		"outputs":      map[string]any{"ok": true},
		"dotted_order": "20260101T000000000000Z" + runID[:8],
	})
	req := httptest.NewRequest("POST", "/runs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 202 && rr.Code != 200 {
		t.Fatalf("create run %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/ui/projects", nil)
	req.Header.Set("x-api-key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("projects %d %s", rr.Code, rr.Body.String())
	}
	var projects []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &projects); err != nil {
		t.Fatal(err)
	}
	var sessionID string
	for _, p := range projects {
		if p["name"] == project {
			sessionID, _ = p["id"].(string)
			break
		}
	}
	if sessionID == "" {
		t.Fatalf("project %q not in list: %v", project, projects)
	}

	for _, pathID := range []string{project, sessionID} {
		for _, ep := range []string{"graph", "paths"} {
			url := "/api/ui/projects/" + pathID + "/" + ep + "?window=24h"
			req = httptest.NewRequest("GET", url, nil)
			req.Header.Set("x-api-key", key)
			rr = httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != 200 {
				t.Fatalf("%s by %q → %d %s", ep, pathID, rr.Code, rr.Body.String())
			}
		}
	}

	unknownName := "no-such-project-" + uuid.NewString()
	unknownUUID := uuid.NewString()
	for _, pathID := range []string{unknownName, unknownUUID} {
		for _, ep := range []string{"graph", "paths"} {
			url := "/api/ui/projects/" + pathID + "/" + ep + "?window=24h"
			req = httptest.NewRequest("GET", url, nil)
			req.Header.Set("x-api-key", key)
			rr = httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != 404 {
				t.Fatalf("%s unknown %q → want 404 got %d %s", ep, pathID, rr.Code, rr.Body.String())
			}
		}
	}
}
