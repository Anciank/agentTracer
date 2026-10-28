package store

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// extractLangGraph denormalizes LangGraph node/step from run payload.
//
// Sources (in order):
//  1. extra.metadata.langgraph_node / langgraph_step (string / number)
//  2. Name fallback: if langgraph_node is still empty and this run is a *direct*
//     child of a LangGraph root, use the run's name.
//
// LangGraph root heuristic (cheapest sane check, first match wins):
//   - root name equals "LangGraph" (SDK default graph name), OR
//   - root extra.metadata contains any key prefixed "langgraph_" or
//     ls_example / checkpoint keys commonly set by LangGraph, OR
//   - root serialized.id / serialized.name contains "langgraph" (case-insensitive).
//
// path_fingerprint is intentionally NOT populated here (filled when a root ends, later).
func extractLangGraph(ctx context.Context, s *Store, merged map[string]any) (node *string, step *int) {
	if meta := digExtraMetadata(merged["extra"]); meta != nil {
		if n := asString(meta["langgraph_node"]); n != "" {
			node = &n
		}
		if v, ok := meta["langgraph_step"]; ok && v != nil {
			n := asInt(v)
			step = &n
		}
	}
	if node != nil {
		return node, step
	}
	// Name fallback for direct children of a LangGraph root.
	parentID := asUUID(merged["parent_run_id"])
	if parentID == nil || s == nil {
		return node, step
	}
	parent, err := s.GetRun(ctx, parentID.String())
	if err != nil || parent == nil {
		return node, step
	}
	// Direct child of root only: parent itself has no parent.
	if parent["parent_run_id"] != nil && asString(parent["parent_run_id"]) != "" {
		return node, step
	}
	if !isLangGraphRoot(parent) {
		return node, step
	}
	if n := asString(merged["name"]); n != "" {
		node = &n
	}
	return node, step
}

func digExtraMetadata(extra any) map[string]any {
	m, ok := extra.(map[string]any)
	if !ok || m == nil {
		return nil
	}
	meta, _ := m["metadata"].(map[string]any)
	return meta
}

func isLangGraphRoot(root map[string]any) bool {
	if asString(root["name"]) == "LangGraph" {
		return true
	}
	if meta := digExtraMetadata(root["extra"]); meta != nil {
		for k := range meta {
			lk := strings.ToLower(k)
			if strings.HasPrefix(lk, "langgraph_") ||
				lk == "checkpoint_ns" || lk == "checkpoint_id" ||
				strings.Contains(lk, "langgraph") {
				return true
			}
		}
	}
	if ser, ok := root["serialized"].(map[string]any); ok {
		for _, k := range []string{"id", "name", "lc"} {
			if s := asString(ser[k]); s != "" && strings.Contains(strings.ToLower(s), "langgraph") {
				return true
			}
		}
		// serialized.id is often a list of path segments
		if arr, ok := ser["id"].([]any); ok {
			for _, part := range arr {
				if s := asString(part); strings.Contains(strings.ToLower(s), "langgraph") {
					return true
				}
			}
		}
	}
	return false
}

// keep existing pointer helpers used by tests
var _ = uuid.Nil
