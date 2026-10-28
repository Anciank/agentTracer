package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NormalizeMessages sniffs common chat shapes and returns the frozen UI messages response.
// Prefer outputs.messages when it is a superset/prefix-extension of inputs.messages
// (typical LangGraph chain: inputs = prior turns, outputs = prior + AI reply).
// Otherwise show inputs then append novel output messages (dedupe by id, else role+content).
func NormalizeMessages(inputs, outputs any) map[string]any {
	empty := map[string]any{"messages": []any{}, "paired": []any{}, "raw_path": nil}

	inHit := sniffMessages(inputs, "inputs")
	outHit := sniffMessages(outputs, "outputs")

	if inHit == nil && outHit == nil {
		return empty
	}

	var raw []any
	var path string
	var src string
	switch {
	case inHit != nil && outHit != nil:
		if messagesSuperset(outHit.msgs, inHit.msgs) {
			raw, path, src = outHit.msgs, outHit.path, outHit.src
		} else {
			raw = mergeMessages(inHit.msgs, outHit.msgs)
			path = inHit.path + "+" + outHit.path
			src = inHit.src
			if src == "unknown" {
				src = outHit.src
			}
		}
	case inHit != nil:
		raw, path, src = inHit.msgs, inHit.path, inHit.src
	default:
		raw, path, src = outHit.msgs, outHit.path, outHit.src
	}

	outMsgs := make([]map[string]any, 0, len(raw))
	for i, r := range raw {
		outMsgs = append(outMsgs, normalizeOneMessage(i, r, src))
	}
	paired := pairToolCalls(outMsgs)
	return map[string]any{
		"messages": outMsgs,
		"paired":   paired,
		"raw_path": path,
	}
}

type msgHit struct {
	path string
	msgs []any
	src  string
}

func sniffMessages(blob any, root string) *msgHit {
	if blob == nil {
		return nil
	}
	try := func(path string, v any, src string) *msgHit {
		if v == nil {
			return nil
		}
		arr, ok := v.([]any)
		if !ok || len(arr) == 0 {
			return nil
		}
		first, ok := arr[0].(map[string]any)
		if !ok {
			return nil
		}
		if !(hasAny(first, "role", "type", "content") || hasAny(first, "id", "kwargs", "lc")) {
			return nil
		}
		return &msgHit{path: path, msgs: arr, src: src}
	}

	m, ok := blob.(map[string]any)
	if !ok {
		return try(root, blob, "unknown")
	}
	if h := try(root+".messages", m["messages"], "openai"); h != nil {
		return h
	}
	if kw, ok := m["kwargs"].(map[string]any); ok {
		if h := try(root+".kwargs.input", kw["input"], "langchain"); h != nil {
			return h
		}
	}
	if gens, ok := m["generations"].([]any); ok {
		var msgs []any
		hadMessage := false
		hadPlainText := false
		for _, g := range gens {
			row, _ := g.([]any)
			if row == nil {
				// flat generations[*]
				if cm, ok := g.(map[string]any); ok {
					msg, plain := generationToMessage(cm)
					if msg != nil {
						msgs = append(msgs, msg)
						if plain {
							hadPlainText = true
						} else {
							hadMessage = true
						}
					}
				}
				continue
			}
			for _, cell := range row {
				cm, _ := cell.(map[string]any)
				if cm == nil {
					continue
				}
				msg, plain := generationToMessage(cm)
				if msg != nil {
					msgs = append(msgs, msg)
					if plain {
						hadPlainText = true
					} else {
						hadMessage = true
					}
				}
			}
		}
		if len(msgs) > 0 {
			path := root + ".generations[*][*].message"
			if hadPlainText && !hadMessage {
				path = root + ".generations[*][*].text"
			} else if hadPlainText && hadMessage {
				path = root + ".generations[*][*]"
			}
			return &msgHit{path: path, msgs: msgs, src: "langchain"}
		}
	}
	return try(root, blob, "unknown")
}

// generationToMessage extracts a chat message from one LLM generation cell.
// Prefer .message (AIMessage / AIMessageChunk / LC / OpenAI-style). When the
// cell is a plain {text: "..."} ChatGeneration with no .message, synthesize an
// assistant message from .text so the UI always gets a normalized role.
// plain=true when the message was synthesized from .text only.
func generationToMessage(cm map[string]any) (msg any, plain bool) {
	if cm == nil {
		return nil, false
	}
	if m, ok := cm["message"]; ok && m != nil {
		return m, false
	}
	// OpenAI-style choice nested under the generation (rare but handled).
	if ch, ok := cm["choices"].([]any); ok {
		for _, c := range ch {
			cm2, _ := c.(map[string]any)
			if cm2 == nil {
				continue
			}
			if m, ok := cm2["message"]; ok && m != nil {
				return m, false
			}
		}
	}
	if _, hasText := cm["text"]; hasText {
		return map[string]any{
			"role":    "assistant",
			"content": cm["text"],
		}, true
	}
	return nil, false
}

func hasAny(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

// messagesSuperset reports whether outer contains inner as a prefix (by dedupe key).
func messagesSuperset(outer, inner []any) bool {
	if len(outer) < len(inner) {
		return false
	}
	for i := range inner {
		if messageDedupeKey(outer[i]) != messageDedupeKey(inner[i]) {
			return false
		}
	}
	return true
}

func mergeMessages(inputs, outputs []any) []any {
	seen := map[string]bool{}
	out := make([]any, 0, len(inputs)+len(outputs))
	for _, m := range inputs {
		k := messageDedupeKey(m)
		seen[k] = true
		out = append(out, m)
	}
	for _, m := range outputs {
		k := messageDedupeKey(m)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, m)
	}
	return out
}

// messageDedupeKey prefers an explicit id; else role+content fingerprint.
func messageDedupeKey(raw any) string {
	m, ok := raw.(map[string]any)
	if !ok {
		b, _ := json.Marshal(raw)
		return "raw:" + string(b)
	}
	if id := extractMessageID(m); id != "" {
		return "id:" + id
	}
	role, content := peekRoleContent(m)
	return "rc:" + role + "|" + content
}

func extractMessageID(m map[string]any) string {
	if id := asStr(m["id"]); id != "" && !strings.Contains(id, "/") {
		// plain string id (not LC id path array serialized oddly)
		return id
	}
	if kw, ok := m["kwargs"].(map[string]any); ok {
		if id := asStr(kw["id"]); id != "" {
			return id
		}
	}
	if data, ok := m["data"].(map[string]any); ok {
		if id := asStr(data["id"]); id != "" {
			return id
		}
	}
	return ""
}

func peekRoleContent(m map[string]any) (role, content string) {
	nm := normalizeOneMessage(0, m, "unknown")
	role = asStr(nm["role"])
	parts, _ := nm["content"].([]any)
	var b strings.Builder
	for _, p := range parts {
		pm, _ := p.(map[string]any)
		if pm == nil {
			continue
		}
		switch asStr(pm["type"]) {
		case "text":
			b.WriteString(asStr(pm["text"]))
		case "json":
			raw, _ := json.Marshal(pm["value"])
			b.Write(raw)
		}
	}
	return role, b.String()
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
	// Work on a shallow copy so unwrap does not mutate the caller's map.
	mm := map[string]any{}
	for k, v := range m {
		mm[k] = v
	}
	m = mm

	// LangChain serialized message: type "constructor" and/or `lc` key present.
	// Unwrap kwargs (preferred) or data before reading role/content — otherwise
	// role becomes "constructor" and content stays empty.
	if isLCSerialized(m) {
		src = "langchain"
		unwrapLCMessage(m)
	}

	role := strings.ToLower(asStr(m["role"]))
	if role == "" {
		role = strings.ToLower(asStr(m["type"]))
	}
	// Non-constructor LC nesting (legacy dumps with only data/kwargs, no lc/type).
	if role == "" || role == "constructor" {
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
			if m["tool_call_id"] == nil {
				m["tool_call_id"] = data["tool_call_id"]
			}
			if m["name"] == nil {
				m["name"] = data["name"]
			}
			src = "langchain"
		}
		if kw, ok := m["kwargs"].(map[string]any); ok {
			r := strings.ToLower(asStr(kw["type"]))
			if r == "" {
				r = strings.ToLower(asStr(kw["role"]))
			}
			if r != "" {
				role = r
			}
			if m["content"] == nil {
				m["content"] = kw["content"]
			}
			if m["tool_calls"] == nil {
				m["tool_calls"] = kw["tool_calls"]
			}
			if m["additional_kwargs"] == nil {
				m["additional_kwargs"] = kw["additional_kwargs"]
			}
			if m["tool_call_id"] == nil {
				m["tool_call_id"] = kw["tool_call_id"]
			}
			if m["name"] == nil {
				m["name"] = kw["name"]
			}
			src = "langchain"
		}
		if role == "" || role == "constructor" {
			if r := roleFromLCID(m["id"]); r != "" {
				role = r
			}
		}
	}

	// Plain generation cells and some SDK dumps put body in "text" not "content".
	if m["content"] == nil {
		if _, ok := m["text"]; ok {
			m["content"] = m["text"]
		}
	}

	role = mapMessageRole(role)
	// Never leave role empty: infer from LC id path one more time, then source
	// position (langchain/generations/outputs → assistant), then hard default.
	if role == "" {
		if r := roleFromLCID(m["id"]); r != "" {
			role = r
		}
	}
	if role == "" {
		role = defaultRoleForSource(defaultSrc)
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
	id := fmt.Sprintf("m%d", i)
	if rid := extractMessageID(m); rid != "" {
		id = rid
	}
	if role == "" {
		role = "assistant"
	}
	return map[string]any{
		"id": id, "role": role, "name": name,
		"content": content, "tool_calls": toolCalls, "tool_call_id": toolCallID, "source": src,
	}
}

func isLCSerialized(m map[string]any) bool {
	if _, ok := m["lc"]; ok {
		return true
	}
	return strings.EqualFold(asStr(m["type"]), "constructor")
}

// unwrapLCMessage lifts kwargs/data fields onto m and derives role from the LC id path
// (e.g. …/AIMessage) and/or kwargs.type before the constructor marker can leak as role.
func unwrapLCMessage(m map[string]any) {
	lcIDPath := m["id"] // preserve class path before kwargs.id overwrites it
	var body map[string]any
	if kw, ok := m["kwargs"].(map[string]any); ok {
		body = kw
	} else if data, ok := m["data"].(map[string]any); ok {
		body = data
	}
	if body != nil {
		if v, ok := body["content"]; ok {
			m["content"] = v
		}
		if v, ok := body["tool_calls"]; ok {
			m["tool_calls"] = v
		}
		if v, ok := body["additional_kwargs"]; ok {
			m["additional_kwargs"] = v
		}
		if v, ok := body["tool_call_id"]; ok {
			m["tool_call_id"] = v
		}
		if v, ok := body["name"]; ok {
			m["name"] = v
		}
		if v, ok := body["id"]; ok {
			m["id"] = v // runtime message id (string); class path kept in lcIDPath
		}
		if r := asStr(body["type"]); r != "" && !strings.EqualFold(r, "constructor") {
			m["role"] = r
		} else if r := asStr(body["role"]); r != "" {
			m["role"] = r
		}
	}
	if r := roleFromLCID(lcIDPath); r != "" {
		// Prefer class-derived role when body type is missing or still constructor-like.
		cur := strings.ToLower(asStr(m["role"]))
		if cur == "" || cur == "constructor" {
			m["role"] = r
		} else if cur == "ai" || cur == "human" || cur == "system" || cur == "tool" || cur == "function" {
			// keep short LC type aliases; mapMessageRole will normalize
		} else {
			m["role"] = r
		}
	}
	if strings.EqualFold(asStr(m["type"]), "constructor") {
		delete(m, "type")
	}
}

func roleFromLCID(id any) string {
	switch t := id.(type) {
	case []any:
		if len(t) == 0 {
			return ""
		}
		last := asStr(t[len(t)-1])
		return mapMessageRole(last)
	case []string:
		if len(t) == 0 {
			return ""
		}
		return mapMessageRole(t[len(t)-1])
	case string:
		// Sometimes dotted or bare class name
		if strings.Contains(t, ".") {
			parts := strings.Split(t, ".")
			return mapMessageRole(parts[len(parts)-1])
		}
		if strings.HasSuffix(t, "Message") || strings.HasSuffix(t, "MessageChunk") {
			return mapMessageRole(t)
		}
	}
	return ""
}

// defaultRoleForSource picks a non-empty role when nothing else could be inferred.
// Output / generation / langchain paths default to assistant; unknown stays assistant
// so the UI never has to guess from run_type alone.
func defaultRoleForSource(src string) string {
	switch strings.ToLower(strings.TrimSpace(src)) {
	case "openai", "anthropic", "langchain", "unknown", "":
		return "assistant"
	default:
		return "assistant"
	}
}

func mapMessageRole(role string) string {
	r := strings.ToLower(strings.TrimSpace(role))
	switch r {
	case "human", "user", "humanmessage":
		return "user"
	case "ai", "assistant", "aimessage", "aimessagechunk", "chatmessagechunk":
		return "assistant"
	case "system", "systemmessage", "systemmessagechunk":
		return "system"
	case "tool", "function", "toolmessage", "functionmessage", "toolmessagechunk":
		return "tool"
	case "chatmessage":
		// generic chat — leave as assistant unless caller set role
		return "assistant"
	case "constructor", "":
		return "assistant"
	default:
		// Strip common suffixes for future LC classes
		for _, suf := range []string{"messagechunk", "message"} {
			if strings.HasSuffix(r, suf) && len(r) > len(suf) {
				mapped := mapMessageRole(strings.TrimSuffix(r, suf))
				if mapped != "" {
					return mapped
				}
			}
		}
		if r == "" {
			return "assistant"
		}
		return r
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
