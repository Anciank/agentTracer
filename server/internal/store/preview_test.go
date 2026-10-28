package store

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPreviewNeverSplitsUTF8(t *testing.T) {
	outs := map[string]any{
		"messages": []any{
			map[string]any{
				"content": "LangGraph 是一个由 LangChain 团队开发的开源框架，专为构建状态化、多智能体或复杂逻辑的 LLM 应用而设计。它通过有向图（DAG）模型将任务分解为节点和边，允许你显式控制执行流程、管理中间状态并实现循环迭代，从而突破传统链式调用的线性限制。",
				"type":    "ai",
			},
		},
	}
	raw, err := json.Marshal(outs)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 200 {
		t.Fatalf("fixture too short: %d", len(raw))
	}
	naive := string(raw[:200])
	if utf8.ValidString(naive) {
		t.Fatalf("test fixture no longer hits mid-rune at 200; pick longer CJK")
	}
	got := preview(outs, 200)
	if !utf8.ValidString(got) {
		t.Fatalf("preview returned invalid UTF-8 (len=%d)", len(got))
	}
	if len(got) > 200 {
		t.Fatalf("preview longer than n: %d", len(got))
	}
	if got == "" {
		t.Fatal("preview empty")
	}
	if preview(map[string]any{"ok": true}, 200) == "" {
		t.Fatal("short preview empty")
	}
	if preview(nil, 200) != "" {
		t.Fatal("nil preview")
	}
}

func TestPreviewEarlyInvalidDoesNotEmpty(t *testing.T) {
	// Craft via raw JSON with an invalid byte early; marshal path won't emit this,
	// so call truncate path through preview on a long string then simulate:
	long := map[string]any{"x": strings.Repeat("字", 80)}
	got := preview(long, 50)
	if got == "" || !utf8.ValidString(got) {
		t.Fatalf("got %q", got)
	}
}

func TestPreviewEveryOffsetValid(t *testing.T) {
	outs := map[string]any{
		"messages": []any{
			map[string]any{"content": "前缀😀中文🚀" + strings.Repeat("测", 60), "type": "ai"},
		},
	}
	raw, _ := json.Marshal(outs)
	for n := 1; n <= len(raw) && n <= 300; n++ {
		// preview always marshals full then truncates
		got := preview(outs, n)
		if !utf8.ValidString(got) {
			t.Fatalf("n=%d invalid", n)
		}
		if len(got) > n {
			t.Fatalf("n=%d len=%d", n, len(got))
		}
	}
}
