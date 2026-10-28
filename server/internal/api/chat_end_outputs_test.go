package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"

	"github.com/google/uuid"
	"path/filepath"
	"testing"
	"time"
)

// TestCapturedChatEndWithCJKOutputs: scrubbed captured langsmith multipart parts
// (patch.<chat> + .inputs/.outputs/.events/.extra) where .outputs holds CJK.
// Pre-fix: UpsertRun failed because outputs_preview truncated mid-UTF-8,
// Postgres rejected the row, and end_time stayed null (204 still returned).
func TestCapturedChatEndWithCJKOutputs(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()

	metaPath := filepath.Join("testdata", "chat_end_outputs_multipart.meta.json")
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		ContentType string `json:"content_type"`
		StuckChatID string `json:"stuck_chat_id"`
	}
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("testdata", "chat_end_outputs_multipart.bin"))
	if err != nil {
		t.Fatal(err)
	}
	chatID := meta.StuckChatID
	if chatID == "" {
		t.Fatal("missing stuck_chat_id")
	}

	// Create the run first (SDK post without end), mirroring capture order.
	start := "2026-10-28T11:21:46.876000+00:00"
	postBody, _ := json.Marshal(map[string]any{
		"id": chatID, "trace_id": chatID, "name": "chat", "run_type": "chain",
		"session_name": "fix-chat-end-cjk", "start_time": start, "end_time": nil,
		"dotted_order": "20261028T112146876000Z" + chatID[:8],
		"outputs":      map[string]any{},
	})
	req := httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(encodeMP([]struct {
		name, ct string
		data     []byte
	}{
		{"post." + chatID, "application/json", postBody},
		{"post." + chatID + ".outputs", "application/json", []byte(`{}`)},
	})))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
	req.Header.Set("X-Api-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("create %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(body))
	req.Header.Set("Content-Type", meta.ContentType)
	req.Header.Set("X-Api-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("captured patch multipart %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/ui/runs/"+chatID, nil)
	req.Header.Set("X-Api-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("get %d %s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["end_time"] == nil || got["end_time"] == "" {
		t.Fatalf("end_time still null after captured chat end+CJK outputs: %#v", got)
	}
	if s, _ := got["status"].(string); s != "success" {
		t.Fatalf("status=%v want success", got["status"])
	}
	outs, _ := got["outputs"].(map[string]any)
	if outs == nil || outs["messages"] == nil {
		t.Fatalf("outputs not applied: %#v", got["outputs"])
	}
}

// TestChatEndCJKOutputsSynthetic: minimal repro — patch end_time + CJK outputs
// attachment in one multipart must both land.
func TestChatEndCJKOutputsSynthetic(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	runID := uuid.NewString()
	start := time.Now().UTC().Add(-2 * time.Second).Format(time.RFC3339Nano)
	end := time.Now().UTC().Format(time.RFC3339Nano)
	postBody, _ := json.Marshal(map[string]any{
		"id": runID, "trace_id": runID, "name": "chat", "run_type": "chain",
		"session_name": "cjk-synthetic", "start_time": start, "end_time": nil,
		"dotted_order": "20261028T000000000000Z" + runID[:8],
	})
	patchBody, _ := json.Marshal(map[string]any{
		"id": runID, "trace_id": runID, "end_time": end,
		"dotted_order": "20261028T000000000000Z" + runID[:8],
	})
	outputs, _ := json.Marshal(map[string]any{
		"messages": []any{
			map[string]any{
				"content": "LangGraph 是一个由 LangChain 团队开发的开源框架，专为构建状态化、多智能体或复杂逻辑的 LLM 应用而设计。它通过有向图（DAG）模型将任务分解为节点和边，允许你显式控制执行流程、管理中间状态并实现循环迭代，从而突破传统链式调用的线性限制。对于后端工程师而言，其核心价值在于提供了类似工作流引擎的确定性控制能力。",
				"type":    "ai",
			},
		},
	})
	req := httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(encodeMP([]struct {
		name, ct string
		data     []byte
	}{
		{"post." + runID, "application/json", postBody},
	})))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
	req.Header.Set("X-Api-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("post %d", rr.Code)
	}

	req = httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(encodeMP([]struct {
		name, ct string
		data     []byte
	}{
		{"patch." + runID, "application/json", patchBody},
		{"patch." + runID + ".outputs", "application/json", outputs},
	})))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
	req.Header.Set("X-Api-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("patch %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/ui/runs/"+runID, nil)
	req.Header.Set("X-Api-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["end_time"] == nil || got["end_time"] == "" {
		t.Fatalf("synthetic CJK outputs wiped end_time: %#v", got)
	}
	if s, _ := got["status"].(string); s != "success" {
		t.Fatalf("status=%v", got["status"])
	}
}
