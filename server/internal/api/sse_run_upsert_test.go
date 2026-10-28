package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSSERunUpsertOnCreateAndPatch(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest("GET", "/api/ui/traces/stream?project=sse-proj", nil)
	req = req.WithContext(ctx)
	req.Header.Set("x-api-key", key)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rr, req)
	}()

	// Give the stream a moment to subscribe
	time.Sleep(150 * time.Millisecond)

	runID := uuid.NewString()
	start := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	postBody, _ := json.Marshal(map[string]any{
		"id": runID, "trace_id": runID, "name": "root", "run_type": "chain",
		"session_name": "sse-proj", "start_time": start,
		"extra": map[string]any{"metadata": map[string]any{
			"usage":         map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
			"ls_model_name": "gpt-test",
		}},
	})
	postReq := httptest.NewRequest("POST", "/runs", bytes.NewReader(postBody))
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("x-api-key", key)
	postRR := httptest.NewRecorder()
	h.ServeHTTP(postRR, postReq)
	if postRR.Code != 200 && postRR.Code != 201 && postRR.Code != 202 {
		// LangSmith often 200/202; accept 2xx
		if postRR.Code < 200 || postRR.Code >= 300 {
			cancel()
			<-done
			t.Fatalf("POST /runs %d %s", postRR.Code, postRR.Body.String())
		}
	}

	end := time.Now().UTC().Format(time.RFC3339Nano)
	patchBody, _ := json.Marshal(map[string]any{
		"id": runID, "end_time": end, "status": "success",
		"outputs": map[string]any{"ok": true},
		"extra": map[string]any{"metadata": map[string]any{
			"usage": map[string]any{"input_tokens": 11, "output_tokens": 7, "total_tokens": 18},
		}},
	})
	patchReq := httptest.NewRequest("PATCH", "/runs/"+runID, bytes.NewReader(patchBody))
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("x-api-key", key)
	patchRR := httptest.NewRecorder()
	h.ServeHTTP(patchRR, patchReq)
	if patchRR.Code < 200 || patchRR.Code >= 300 {
		cancel()
		<-done
		t.Fatalf("PATCH /runs %d %s", patchRR.Code, patchRR.Body.String())
	}

	// Wait for events to flush into recorder
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events := parseSSE(rr.Body.String())
		nUpsert := 0
		for _, e := range events {
			if e.Event == "run_upsert" {
				nUpsert++
			}
		}
		if nUpsert >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done

	var upserts []map[string]any
	for _, e := range parseSSE(rr.Body.String()) {
		if e.Event == "run_upsert" {
			upserts = append(upserts, e.Data)
		}
	}
	if len(upserts) < 2 {
		t.Fatalf("want >=2 run_upsert, got %d; body=%q", len(upserts), rr.Body.String())
	}

	// First should be create (no end or pending); last patch should have end_time + tokens
	last := upserts[len(upserts)-1]
	if fmt.Sprint(last["trace_id"]) != runID {
		t.Fatalf("trace_id=%v", last["trace_id"])
	}
	row, _ := last["row"].(map[string]any)
	if row == nil {
		t.Fatalf("missing row: %+v", last)
	}
	if row["end_time"] == nil || fmt.Sprint(row["end_time"]) == "" {
		t.Fatalf("patch row missing end_time: %+v", row)
	}
	if st := fmt.Sprint(row["status"]); st != "success" && st != "pending" {
		// status may be derived
		t.Logf("status=%s", st)
	}
	if fmt.Sprint(row["status"]) != "success" {
		t.Fatalf("status want success got %v", row["status"])
	}
	tok, _ := row["tokens"].(map[string]any)
	if tok == nil {
		t.Fatalf("missing tokens: %+v", row)
	}
	// After patch, total should reflect updated usage (18) or at least non-zero
	tot := 0
	switch v := tok["total"].(type) {
	case float64:
		tot = int(v)
	case int:
		tot = v
	case json.Number:
		n, _ := v.Int64()
		tot = int(n)
	}
	if tot < 15 {
		t.Fatalf("tokens.total want >=15 got %v in %+v", tok["total"], tok)
	}
}

type sseEvent struct {
	Event string
	Data  map[string]any
}

func parseSSE(body string) []sseEvent {
	var out []sseEvent
	sc := bufio.NewScanner(strings.NewReader(body))
	var ev string
	var data strings.Builder
	flush := func() {
		if ev == "" && data.Len() == 0 {
			return
		}
		m := map[string]any{}
		_ = json.Unmarshal([]byte(data.String()), &m)
		out = append(out, sseEvent{Event: ev, Data: m})
		ev = ""
		data.Reset()
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "event:") {
			ev = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	return out
}

// silence unused import if recorder path changes
var _ = io.EOF
var _ = http.StatusOK
