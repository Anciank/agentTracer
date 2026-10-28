package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFillNullLLMRollupSumsDescendants(t *testing.T) {
	root := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	mid := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	llm1 := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	llm2 := "dddddddd-dddd-dddd-dddd-dddddddddddd"
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Second)
	t2 := t0.Add(2 * time.Second)

	chain := map[string]any{
		"id": root, "run_type": "chain", "name": "root",
		"input_tokens": 0, "output_tokens": 0, "total_tokens": 0, "model": nil,
	}
	trace := []map[string]any{
		chain,
		{"id": mid, "parent_run_id": root, "run_type": "chain", "name": "chat",
			"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
		{"id": llm1, "parent_run_id": mid, "run_type": "llm", "name": "ChatOpenAI",
			"model": "gpt-4o-mini", "input_tokens": 10, "output_tokens": 5, "total_tokens": 15,
			"start_time": t0},
		{"id": llm2, "parent_run_id": mid, "run_type": "llm", "name": "ChatOpenAI",
			"model": "gpt-4o-mini", "input_tokens": 20, "output_tokens": 8, "total_tokens": 28,
			"start_time": t1},
		// sibling tool — ignored
		{"id": "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee", "parent_run_id": root, "run_type": "tool",
			"name": "lookup", "input_tokens": 99, "output_tokens": 99, "total_tokens": 198,
			"model": "should-ignore", "start_time": t2},
	}
	in, out, tot, model := fillNullLLMRollup(chain, trace)
	if in != 30 || out != 13 || tot != 43 {
		t.Fatalf("tokens got %d/%d/%d want 30/13/43", in, out, tot)
	}
	if model != "gpt-4o-mini" {
		t.Fatalf("model %v", model)
	}
}

func TestFillNullLLMRollupNeverOverwrites(t *testing.T) {
	root := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	llm := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	chain := map[string]any{
		"id": root, "run_type": "chain",
		"input_tokens": 7, "output_tokens": 3, "total_tokens": 10, "model": "already-set",
	}
	trace := []map[string]any{
		chain,
		{"id": llm, "parent_run_id": root, "run_type": "llm",
			"model": "gpt-4o", "input_tokens": 100, "output_tokens": 50, "total_tokens": 150,
			"start_time": time.Now().UTC()},
	}
	in, out, tot, model := fillNullLLMRollup(chain, trace)
	if in != 7 || out != 3 || tot != 10 {
		t.Fatalf("overwrote tokens: %d/%d/%d", in, out, tot)
	}
	if model != "already-set" {
		t.Fatalf("overwrote model: %v", model)
	}
}

func TestFillNullLLMRollupPartialFill(t *testing.T) {
	// Parent has total set but input/output/model unset — fill only nulls.
	root := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	llm := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	chain := map[string]any{
		"id": root, "run_type": "chain",
		"input_tokens": 0, "output_tokens": 0, "total_tokens": 999, "model": "",
	}
	trace := []map[string]any{
		chain,
		{"id": llm, "parent_run_id": root, "run_type": "llm",
			"model": "claude-3-haiku", "input_tokens": 40, "output_tokens": 12, "total_tokens": 52},
	}
	in, out, tot, model := fillNullLLMRollup(chain, trace)
	if in != 40 || out != 12 {
		t.Fatalf("partial fill tokens %d/%d", in, out)
	}
	if tot != 999 {
		t.Fatalf("total overwritten: %d", tot)
	}
	if model != "claude-3-haiku" {
		t.Fatalf("model %v", model)
	}
}

func TestFillNullLLMRollupModelMostUsedThenEarliest(t *testing.T) {
	root := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	trace := []map[string]any{
		{"id": root, "run_type": "chain", "input_tokens": 0, "output_tokens": 0, "total_tokens": 0, "model": nil},
		{"id": "1", "parent_run_id": root, "run_type": "llm", "model": "gpt-4o",
			"input_tokens": 1, "output_tokens": 1, "total_tokens": 2, "start_time": t0.Add(2 * time.Second)},
		{"id": "2", "parent_run_id": root, "run_type": "llm", "model": "gpt-4o-mini",
			"input_tokens": 1, "output_tokens": 1, "total_tokens": 2, "start_time": t0},
		{"id": "3", "parent_run_id": root, "run_type": "llm", "model": "gpt-4o-mini",
			"input_tokens": 1, "output_tokens": 1, "total_tokens": 2, "start_time": t0.Add(time.Second)},
	}
	_, _, _, model := fillNullLLMRollup(trace[0], trace)
	if model != "gpt-4o-mini" {
		t.Fatalf("want most-used gpt-4o-mini, got %v", model)
	}

	// Equal counts: earliest start_time wins.
	trace2 := []map[string]any{
		{"id": root, "run_type": "chain", "input_tokens": 0, "output_tokens": 0, "total_tokens": 0, "model": nil},
		{"id": "a", "parent_run_id": root, "run_type": "llm", "model": "b-model",
			"input_tokens": 1, "output_tokens": 0, "total_tokens": 1, "start_time": t0.Add(time.Second)},
		{"id": "b", "parent_run_id": root, "run_type": "llm", "model": "a-model",
			"input_tokens": 1, "output_tokens": 0, "total_tokens": 1, "start_time": t0},
	}
	_, _, _, model2 := fillNullLLMRollup(trace2[0], trace2)
	if model2 != "a-model" {
		t.Fatalf("tie-break earliest start want a-model got %v", model2)
	}
}

func TestFillNullLLMRollupNoDescendantsLeavesUnset(t *testing.T) {
	root := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	chain := map[string]any{
		"id": root, "run_type": "chain",
		"input_tokens": 0, "output_tokens": 0, "total_tokens": 0, "model": nil,
	}
	in, out, tot, model := fillNullLLMRollup(chain, []map[string]any{chain})
	if in != 0 || out != 0 || tot != 0 || model != nil {
		t.Fatalf("got %d/%d/%d %v", in, out, tot, model)
	}
}

func TestAPIUIRunRollupFromLLMChild(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	rootID := uuid.NewString()
	childID := uuid.NewString()
	midID := uuid.NewString()
	proj := "rollup-synth-" + uuid.NewString()[:8]
	start := time.Now().UTC()

	post := func(body map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		id := body["id"].(string)
		req := httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(encodeMP([]struct {
			name, ct string
			data     []byte
		}{
			{"post." + id, "application/json", b},
		})))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
		req.Header.Set("X-Api-Key", key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 204 {
			t.Fatalf("post %s: %d %s", id, rr.Code, rr.Body.String())
		}
	}

	// Chain root with no tokens/model.
	post(map[string]any{
		"id": rootID, "trace_id": rootID, "name": "agent", "run_type": "chain",
		"session_name": proj, "start_time": start.Format(time.RFC3339Nano),
		"end_time":     start.Add(3 * time.Second).Format(time.RFC3339Nano),
		"dotted_order": "20261028T120000000000Z" + rootID[:8],
	})
	// Intermediate chain (also empty).
	post(map[string]any{
		"id": midID, "trace_id": rootID, "parent_run_id": rootID, "name": "chat", "run_type": "chain",
		"session_name": proj, "start_time": start.Add(time.Second).Format(time.RFC3339Nano),
		"end_time":     start.Add(2 * time.Second).Format(time.RFC3339Nano),
		"dotted_order": "20261028T120000000000Z" + rootID[:8] + ".20261028T120001000000Z" + midID[:8],
	})
	// Nested LLM with tokens + model (via top-level fields that UpsertRun accepts).
	post(map[string]any{
		"id": childID, "trace_id": rootID, "parent_run_id": midID, "name": "ChatOpenAI", "run_type": "llm",
		"session_name": proj, "start_time": start.Add(1500 * time.Millisecond).Format(time.RFC3339Nano),
		"end_time": start.Add(2 * time.Second).Format(time.RFC3339Nano),
		"dotted_order": "20261028T120000000000Z" + rootID[:8] + ".20261028T120001000000Z" + midID[:8] +
			".20261028T120001500000Z" + childID[:8],
		"input_tokens": 42, "output_tokens": 17, "total_tokens": 59, "model": "gpt-4o-mini",
		"outputs": map[string]any{"generations": []any{}},
	})

	req := httptest.NewRequest("GET", "/api/ui/runs/"+rootID, nil)
	req.Header.Set("X-Api-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("get %d %s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	tok, _ := got["tokens"].(map[string]any)
	if tok == nil {
		t.Fatalf("no tokens: %v", got)
	}
	if asIntAny(tok["input"]) != 42 || asIntAny(tok["output"]) != 17 || asIntAny(tok["total"]) != 59 {
		t.Fatalf("rolled tokens %#v", tok)
	}
	if got["model"] != "gpt-4o-mini" {
		t.Fatalf("rolled model %v", got["model"])
	}

	// Non-null on a sibling chain mid should not be overwritten when we set them —
	// verify LLM child detail still shows its own values (no "descendant" of itself).
	req = httptest.NewRequest("GET", "/api/ui/runs/"+childID, nil)
	req.Header.Set("X-Api-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("llm get %d", rr.Code)
	}
	var llmGot map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &llmGot)
	lt, _ := llmGot["tokens"].(map[string]any)
	if asIntAny(lt["input"]) != 42 || llmGot["model"] != "gpt-4o-mini" {
		t.Fatalf("llm self detail %#v", llmGot)
	}
}

func TestAPIUIRunRollupDoesNotOverwriteParentTokens(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	rootID := uuid.NewString()
	childID := uuid.NewString()
	proj := "rollup-keep-" + uuid.NewString()[:8]
	start := time.Now().UTC()

	post := func(body map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		id := body["id"].(string)
		req := httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(encodeMP([]struct {
			name, ct string
			data     []byte
		}{{"post." + id, "application/json", b}})))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
		req.Header.Set("X-Api-Key", key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 204 {
			t.Fatalf("post %d %s", rr.Code, rr.Body.String())
		}
	}
	post(map[string]any{
		"id": rootID, "trace_id": rootID, "name": "agent", "run_type": "chain",
		"session_name": proj, "start_time": start.Format(time.RFC3339Nano),
		"end_time":     start.Add(time.Second).Format(time.RFC3339Nano),
		"dotted_order": "20261028T120000000000Z" + rootID[:8],
		"input_tokens": 1, "output_tokens": 1, "total_tokens": 2, "model": "parent-model",
	})
	post(map[string]any{
		"id": childID, "trace_id": rootID, "parent_run_id": rootID, "name": "llm", "run_type": "llm",
		"session_name": proj, "start_time": start.Format(time.RFC3339Nano),
		"end_time":     start.Add(time.Second).Format(time.RFC3339Nano),
		"dotted_order": "20261028T120000000000Z" + rootID[:8] + ".20261028T120000100000Z" + childID[:8],
		"input_tokens": 100, "output_tokens": 50, "total_tokens": 150, "model": "child-model",
	})
	req := httptest.NewRequest("GET", "/api/ui/runs/"+rootID, nil)
	req.Header.Set("X-Api-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	tok, _ := got["tokens"].(map[string]any)
	if asIntAny(tok["input"]) != 1 || asIntAny(tok["total"]) != 2 || got["model"] != "parent-model" {
		t.Fatalf("must keep parent values: %#v model=%v", tok, got["model"])
	}
}
