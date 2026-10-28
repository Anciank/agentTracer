package store

import (
	"testing"
	"time"
)

func TestExtractUsageFromExtra(t *testing.T) {
	merged := map[string]any{
		"extra": map[string]any{
			"metadata": map[string]any{
				"ls_model_name": "gpt-4o-mini",
				"usage": map[string]any{
					"input_tokens":  10,
					"output_tokens": 5,
				},
			},
		},
	}
	in, out, tot, model := extractUsage(merged)
	if in != 10 || out != 5 || tot != 15 || model != "gpt-4o-mini" {
		t.Fatalf("got %d %d %d %q", in, out, tot, model)
	}
}

func TestEncodeDecodeCursor(t *testing.T) {
	tt, err := time.Parse(time.RFC3339Nano, "2026-10-27T15:04:05.123Z")
	if err != nil {
		t.Fatal(err)
	}
	c := EncodeCursor(tt, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	st, tid, err := DecodeCursor(c)
	if err != nil {
		t.Fatal(err)
	}
	if tid != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("tid %s", tid)
	}
	if st.UTC().Format("2006-01-02T15:04:05") != "2026-10-27T15:04:05" {
		t.Fatalf("time %v", st)
	}
}
