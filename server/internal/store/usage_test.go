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

func TestExtractUsagePatchOverwrites(t *testing.T) {
	merged := map[string]any{
		"input_tokens": 9, "output_tokens": 1, "total_tokens": 10,
		"extra": map[string]any{"metadata": map[string]any{
			"usage": map[string]any{"input_tokens": 12, "output_tokens": 8, "total_tokens": 20},
		}},
	}
	in, out, tot, _ := extractUsage(merged)
	if in != 12 || out != 8 || tot != 20 {
		t.Fatalf("want 12/8/20 got %d/%d/%d", in, out, tot)
	}
}

func TestExtractUsageLLMOutputTokenUsage(t *testing.T) {
	// ChatOpenAI / older LC: outputs.llm_output.token_usage
	merged := map[string]any{
		"name": "ChatOpenAI", "run_type": "llm",
		"outputs": map[string]any{
			"llm_output": map[string]any{
				"token_usage": map[string]any{
					"prompt_tokens":     128,
					"completion_tokens": 64,
					"total_tokens":      192,
				},
				"model_name": "gpt-4o-mini",
			},
			"generations": []any{},
		},
		// Lower-priority decoy must not win when llm_output is present.
		"extra": map[string]any{"metadata": map[string]any{
			"usage_metadata": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
		}},
	}
	in, out, tot, _ := extractUsage(merged)
	if in != 128 || out != 64 || tot != 192 {
		t.Fatalf("got %d/%d/%d", in, out, tot)
	}
}

func TestExtractUsageGenerationsNestedKwargs(t *testing.T) {
	// Real-ish LC serialized AIMessage under nested generations[][].
	merged := map[string]any{
		"name": "ChatOpenAI", "run_type": "llm",
		"outputs": map[string]any{
			"generations": []any{
				[]any{
					map[string]any{
						"text": "hello",
						"message": map[string]any{
							"lc":  1,
							"type": "constructor",
							"id":  []any{"langchain", "schema", "messages", "AIMessage"},
							"kwargs": map[string]any{
								"content": "hello",
								"usage_metadata": map[string]any{
									"input_tokens":  40,
									"output_tokens": 12,
									// total omitted on purpose — must be computed
								},
							},
						},
					},
				},
			},
		},
	}
	in, out, tot, _ := extractUsage(merged)
	if in != 40 || out != 12 || tot != 52 {
		t.Fatalf("got %d/%d/%d want 40/12/52", in, out, tot)
	}
}

func TestExtractUsageGenerationsFlatDirect(t *testing.T) {
	merged := map[string]any{
		"outputs": map[string]any{
			"generations": []any{
				map[string]any{
					"message": map[string]any{
						"usage_metadata": map[string]any{
							"input_tokens":  7,
							"output_tokens": 3,
							"total_tokens":  10,
						},
					},
				},
			},
		},
	}
	in, out, tot, _ := extractUsage(merged)
	if in != 7 || out != 3 || tot != 10 {
		t.Fatalf("got %d/%d/%d", in, out, tot)
	}
}

func TestExtractUsageExtraMetadataUsageMetadata(t *testing.T) {
	merged := map[string]any{
		"extra": map[string]any{
			"metadata": map[string]any{
				"ls_model_name": "gpt-4o",
				"usage_metadata": map[string]any{
					"input_tokens":  100,
					"output_tokens": 50,
					"total_tokens":  150,
				},
			},
		},
	}
	in, out, tot, model := extractUsage(merged)
	if in != 100 || out != 50 || tot != 150 || model != "gpt-4o" {
		t.Fatalf("got %d/%d/%d %q", in, out, tot, model)
	}
}

func TestExtractUsagePriorityLLMOutputBeatsGenerations(t *testing.T) {
	merged := map[string]any{
		"outputs": map[string]any{
			"llm_output": map[string]any{
				"token_usage": map[string]any{
					"prompt_tokens": 2, "completion_tokens": 2, "total_tokens": 4,
				},
			},
			"generations": []any{
				map[string]any{
					"message": map[string]any{
						"usage_metadata": map[string]any{
							"input_tokens": 99, "output_tokens": 99, "total_tokens": 198,
						},
					},
				},
			},
		},
	}
	in, out, tot, _ := extractUsage(merged)
	if in != 2 || out != 2 || tot != 4 {
		t.Fatalf("priority broken: %d/%d/%d", in, out, tot)
	}
}
