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

// extractUsage denormalizes token counts + model for the runs columns.
//
// Source priority (first non-empty wins; dig nested before denormalized
// columns so a PATCH carrying new usage overwrites stale columns):
//  1. outputs.llm_output.token_usage
//     (prompt_tokens / completion_tokens / total_tokens)
//  2. outputs.generations[*][*].message(.kwargs).usage_metadata
//     and flat generations[*].message(.kwargs).usage_metadata
//     (input_tokens / output_tokens / total_tokens; LC kwargs wrapper OK)
//  3. extra.metadata.usage_metadata
//     (input_tokens / output_tokens / total_tokens)
//  4. Existing digs: extra/outputs/inputs usage | token_usage | usage_metadata
//     under the node or its metadata, plus bare token fields / invocation_params
//  5. Top-level input_tokens / output_tokens / total_tokens / model columns
//
// total_tokens is computed as input+output when still missing.
func extractUsage(merged map[string]any) (inTok, outTok, totTok int, model string) {
	if outs, ok := merged["outputs"].(map[string]any); ok {
		if llm, ok := outs["llm_output"].(map[string]any); ok {
			if tu, ok := llm["token_usage"].(map[string]any); ok {
				inTok, outTok, totTok = applyPromptCompletion(tu, inTok, outTok, totTok)
			}
		}
		if !hasTokens(inTok, outTok, totTok) {
			inTok, outTok, totTok = usageFromGenerations(outs["generations"], inTok, outTok, totTok)
		}
	}

	if !hasTokens(inTok, outTok, totTok) {
		if extra, ok := merged["extra"].(map[string]any); ok {
			if meta, ok := extra["metadata"].(map[string]any); ok {
				if um, ok := meta["usage_metadata"].(map[string]any); ok {
					inTok, outTok, totTok = applyInputOutput(um, inTok, outTok, totTok)
				}
			}
		}
	}

	// Existing dig paths fill remaining gaps; nested dig still runs before
	// denormalized columns so PATCH usage overwrites stale columns.
	if !hasTokens(inTok, outTok, totTok) {
		for _, node := range []any{merged["extra"], merged["outputs"], merged["inputs"]} {
			inTok, outTok, totTok, model = digUsage(node, inTok, outTok, totTok, model)
		}
	} else if model == "" {
		for _, node := range []any{merged["extra"], merged["outputs"], merged["inputs"]} {
			_, _, _, model = digUsage(node, 0, 0, 0, model)
			if model != "" {
				break
			}
		}
	}

	if inTok == 0 {
		if v := asInt(merged["input_tokens"]); v > 0 {
			inTok = v
		}
	}
	if outTok == 0 {
		if v := asInt(merged["output_tokens"]); v > 0 {
			outTok = v
		}
	}
	if totTok == 0 {
		if v := asInt(merged["total_tokens"]); v > 0 {
			totTok = v
		}
	}
	if model == "" {
		if m := asString(merged["model"]); m != "" {
			model = m
		}
	}
	if totTok == 0 && (inTok > 0 || outTok > 0) {
		totTok = inTok + outTok
	}
	return
}

func hasTokens(inTok, outTok, totTok int) bool {
	return inTok > 0 || outTok > 0 || totTok > 0
}

func applyPromptCompletion(c map[string]any, inTok, outTok, totTok int) (int, int, int) {
	if v := asInt(c["prompt_tokens"]); v > 0 {
		inTok = v
	} else if v := asInt(c["input_tokens"]); v > 0 {
		inTok = v
	}
	if v := asInt(c["completion_tokens"]); v > 0 {
		outTok = v
	} else if v := asInt(c["output_tokens"]); v > 0 {
		outTok = v
	}
	if v := asInt(c["total_tokens"]); v > 0 {
		totTok = v
	}
	return inTok, outTok, totTok
}

func applyInputOutput(c map[string]any, inTok, outTok, totTok int) (int, int, int) {
	if v := asInt(c["input_tokens"]); v > 0 {
		inTok = v
	} else if v := asInt(c["prompt_tokens"]); v > 0 {
		inTok = v
	}
	if v := asInt(c["output_tokens"]); v > 0 {
		outTok = v
	} else if v := asInt(c["completion_tokens"]); v > 0 {
		outTok = v
	}
	if v := asInt(c["total_tokens"]); v > 0 {
		totTok = v
	}
	return inTok, outTok, totTok
}

// usageFromGenerations walks LangChain ChatOpenAI output shapes:
//   generations: [[{message: ...}]]  (nested)
//   generations: [{message: ...}]    (flat)
// message may be LC-serialized with kwargs.usage_metadata or carry
// usage_metadata directly.
func usageFromGenerations(gens any, inTok, outTok, totTok int) (int, int, int) {
	switch g := gens.(type) {
	case []any:
		for _, item := range g {
			switch row := item.(type) {
			case []any:
				for _, cell := range row {
					inTok, outTok, totTok = usageFromGenerationCell(cell, inTok, outTok, totTok)
					if hasTokens(inTok, outTok, totTok) {
						return inTok, outTok, totTok
					}
				}
			case map[string]any:
				inTok, outTok, totTok = usageFromGenerationCell(row, inTok, outTok, totTok)
				if hasTokens(inTok, outTok, totTok) {
					return inTok, outTok, totTok
				}
			}
		}
	}
	return inTok, outTok, totTok
}

func usageFromGenerationCell(cell any, inTok, outTok, totTok int) (int, int, int) {
	m, ok := cell.(map[string]any)
	if !ok {
		return inTok, outTok, totTok
	}
	msg, _ := m["message"].(map[string]any)
	if msg == nil {
		return inTok, outTok, totTok
	}
	if um := usageMetadataFromMessage(msg); um != nil {
		return applyInputOutput(um, inTok, outTok, totTok)
	}
	return inTok, outTok, totTok
}

func usageMetadataFromMessage(msg map[string]any) map[string]any {
	if um, ok := msg["usage_metadata"].(map[string]any); ok && um != nil {
		return um
	}
	if kwargs, ok := msg["kwargs"].(map[string]any); ok {
		if um, ok := kwargs["usage_metadata"].(map[string]any); ok && um != nil {
			return um
		}
	}
	return nil
}

func digUsage(node any, inTok, outTok, totTok int, model string) (int, int, int, string) {
	m, ok := node.(map[string]any)
	if !ok || m == nil {
		return inTok, outTok, totTok, model
	}
	usageMaps := []map[string]any{}
	addUsage := func(c map[string]any) {
		if usage, ok := c["usage"].(map[string]any); ok {
			usageMaps = append(usageMaps, usage)
		}
		if usage, ok := c["token_usage"].(map[string]any); ok {
			usageMaps = append(usageMaps, usage)
		}
		if ls, ok := c["usage_metadata"].(map[string]any); ok {
			usageMaps = append(usageMaps, ls)
		}
	}
	addUsage(m)
	if meta, ok := m["metadata"].(map[string]any); ok {
		addUsage(meta)
		usageMaps = append(usageMaps, meta)
	}
	for _, c := range usageMaps {
		for _, k := range []string{"input_tokens", "prompt_tokens", "inputTokenCount"} {
			if v := asInt(c[k]); v > 0 {
				inTok = v
				break
			}
		}
		for _, k := range []string{"output_tokens", "completion_tokens", "outputTokenCount"} {
			if v := asInt(c[k]); v > 0 {
				outTok = v
				break
			}
		}
		for _, k := range []string{"total_tokens", "totalTokenCount"} {
			if v := asInt(c[k]); v > 0 {
				totTok = v
				break
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
	if inTok == 0 {
		for _, k := range []string{"input_tokens", "prompt_tokens", "inputTokenCount"} {
			if v := asInt(m[k]); v > 0 {
				inTok = v
				break
			}
		}
	}
	if outTok == 0 {
		for _, k := range []string{"output_tokens", "completion_tokens", "outputTokenCount"} {
			if v := asInt(m[k]); v > 0 {
				outTok = v
				break
			}
		}
	}
	if totTok == 0 {
		for _, k := range []string{"total_tokens", "totalTokenCount"} {
			if v := asInt(m[k]); v > 0 {
				totTok = v
				break
			}
		}
	}
	if model == "" {
		for _, k := range []string{"ls_model_name", "model", "model_name", "model_id"} {
			if s := asString(m[k]); s != "" {
				model = s
				break
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
