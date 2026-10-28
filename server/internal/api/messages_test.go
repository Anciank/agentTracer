package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
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

func TestNormalizeMessagesMergesOutputsAIReply(t *testing.T) {
	// Chain run: inputs.messages = user turn; outputs.messages = user + AI reply.
	// Old bug: early return on inputs hid the AI reply.
	inputs := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "What is 2+2?", "id": "u1"},
		},
	}
	outputs := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "What is 2+2?", "id": "u1"},
			map[string]any{"role": "assistant", "content": "4", "id": "a1"},
		},
	}
	out := NormalizeMessages(inputs, outputs)
	msgs := asMsgMaps(t, out["messages"])
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages (user+AI), got %d path=%v msgs=%v", len(msgs), out["raw_path"], msgs)
	}
	if asStr(msgs[0]["role"]) != "user" || contentText(msgs[0]) != "What is 2+2?" {
		t.Fatalf("msg0 %v", msgs[0])
	}
	if asStr(msgs[1]["role"]) != "assistant" || contentText(msgs[1]) != "4" {
		t.Fatalf("msg1 AI reply missing: %v", msgs[1])
	}
	if out["raw_path"] != "outputs.messages" {
		t.Fatalf("prefer outputs when superset, got path %v", out["raw_path"])
	}
}

func TestNormalizeMessagesAppendWhenOutputsNotSuperset(t *testing.T) {
	inputs := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
	}
	outputs := map[string]any{
		"messages": []any{
			map[string]any{"role": "assistant", "content": "hello there"},
		},
	}
	out := NormalizeMessages(inputs, outputs)
	msgs := asMsgMaps(t, out["messages"])
	if len(msgs) != 2 {
		t.Fatalf("want input+output append, got %d %v", len(msgs), msgs)
	}
	if asStr(msgs[0]["role"]) != "user" || asStr(msgs[1]["role"]) != "assistant" {
		t.Fatalf("roles %v %v", msgs[0]["role"], msgs[1]["role"])
	}
}

func TestNormalizeOneMessageLCConstructorAIMessage(t *testing.T) {
	// ChatOpenAI / LangChain serialized AIMessage — old bug: role=constructor, empty content.
	raw := map[string]any{
		"lc":   1,
		"type": "constructor",
		"id":   []any{"langchain_core", "messages", "AIMessage"},
		"kwargs": map[string]any{
			"content":    "The capital is Paris.",
			"type":       "ai",
			"id":         "run-abc-llm",
			"tool_calls": []any{},
		},
	}
	m := normalizeOneMessage(0, raw, "langchain")
	if asStr(m["role"]) != "assistant" {
		t.Fatalf("role want assistant got %v", m["role"])
	}
	if contentText(m) != "The capital is Paris." {
		t.Fatalf("content %v", m["content"])
	}
	if asStr(m["source"]) != "langchain" {
		t.Fatalf("source %v", m["source"])
	}
}

func TestNormalizeOneMessageLCHumanAndTool(t *testing.T) {
	human := map[string]any{
		"lc": 1, "type": "constructor",
		"id":     []any{"langchain", "schema", "messages", "HumanMessage"},
		"kwargs": map[string]any{"content": "hi", "type": "human"},
	}
	tool := map[string]any{
		"type": "constructor",
		"id":   []any{"langchain_core", "messages", "ToolMessage"},
		"kwargs": map[string]any{
			"content":      `{"ok":true}`,
			"type":         "tool",
			"tool_call_id": "call_9",
			"name":         "lookup",
		},
	}
	hm := normalizeOneMessage(0, human, "unknown")
	tm := normalizeOneMessage(1, tool, "unknown")
	if asStr(hm["role"]) != "user" || contentText(hm) != "hi" {
		t.Fatalf("human %v", hm)
	}
	if asStr(tm["role"]) != "tool" || asStr(tm["tool_call_id"]) != "call_9" {
		t.Fatalf("tool %v", tm)
	}
}

func TestNormalizeMessagesGenerationsAIMessageChunk(t *testing.T) {
	outputs := map[string]any{
		"generations": []any{
			[]any{
				map[string]any{
					"text": "chunk-hi",
					"message": map[string]any{
						"lc":   1,
						"type": "constructor",
						"id":   []any{"langchain_core", "messages", "AIMessageChunk"},
						"kwargs": map[string]any{
							"content": "chunk-hi",
							"type":    "AIMessageChunk",
						},
					},
				},
			},
		},
	}
	out := NormalizeMessages(nil, outputs)
	msgs := asMsgMaps(t, out["messages"])
	if len(msgs) != 1 {
		t.Fatalf("len %d path=%v", len(msgs), out["raw_path"])
	}
	if asStr(msgs[0]["role"]) != "assistant" || contentText(msgs[0]) != "chunk-hi" {
		t.Fatalf("generation message %v", msgs[0])
	}
}

func TestNormalizeMessagesChainLCSerialized(t *testing.T) {
	// Realistic chain: LC-serialized inputs + outputs with AI reply appended.
	user := map[string]any{
		"lc": 1, "type": "constructor",
		"id":     []any{"langchain_core", "messages", "HumanMessage"},
		"kwargs": map[string]any{"content": "ping", "type": "human", "id": "h1"},
	}
	ai := map[string]any{
		"lc": 1, "type": "constructor",
		"id":     []any{"langchain_core", "messages", "AIMessage"},
		"kwargs": map[string]any{"content": "pong", "type": "ai", "id": "a1"},
	}
	inputs := map[string]any{"messages": []any{user}}
	outputs := map[string]any{"messages": []any{user, ai}}
	out := NormalizeMessages(inputs, outputs)
	msgs := asMsgMaps(t, out["messages"])
	if len(msgs) != 2 {
		t.Fatalf("want 2 got %d %v", len(msgs), msgs)
	}
	if asStr(msgs[1]["role"]) != "assistant" || contentText(msgs[1]) != "pong" {
		t.Fatalf("AI reply %v", msgs[1])
	}
}

func TestNormalizeMessagesPlainTextGenerationNoMessage(t *testing.T) {
	// LLM ChatGeneration with only {text} — no .message field.
	// Must still surface as an assistant message with a non-empty role.
	outputs := map[string]any{
		"generations": []any{
			[]any{
				map[string]any{
					"text":            "hello from plain generation",
					"generation_info": map[string]any{"finish_reason": "stop"},
				},
			},
		},
	}
	out := NormalizeMessages(nil, outputs)
	msgs := asMsgMaps(t, out["messages"])
	if len(msgs) != 1 {
		t.Fatalf("want 1 message from plain text generation, got %d path=%v msgs=%v", len(msgs), out["raw_path"], msgs)
	}
	if asStr(msgs[0]["role"]) != "assistant" {
		t.Fatalf("role want assistant got %q full=%v", msgs[0]["role"], msgs[0])
	}
	if contentText(msgs[0]) != "hello from plain generation" {
		t.Fatalf("content %v", msgs[0]["content"])
	}
	assertEveryMessageHasRole(t, msgs)
}

func TestNormalizeMessagesGenerationsMixedPlainAndMessage(t *testing.T) {
	outputs := map[string]any{
		"generations": []any{
			[]any{
				map[string]any{"text": "plain-only"},
				map[string]any{
					"text": "with-msg",
					"message": map[string]any{
						"lc": 1, "type": "constructor",
						"id":     []any{"langchain_core", "messages", "AIMessage"},
						"kwargs": map[string]any{"content": "with-msg", "type": "ai"},
					},
				},
			},
		},
	}
	out := NormalizeMessages(nil, outputs)
	msgs := asMsgMaps(t, out["messages"])
	if len(msgs) != 2 {
		t.Fatalf("want 2 got %d %v", len(msgs), msgs)
	}
	for i, m := range msgs {
		if asStr(m["role"]) != "assistant" {
			t.Fatalf("msg[%d] role=%q want assistant: %v", i, m["role"], m)
		}
	}
	if contentText(msgs[0]) != "plain-only" || contentText(msgs[1]) != "with-msg" {
		t.Fatalf("contents %q %q", contentText(msgs[0]), contentText(msgs[1]))
	}
	assertEveryMessageHasRole(t, msgs)
}

func TestNormalizeMessagesNeverEmptyRoleOnOutputSide(t *testing.T) {
	// Content-only map with no role/type/id — output-side default must be assistant.
	outputs := map[string]any{
		"messages": []any{
			map[string]any{"content": "who am i"},
		},
	}
	out := NormalizeMessages(nil, outputs)
	msgs := asMsgMaps(t, out["messages"])
	if len(msgs) != 1 {
		t.Fatalf("len %d", len(msgs))
	}
	if asStr(msgs[0]["role"]) != "assistant" {
		t.Fatalf("role want assistant got %q", msgs[0]["role"])
	}
	assertEveryMessageHasRole(t, msgs)
}

func TestNormalizeOneMessageFlatGenerationTextField(t *testing.T) {
	m := normalizeOneMessage(0, map[string]any{"text": "solo text field"}, "langchain")
	if asStr(m["role"]) != "assistant" {
		t.Fatalf("role %q", m["role"])
	}
	if contentText(m) != "solo text field" {
		t.Fatalf("content %v", m["content"])
	}
}

func assertEveryMessageHasRole(t *testing.T, msgs []map[string]any) {
	t.Helper()
	for i, m := range msgs {
		role := asStr(m["role"])
		if role == "" {
			t.Fatalf("message[%d] has empty role: %v", i, m)
		}
		if _, ok := m["role"]; !ok {
			t.Fatalf("message[%d] missing role key: %v", i, m)
		}
	}
}

func asMsgMaps(t *testing.T, v any) []map[string]any {
	t.Helper()
	switch msgs := v.(type) {
	case []map[string]any:
		return msgs
	case []any:
		out := make([]map[string]any, 0, len(msgs))
		for _, x := range msgs {
			m, ok := x.(map[string]any)
			if !ok {
				t.Fatalf("not map: %T", x)
			}
			out = append(out, m)
		}
		return out
	default:
		t.Fatalf("messages type %T", v)
		return nil
	}
}

func contentText(m map[string]any) string {
	parts, _ := m["content"].([]any)
	var b strings.Builder
	for _, p := range parts {
		pm, _ := p.(map[string]any)
		if pm != nil && asStr(pm["type"]) == "text" {
			b.WriteString(asStr(pm["text"]))
		}
	}
	return b.String()
}
