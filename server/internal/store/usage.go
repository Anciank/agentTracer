package store

import "encoding/json"

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	case float32:
		return int(t)
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	default:
		return 0
	}
}

func extractUsage(merged map[string]any) (inTok, outTok, totTok int, model string) {
	if v := asInt(merged["input_tokens"]); v > 0 {
		inTok = v
	}
	if v := asInt(merged["output_tokens"]); v > 0 {
		outTok = v
	}
	if v := asInt(merged["total_tokens"]); v > 0 {
		totTok = v
	}
	if m := asString(merged["model"]); m != "" {
		model = m
	}
	for _, node := range []any{merged["extra"], merged["outputs"], merged["inputs"]} {
		inTok, outTok, totTok, model = digUsage(node, inTok, outTok, totTok, model)
	}
	if totTok == 0 && (inTok > 0 || outTok > 0) {
		totTok = inTok + outTok
	}
	return
}

func digUsage(node any, inTok, outTok, totTok int, model string) (int, int, int, string) {
	m, ok := node.(map[string]any)
	if !ok || m == nil {
		return inTok, outTok, totTok, model
	}
	candidates := []map[string]any{m}
	if meta, ok := m["metadata"].(map[string]any); ok {
		candidates = append(candidates, meta)
	}
	// Collect usage maps from m and from nested metadata.
	expand := append([]map[string]any{}, candidates...)
	for _, c := range expand {
		if usage, ok := c["usage"].(map[string]any); ok {
			candidates = append(candidates, usage)
		}
		if usage, ok := c["token_usage"].(map[string]any); ok {
			candidates = append(candidates, usage)
		}
		if ls, ok := c["usage_metadata"].(map[string]any); ok {
			candidates = append(candidates, ls)
		}
	}
	for _, c := range candidates {
		if inTok == 0 {
			for _, k := range []string{"input_tokens", "prompt_tokens", "inputTokenCount"} {
				if v := asInt(c[k]); v > 0 {
					inTok = v
					break
				}
			}
		}
		if outTok == 0 {
			for _, k := range []string{"output_tokens", "completion_tokens", "outputTokenCount"} {
				if v := asInt(c[k]); v > 0 {
					outTok = v
					break
				}
			}
		}
		if totTok == 0 {
			for _, k := range []string{"total_tokens", "totalTokenCount"} {
				if v := asInt(c[k]); v > 0 {
					totTok = v
					break
				}
			}
		}
		if model == "" {
			for _, k := range []string{"ls_model_name", "model", "model_name", "model_id"} {
				if s := asString(c[k]); s != "" {
					model = s
					break
				}
			}
		}
	}
	if model == "" {
		if inv, ok := m["invocation_params"].(map[string]any); ok {
			if s := asString(inv["model"]); s != "" {
				model = s
			} else if s := asString(inv["model_name"]); s != "" {
				model = s
			}
		}
	}
	return inTok, outTok, totTok, model
}
