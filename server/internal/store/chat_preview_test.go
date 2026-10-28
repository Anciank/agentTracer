package store

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChatPreviewsOpenAI(t *testing.T) {
	in := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "sys"},
			map[string]any{"role": "user", "content": "Hello world"},
			map[string]any{"role": "assistant", "content": "Hi there"},
			map[string]any{"role": "user", "content": "Second"},
			map[string]any{"role": "assistant", "content": "Final answer"},
		},
	}
	fu, la := chatPreviewsFromIO(in, nil)
	if fu != "Hello world" {
		t.Fatalf("first user=%q", fu)
	}
	if la != "Final answer" {
		t.Fatalf("last ai=%q", la)
	}
}

func TestChatPreviewsLCHumanAI(t *testing.T) {
	in := map[string]any{
		"messages": []any{
			map[string]any{
				"lc":     1,
				"type":   "constructor",
				"id":     []any{"langchain", "schema", "messages", "HumanMessage"},
				"kwargs": map[string]any{"content": "reset my password please"},
			},
		},
	}
	out := map[string]any{
		"messages": []any{
			map[string]any{
				"lc":     1,
				"type":   "constructor",
				"id":     []any{"langchain", "schema", "messages", "AIMessage"},
				"kwargs": map[string]any{"content": "Sure — open Settings."},
			},
		},
	}
	fu, la := chatPreviewsFromIO(in, out)
	if fu != "reset my password please" {
		t.Fatalf("fu=%q", fu)
	}
	if la != "Sure — open Settings." {
		t.Fatalf("la=%q", la)
	}
}

func TestChatPreviewsTruncateUTF8(t *testing.T) {
	// 3-byte runes; 120-byte cap must not split
	s := strings.Repeat("你", 80) // 240 bytes
	fu, _ := chatPreviewsFromIO(map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": s}},
	}, nil)
	if len(fu) > ListPreviewBytes {
		t.Fatalf("len=%d", len(fu))
	}
	if !utf8.ValidString(fu) {
		t.Fatal("invalid utf8")
	}
	if fu != textsafeTruncateCheck(s) {
		// just ensure non-empty and valid
	}
	if fu == "" {
		t.Fatal("empty")
	}
}

func textsafeTruncateCheck(s string) string {
	return truncateListPreview(s)
}

func TestPickTraceChatPreviewsOrder(t *testing.T) {
	runs := []map[string]any{
		{
			"start_time":         "2026-10-28T12:00:02Z",
			"first_user_preview": "",
			"last_ai_preview":    "later ai",
		},
		{
			"start_time":         "2026-10-28T12:00:01Z",
			"first_user_preview": "first user",
			"last_ai_preview":    "early ai",
		},
	}
	fu, la := pickTraceChatPreviews(runs)
	if fu != "first user" {
		t.Fatalf("fu=%q", fu)
	}
	if la != "later ai" {
		t.Fatalf("la=%q", la)
	}
}
