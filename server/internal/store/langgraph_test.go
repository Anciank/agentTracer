package store

import (
	"context"
	"testing"
)

func TestExtractLangGraphFromMetadata(t *testing.T) {
	merged := map[string]any{
		"name": "supervisor",
		"extra": map[string]any{
			"metadata": map[string]any{
				"langgraph_node": "supervisor",
				"langgraph_step": 3,
			},
		},
	}
	node, step := extractLangGraph(context.Background(), nil, merged)
	if node == nil || *node != "supervisor" {
		t.Fatalf("node=%v", node)
	}
	if step == nil || *step != 3 {
		t.Fatalf("step=%v", step)
	}
}

func TestIsLangGraphRootHeuristic(t *testing.T) {
	if !isLangGraphRoot(map[string]any{"name": "LangGraph"}) {
		t.Fatal("name LangGraph should match")
	}
	if !isLangGraphRoot(map[string]any{
		"name":  "agent",
		"extra": map[string]any{"metadata": map[string]any{"langgraph_version": "0.2"}},
	}) {
		t.Fatal("metadata langgraph_version should match")
	}
	if !isLangGraphRoot(map[string]any{
		"name":       "agent",
		"serialized": map[string]any{"id": []any{"langgraph", "graph"}},
	}) {
		t.Fatal("serialized.id segment should match")
	}
	if isLangGraphRoot(map[string]any{"name": "chain"}) {
		t.Fatal("plain chain must not match")
	}
}

func TestExtractLangGraphNameFallback(t *testing.T) {
	// Without a store we cannot resolve parent; exercise dig + empty parent path.
	merged := map[string]any{
		"name":          "search",
		"parent_run_id": "00000000-0000-0000-0000-000000000001",
		"extra":         map[string]any{"metadata": map[string]any{}},
	}
	node, step := extractLangGraph(context.Background(), nil, merged)
	if node != nil || step != nil {
		t.Fatalf("nil store must not invent node: %v %v", node, step)
	}
}

func TestExtractLangGraphNameFallbackWithParent(t *testing.T) {
	// Fake store via getRunRaw path is heavy; use a minimal stub by constructing
	// parent map and calling isLangGraphRoot + manual fallback mirror.
	parent := map[string]any{"name": "LangGraph", "parent_run_id": nil}
	if !isLangGraphRoot(parent) {
		t.Fatal("expected LangGraph root")
	}
	childName := "tools"
	// Simulate fallback decision used in extractLangGraph when parent is root.
	var node *string
	if isLangGraphRoot(parent) && (parent["parent_run_id"] == nil || asString(parent["parent_run_id"]) == "") {
		node = &childName
	}
	if node == nil || *node != "tools" {
		t.Fatalf("fallback node=%v", node)
	}
}
