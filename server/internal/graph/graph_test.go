package graph

import (
	"fmt"
	"testing"
)

func TestCollapseAndFingerprint(t *testing.T) {
	keys := []string{"supervisor", "tools", "tools", "tools", "respond"}
	steps := CollapseConsecutive(keys)
	if len(steps) != 3 || steps[1].Repeat != 3 {
		t.Fatalf("steps=%v", steps)
	}
	fp := Fingerprint(steps)
	if fp != "supervisor>tools×3>respond" {
		t.Fatalf("fp=%q", fp)
	}
	back := ParseFingerprint(fp)
	if Fingerprint(back) != fp {
		t.Fatalf("roundtrip %v", back)
	}
	if LabelFromSteps(steps) != "supervisor → tools ×3 → respond" {
		t.Fatalf("label=%q", LabelFromSteps(steps))
	}
}

func TestVisitKey(t *testing.T) {
	k, lg, ok := VisitKey(" agent ", "x", "llm", false, true)
	if !ok || !lg || k != "agent" {
		t.Fatalf("lg got %q %v %v", k, lg, ok)
	}
	k, lg, ok = VisitKey("", "tools", "tool", false, true)
	if !ok || lg || k != "tools\x1ftool" {
		t.Fatalf("fb got %q %v %v", k, lg, ok)
	}
	_, _, ok = VisitKey("", "root", "chain", true, false)
	if ok {
		t.Fatal("root alone should not be a visit")
	}
	_, _, ok = VisitKey("", "nested", "llm", false, false)
	if ok {
		t.Fatal("nested non-LG should skip")
	}
}

func TestSelectTopNodesKeepsFailing(t *testing.T) {
	acc := map[string]*NodeAccum{}
	for i := 0; i < 20; i++ {
		k := fmt.Sprintf("n%d", i)
		acc[k] = &NodeAccum{Key: k, Count: 100 - i}
	}
	acc["rare_fail"] = &NodeAccum{Key: "rare_fail", Count: 1, ErrorCount: 1}
	nodes, other := SelectTopNodes(acc, 5)
	keys := map[string]bool{}
	for _, n := range nodes {
		keys[n["key"].(string)] = true
	}
	if !keys["rare_fail"] {
		t.Fatalf("failing node missing: %v", keys)
	}
	if other == nil {
		t.Fatal("expected other rollup")
	}
	if other["key"] != OtherKey {
		t.Fatalf("other key %v", other["key"])
	}
	if !other["truncated"].(bool) {
		t.Fatal("other must be truncated")
	}
}

func TestRecordTraceEdgesLoops(t *testing.T) {
	edges := map[string]*EdgeAccum{}
	RecordTraceEdges(edges, []string{"a", "b", "b", "b", "c"}, nil)
	loop := edges["b>b"]
	if loop == nil || !loop.Loop || loop.Count != 1 || len(loop.Repeats) != 1 || loop.Repeats[0] != 3 {
		t.Fatalf("loop=%+v", loop)
	}
	ab := edges["a>b"]
	bc := edges["b>c"]
	if ab == nil || bc == nil || ab.Loop || bc.Loop {
		t.Fatalf("ab=%v bc=%v", ab, bc)
	}
}

func TestSplitEdgesMinShare(t *testing.T) {
	edges := map[string]*EdgeAccum{
		"a>b": {From: "a", To: "b", Count: 50},
		"a>c": {From: "a", To: "c", Count: 1},
	}
	vis, hid := SplitEdges(edges, 1000, 0.01) // share 0.001 < 0.01
	if len(vis) != 1 || len(hid) != 1 {
		t.Fatalf("vis=%d hid=%d", len(vis), len(hid))
	}
}
