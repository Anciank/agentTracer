package textsafe

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8NeverSplitsAndKeepsEarlyInvalid(t *testing.T) {
	// Invalid byte early must NOT empty the result (old trim-until-valid bug).
	b := []byte("hello\xffworld-and-more-padding-to-exceed-limit-abcdefghij")
	got := TruncateUTF8(b, 20)
	if len(got) == 0 {
		t.Fatal("truncated emptied buffer with early invalid byte")
	}
	if !utf8.Valid(got) {
		t.Fatalf("invalid utf8: %q", got)
	}
	if !strings.Contains(string(got), "hello") {
		t.Fatalf("lost prefix: %q", got)
	}
}

func TestTruncateUTF8EveryOffsetCJKEmoji(t *testing.T) {
	s := "前缀😀中文测试🚀后缀" + strings.Repeat("字", 40)
	raw := []byte(s)
	for n := 1; n <= len(raw); n++ {
		got := TruncateUTF8(raw, n)
		if !utf8.Valid(got) {
			t.Fatalf("n=%d invalid: %q", n, got)
		}
		if len(got) > n {
			t.Fatalf("n=%d len=%d > n", n, len(got))
		}
	}
}

func TestSanitizeStripNUL(t *testing.T) {
	in := map[string]any{
		"a": "ok\x00there",
		"b": []any{"x\x00y", map[string]any{"k": "v\x00"}},
	}
	out := Sanitize(in).(map[string]any)
	if out["a"] != "okthere" {
		t.Fatalf("a=%q", out["a"])
	}
	arr := out["b"].([]any)
	if arr[0] != "xy" {
		t.Fatalf("arr0=%q", arr[0])
	}
	if arr[1].(map[string]any)["k"] != "v" {
		t.Fatalf("nested=%v", arr[1])
	}
}
