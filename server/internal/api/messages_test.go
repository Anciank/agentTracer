package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Anciank/agentTracer/server/internal/config"
)

func TestNormalizeMessagesOpenAI(t *testing.T) {
	inputs := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "content": "hello", "tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{
					"name": "lookup", "arguments": `{"q":"x"}`,
				}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "ok"},
		},
	}
	out := NormalizeMessages(inputs, nil)
	msgs, ok := out["messages"].([]map[string]any)
	if !ok {
		// json-like may be []any of maps
		arr := out["messages"].([]any)
		if len(arr) != 3 {
			t.Fatalf("len %d", len(arr))
		}
	} else if len(msgs) != 3 {
		t.Fatalf("len %d", len(msgs))
	}
	paired := out["paired"].([]any)
	if len(paired) != 1 {
		t.Fatalf("paired %v", paired)
	}
	if out["raw_path"] != "inputs.messages" {
		t.Fatalf("path %v", out["raw_path"])
	}
}

func TestNormalizeMessagesEmptyAlways200Shape(t *testing.T) {
	out := NormalizeMessages(map[string]any{"foo": 1}, map[string]any{"bar": 2})
	if out["raw_path"] != nil {
		t.Fatalf("expected null path, got %v", out["raw_path"])
	}
	msgs := out["messages"].([]any)
	if len(msgs) != 0 {
		t.Fatal("expected empty")
	}
}

func TestHealthNoCounts(t *testing.T) {
	s := New(config.Config{APIKey: "k", CORSOrigins: []string{"*"}}, nil)
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("code %d body %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"runs", "sessions", "feedback", "attachments"} {
		if _, ok := body[leak]; ok {
			t.Fatalf("health leaked %s: %v", leak, body)
		}
	}
}
