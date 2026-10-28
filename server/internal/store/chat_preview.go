package store

import (
	"encoding/json"
	"strings"

	"github.com/Anciank/agentTracer/server/internal/textsafe"
)

// ListPreviewBytes is the max UTF-8 byte length for list row chat previews.
const ListPreviewBytes = 120

// chatPreviewsFromIO returns truncated first-user and last-AI text from chat-shaped
// inputs/outputs (OpenAI messages[], LangChain serialized messages, plain content).
func chatPreviewsFromIO(inputs, outputs any) (firstUser, lastAI string) {
	for _, blob := range []any{inputs, outputs} {
		for _, raw := range collectChatMessages(blob) {
			role, content := peekChatRoleContent(raw)
			content = strings.TrimSpace(content)
			if content == "" {
				continue
			}
			switch role {
			case "user", "human":
				if firstUser == "" {
					firstUser = truncateListPreview(content)
				}
			case "assistant", "ai":
				lastAI = truncateListPreview(content)
			}
		}
	}
	return firstUser, lastAI
}

func truncateListPreview(s string) string {
	return textsafe.StripNUL(textsafe.TruncateUTF8String(s, ListPreviewBytes))
}

func collectChatMessages(blob any) []any {
	if blob == nil {
		return nil
	}
	switch t := blob.(type) {
	case map[string]any:
		if msgs, ok := t["messages"].([]any); ok {
			return msgs
		}
		// generations[][].message or generations[][]generation.message (LLM result)
		if gens, ok := t["generations"].([]any); ok {
			var out []any
			for _, g := range gens {
				switch gm := g.(type) {
				case map[string]any:
					if m := gm["message"]; m != nil {
						out = append(out, m)
					}
				case []any:
					for _, x := range gm {
						if xm, ok := x.(map[string]any); ok {
							if m := xm["message"]; m != nil {
								out = append(out, m)
							}
						}
					}
				}
			}
			return out
		}
		// single message-shaped object
		if _, hasContent := t["content"]; hasContent {
			if _, hasRole := t["role"]; hasRole {
				return []any{t}
			}
			if _, hasType := t["type"]; hasType {
				return []any{t}
			}
			if _, hasLC := t["lc"]; hasLC {
				return []any{t}
			}
		}
	case []any:
		return t
	case string:
		var v any
		if err := json.Unmarshal([]byte(t), &v); err == nil {
			return collectChatMessages(v)
		}
	}
	return nil
}

func peekChatRoleContent(raw any) (role, content string) {
	m, ok := raw.(map[string]any)
	if !ok {
		return "", ""
	}
	// Shallow copy + unwrap LC constructor
	mm := make(map[string]any, len(m)+4)
	for k, v := range m {
		mm[k] = v
	}
	unwrapChatLC(mm)

	role = strings.ToLower(strings.TrimSpace(asString(mm["role"])))
	if role == "" {
		role = strings.ToLower(strings.TrimSpace(asString(mm["type"])))
	}
	role = normalizeChatRole(role)
	content = flattenChatContent(mm["content"])
	return role, content
}

func unwrapChatLC(m map[string]any) {
	lcID := m["id"]
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
		if r := asString(body["type"]); r != "" && !strings.EqualFold(r, "constructor") {
			m["role"] = r
		} else if r := asString(body["role"]); r != "" {
			m["role"] = r
		}
	}
	if r := roleFromChatLCID(lcID); r != "" {
		cur := strings.ToLower(asString(m["role"]))
		if cur == "" || cur == "constructor" {
			m["role"] = r
		}
	}
}

func roleFromChatLCID(id any) string {
	var last string
	switch t := id.(type) {
	case []any:
		if len(t) == 0 {
			return ""
		}
		last = asString(t[len(t)-1])
	case []string:
		if len(t) == 0 {
			return ""
		}
		last = t[len(t)-1]
	case string:
		last = t
		if i := strings.LastIndex(last, "."); i >= 0 {
			last = last[i+1:]
		}
	default:
		return ""
	}
	return normalizeChatRole(last)
}

func normalizeChatRole(role string) string {
	r := strings.ToLower(strings.TrimSpace(role))
	r = strings.TrimSuffix(r, "messagechunk")
	r = strings.TrimSuffix(r, "message")
	switch r {
	case "human", "user", "humanmessage":
		return "user"
	case "ai", "assistant", "aimessage":
		return "assistant"
	case "system", "systemmessage":
		return "system"
	case "tool", "function", "toolmessage", "functionmessage":
		return "tool"
	default:
		return r
	}
}

func flattenChatContent(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var b strings.Builder
		for _, part := range t {
			switch p := part.(type) {
			case string:
				b.WriteString(p)
			case map[string]any:
				if tx := asString(p["text"]); tx != "" {
					b.WriteString(tx)
				} else if asString(p["type"]) == "text" {
					b.WriteString(asString(p["text"]))
				}
			}
		}
		return b.String()
	case map[string]any:
		if tx := asString(t["text"]); tx != "" {
			return tx
		}
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// pickTraceChatPreviews chooses first_user / last_ai from denormalized run columns,
// falling back to scanning in-memory payloads when columns are empty (lazy).
func pickTraceChatPreviews(runs []map[string]any) (firstUser, lastAI string) {
	type timed struct {
		start string
		fu    string
		la    string
	}
	var rows []timed
	for _, r := range runs {
		fu := asString(r["first_user_preview"])
		la := asString(r["last_ai_preview"])
		if fu == "" && la == "" {
			// lazy fallback from payloads
			fu, la = chatPreviewsFromIO(r["inputs"], r["outputs"])
		}
		if fu == "" && la == "" {
			continue
		}
		rows = append(rows, timed{start: asString(r["start_time"]), fu: fu, la: la})
	}
	// first user = earliest non-empty; last AI = latest non-empty (start_time order)
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[j].start < rows[i].start {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
	for _, r := range rows {
		if firstUser == "" && r.fu != "" {
			firstUser = r.fu
		}
		if r.la != "" {
			lastAI = r.la
		}
	}
	return firstUser, lastAI
}
