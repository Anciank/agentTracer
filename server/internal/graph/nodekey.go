package graph

import (
	"strings"
)

const (
	KeySep       = "\x1f"
	OtherKey     = "__other__"
	RootFallback = "__root__" + KeySep + "chain"
)

// NodeKey returns the stable aggregation key for a run visit.
// LangGraph: trimmed langgraph_node. Fallback: name + KeySep + run_type.
// Empty LG node is treated as absent (caller should use fallback or skip).
func NormalizeLGNode(raw string) string {
	return strings.TrimSpace(raw)
}

func FallbackKey(name, runType string) string {
	name = strings.TrimSpace(name)
	runType = strings.TrimSpace(runType)
	if runType == "" {
		runType = "chain"
	}
	if name == "" {
		name = "unnamed"
	}
	return name + KeySep + runType
}

func RootOnlyKey() string { return RootFallback }

// LabelFor returns the display label for a node key.
func LabelFor(key string) string {
	if key == OtherKey {
		return OtherKey
	}
	if i := strings.IndexByte(key, KeySep[0]); i >= 0 {
		return key[:i]
	}
	return key
}

// RunTypeFor returns run_type for fallback keys; empty for LG keys.
func RunTypeFor(key string, kind string) any {
	if kind != "run" {
		return nil
	}
	if i := strings.IndexByte(key, KeySep[0]); i >= 0 && i+1 < len(key) {
		return key[i+1:]
	}
	return "chain"
}

// KindFor classifies a key.
func KindFor(key string, fromLG bool) string {
	if key == OtherKey {
		return "other"
	}
	if fromLG {
		return "langgraph"
	}
	return "run"
}

// VisitKey picks node_key + whether it came from langgraph_node.
// lgNode is the denormalized column (may be empty). isDirectChild means
// parent is a root (or this is examined as a top-level child). isRoot means
// the run itself is a root.
func VisitKey(lgNode, name, runType string, isRoot, isDirectChild bool) (key string, fromLG bool, ok bool) {
	if n := NormalizeLGNode(lgNode); n != "" {
		return n, true, true
	}
	if isRoot {
		// Root-only traces contribute a synthetic visit when no children exist;
		// callers decide. Prefer not counting the root as a visit when children
		// supply the path.
		return "", false, false
	}
	if isDirectChild {
		return FallbackKey(name, runType), false, true
	}
	return "", false, false
}
