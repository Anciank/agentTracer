package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NormalizeMessages sniffs common chat shapes and returns the frozen UI messages response.
func NormalizeMessages(inputs, outputs any) map[string]any {
	empty := map[string]any{"messages": []any{}, "paired": []any{}, "raw_path": nil}

	type hit struct {
		path string
		msgs []any
		src  string
	}
	try := func(path string, v any, src string) *hit {
		if v == nil {
			return nil
		}
		arr, ok := v.([]any)
		if !ok || len(arr) == 0 {
			return nil
		}
		// Heuristic: first element looks like a message
		first, ok := arr[0].(map[string]any)
		if !ok {
			return nil
		}
		if !(hasAny(first, "role", "type", "content") || hasAny(first, "id", "kwargs")) {
			return nil
		}
		return &hit{path: path, msgs: arr, src: src}
	}

	var found *hit
	if m, ok := inputs.(map[string]any); ok {
		found = try("inputs.messages", m["messages"], "openai")
		if found == nil {
			if kw, ok := m["kwargs"].(map[string]any); ok {
				found = try("inputs.kwargs.input", kw["input"], "langchain")
			}
		}
		if found == nil {
			found = try("inputs", inputs, "unknown")
		}
	}
	if found == nil {
		if m, ok := outputs.(map[string]any); ok {
			found = try("outputs.messages", m["messages"], "openai")
			if found == nil {
				if gens, ok := m["generations"].([]any); ok {
					var msgs []any
					for _, g := range gens {
						row, _ := g.([]any)
						for _, cell := range row {
							cm, _ := cell.(map[string]any)
							if cm == nil {
								continue
							}
							if msg, ok := cm["message"]; ok {
								msgs = append(msgs, msg)
							}
						}
					}
					if len(msgs) > 0 {
						found = &hit{path: "outputs.generations[*][*].message", msgs: msgs, src: "langchain"}
					}
				}
			}
			if found == nil {
				found = try("outputs", outputs, "unknown")
			}
		}
	}
	if found == nil {
		return empty
	}

	outMsgs := make([]map[string]any, 0, len(found.msgs))
	for i, raw := range found.msgs {
		outMsgs = append(outMsgs, normalizeOneMessage(i, raw, found.src))
	}
	paired := pairToolCalls(outMsgs)
	return map[string]any{
		"messages": outMsgs,
		"paired":   paired,
		"raw_path": found.path,
	}
}

func hasAny(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func normalizeOneMessage(i int, raw any, defaultSrc string) map[string]any {
	m, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{
			"id": fmt.Sprintf("m%d", i), "role": "assistant", "name": nil,
			"content":    []any{map[string]any{"type": "json", "value": raw}},
			"tool_calls": nil, "tool_call_id": nil, "source": defaultSrc,
		}
	}
	src := defaultSrc
	role := strings.ToLower(asStr(m["role"]))
	if role == "" {
		role = strings.ToLower(asStr(m["type"]))
	}
	// LangChain nested
	if role == "" {
		if data, ok := m["data"].(map[string]any); ok {
			role = strings.ToLower(asStr(data["type"]))
			if role == "" {
				role = strings.ToLower(asStr(data["role"]))
			}
			if m["content"] == nil {
				m["content"] = data["content"]
			}
			if m["tool_calls"] == nil {
				m["tool_calls"] = data["tool_calls"]
			}
			if m["additional_kwargs"] == nil {
				m["additional_kwargs"] = data["additional_kwargs"]
			}
			src = "langchain"
		}
		if kw, ok := m["kwargs"].(map[string]any); ok {
			role = strings.ToLower(asStr(kw["type"]))
			if role == "" {
				role = strings.ToLower(asStr(kw["role"]))
			}
			if m["content"] == nil {
				m["content"] = kw["content"]
			}
			if m["tool_calls"] == nil {
				m["tool_calls"] = kw["tool_calls"]
			}
			src = "langchain"
		}
	}
	switch role {
	case "human", "user":
		role = "user"
	case "ai", "assistant":
		role = "assistant"
	case "system":
		role = "system"
	case "tool", "function":
		role = "tool"
	default:
		if role == "" {
			role = "assistant"
		}
	}
	name := m["name"]
	if name == nil || asStr(name) == "" {
		name = nil
	}
	content := normalizeContent(m["content"], m)
	toolCalls := normalizeToolCalls(m["tool_calls"])
	if toolCalls == nil {
		if ak, ok := m["additional_kwargs"].(map[string]any); ok {
			toolCalls = normalizeToolCalls(ak["tool_calls"])
		}
	}
	var toolCallID any
	if v := asStr(m["tool_call_id"]); v != "" {
		toolCallID = v
	} else if v := asStr(m["tool_use_id"]); v != "" {
		toolCallID = v
	} else {
		toolCallID = nil
	}
	if looksAnthropic(m) {
		src = "anthropic"
	}
	return map[string]any{
		"id": fmt.Sprintf("m%d", i), "role": role, "name": name,
		"content": content, "tool_calls": toolCalls, "tool_call_id": toolCallID, "source": src,
	}
}

func looksAnthropic(m map[string]any) bool {
	if c, ok := m["content"].([]any); ok {
		for _, p := range c {
			pm, _ := p.(map[string]any)
			if pm == nil {
				continue
			}
			t := asStr(pm["type"])
			if t == "tool_use" || t == "tool_result" || t == "image" {
				return true
			}
		}
	}
	return false
}

func normalizeContent(c any, parent map[string]any) []any {
	if c == nil {
		return []any{}
	}
	switch t := c.(type) {
	case string:
		return []any{map[string]any{"type": "text", "text": t}}
	case []any:
		out := make([]any, 0, len(t))
		for _, p := range t {
			pm, ok := p.(map[string]any)
			if !ok {
				out = append(out, map[string]any{"type": "json", "value": p})
				continue
			}
			typ := asStr(pm["type"])
			switch typ {
			case "text":
				out = append(out, map[string]any{"type": "text", "text": asStr(pm["text"])})
			case "image_url":
				url := ""
				if u, ok := pm["image_url"].(map[string]any); ok {
					url = asStr(u["url"])
				} else {
					url = asStr(pm["url"])
				}
				out = append(out, map[string]any{"type": "image_url", "url": url})
			case "image":
				url := asStr(pm["url"])
				if url == "" {
					if src, ok := pm["source"].(map[string]any); ok {
						url = asStr(src["url"])
					}
				}
				out = append(out, map[string]any{"type": "image_url", "url": url})
			case "tool_use":
				out = append(out, map[string]any{
					"type": "tool_use", "id": asStr(pm["id"]), "name": asStr(pm["name"]), "input": pm["input"],
				})
			case "tool_result":
				out = append(out, map[string]any{
					"type": "tool_result", "tool_use_id": asStr(pm["tool_use_id"]), "content": pm["content"],
				})
			default:
				out = append(out, map[string]any{"type": "json", "value": pm})
			}
		}
		return out
	default:
		return []any{map[string]any{"type": "json", "value": c}}
	}
}

func normalizeToolCalls(v any) any {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := asStr(m["id"])
		name := asStr(m["name"])
		var args any
		if fn, ok := m["function"].(map[string]any); ok {
			if name == "" {
				name = asStr(fn["name"])
			}
			args = parseArgs(fn["arguments"])
		} else if a, ok := m["arguments"]; ok {
			args = parseArgs(a)
		} else if a, ok := m["args"]; ok {
			args = parseArgs(a)
		} else {
			args = map[string]any{}
		}
		out = append(out, map[string]any{"id": id, "name": name, "arguments": args})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseArgs(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case string:
		var obj any
		if err := json.Unmarshal([]byte(t), &obj); err == nil {
			return obj
		}
		return map[string]any{"_raw": t}
	default:
		return t
	}
}

func pairToolCalls(msgs []map[string]any) []any {
	callIdx := map[string]int{}
	for i, m := range msgs {
		if asStr(m["role"]) != "assistant" {
			continue
		}
		tcs, _ := m["tool_calls"].([]any)
		for _, tc := range tcs {
			tm, _ := tc.(map[string]any)
			if tm == nil {
				continue
			}
			id := asStr(tm["id"])
			if id != "" {
				callIdx[id] = i
			}
		}
		// anthropic tool_use in content
		for _, part := range asArr(m["content"]) {
			pm, _ := part.(map[string]any)
			if pm != nil && asStr(pm["type"]) == "tool_use" {
				id := asStr(pm["id"])
				if id != "" {
					callIdx[id] = i
				}
			}
		}
	}
	var paired []any
	seen := map[string]bool{}
	for i, m := range msgs {
		if asStr(m["role"]) != "tool" {
			// also tool_result parts
			for _, part := range asArr(m["content"]) {
				pm, _ := part.(map[string]any)
				if pm == nil || asStr(pm["type"]) != "tool_result" {
					continue
				}
				id := asStr(pm["tool_use_id"])
				if id == "" || seen[id] {
					continue
				}
				if ci, ok := callIdx[id]; ok {
					paired = append(paired, map[string]any{"call_id": id, "call_msg": ci, "result_msg": i})
					seen[id] = true
				}
			}
			continue
		}
		id := asStr(m["tool_call_id"])
		if id == "" || seen[id] {
			continue
		}
		if ci, ok := callIdx[id]; ok {
			paired = append(paired, map[string]any{"call_id": id, "call_msg": ci, "result_msg": i})
			seen[id] = true
		}
	}
	if paired == nil {
		paired = []any{}
	}
	return paired
}

func asStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return ""
	}
}

func asArr(v any) []any {
	a, _ := v.([]any)
	return a
}
