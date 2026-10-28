package graph

import (
	"math"
	"sort"
	"strconv"
)

// NodeAccum accumulates per-node visit stats.
type NodeAccum struct {
	Key        string
	FromLG     bool
	Count      int
	ErrorCount int
	Latencies  []float64 // finished visits only, ms
	InTok      int
	OutTok     int
	TotTok     int
}

func (n *NodeAccum) toStats() map[string]any {
	kind := KindFor(n.Key, n.FromLG)
	var p50, p95 any
	if len(n.Latencies) > 0 {
		p50 = percentile(n.Latencies, 0.50)
		p95 = percentile(n.Latencies, 0.95)
	}
	er := 0.0
	if n.Count > 0 {
		er = float64(n.ErrorCount) / float64(n.Count)
	}
	return map[string]any{
		"key": n.Key, "label": LabelFor(n.Key), "run_type": RunTypeFor(n.Key, kind),
		"kind": kind, "count": n.Count, "error_count": n.ErrorCount, "error_rate": round4(er),
		"p50_ms": p50, "p95_ms": p95,
		"tokens":    map[string]any{"input": n.InTok, "output": n.OutTok, "total": n.TotTok},
		"truncated": false,
	}
}

// EdgeAccum accumulates transitions.
type EdgeAccum struct {
	From, To   string
	Count      int // number of traces (or occurrences) contributing
	ErrorCount int
	GapMs      []float64
	// Per-trace repeat for loop edges (length of consecutive run).
	Repeats []int
	Loop    bool
}

func (e *EdgeAccum) toStats(traceCount int) map[string]any {
	share := 0.0
	if traceCount > 0 {
		share = float64(e.Count) / float64(traceCount)
	}
	var avg, p95 any
	if len(e.GapMs) > 0 {
		sum := 0.0
		for _, v := range e.GapMs {
			sum += v
		}
		avg = int(math.Round(sum / float64(len(e.GapMs))))
		p95 = percentile(e.GapMs, 0.95)
	}
	var repAvg, repMax any
	if e.Loop && len(e.Repeats) > 0 {
		s := 0
		mx := 0
		for _, r := range e.Repeats {
			s += r
			if r > mx {
				mx = r
			}
		}
		repAvg = round2(float64(s) / float64(len(e.Repeats)))
		repMax = mx
	}
	return map[string]any{
		"from": e.From, "to": e.To, "count": e.Count, "share": round4(share),
		"avg_ms": avg, "p95_ms": p95, "error_count": e.ErrorCount,
		"loop": e.Loop, "repeat_avg": repAvg, "repeat_max": repMax,
	}
}

// RecordTraceEdges walks visits and updates edge map.
// visits: ordered node keys; errs[i] whether visit i errored; starts/ends ms offsets optional (len==0 skip gaps).
func RecordTraceEdges(edges map[string]*EdgeAccum, visits []string, errored []bool) {
	if len(visits) == 0 {
		return
	}
	i := 0
	for i < len(visits) {
		j := i + 1
		for j < len(visits) && visits[j] == visits[i] {
			j++
		}
		runLen := j - i
		if runLen > 1 {
			k := visits[i] + ">" + visits[i]
			e := edges[k]
			if e == nil {
				e = &EdgeAccum{From: visits[i], To: visits[i], Loop: true}
				edges[k] = e
			}
			e.Count++
			e.Repeats = append(e.Repeats, runLen)
			for t := i; t < j; t++ {
				if t < len(errored) && errored[t] {
					e.ErrorCount++
					break
				}
			}
		}
		if j < len(visits) {
			k := visits[i] + ">" + visits[j]
			e := edges[k]
			if e == nil {
				e = &EdgeAccum{From: visits[i], To: visits[j], Loop: false}
				edges[k] = e
			}
			e.Count++
			if j < len(errored) && errored[j] {
				e.ErrorCount++
			}
		}
		i = j
	}
}

// SelectTopNodes picks topN by (error_count desc, count desc, key asc),
// always including every node with error_count>0. Remainder → other rollup.
func SelectTopNodes(accums map[string]*NodeAccum, topN int) (nodes []map[string]any, other map[string]any) {
	if topN <= 0 {
		topN = 15
	}
	list := make([]*NodeAccum, 0, len(accums))
	for _, a := range accums {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].ErrorCount != list[j].ErrorCount {
			return list[i].ErrorCount > list[j].ErrorCount
		}
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return list[i].Key < list[j].Key
	})

	keep := map[string]bool{}
	for i, a := range list {
		if i < topN || a.ErrorCount > 0 {
			keep[a.Key] = true
		}
	}
	kept := make([]*NodeAccum, 0, len(keep))
	rolled := make([]*NodeAccum, 0)
	for _, a := range list {
		if keep[a.Key] {
			kept = append(kept, a)
		} else {
			rolled = append(rolled, a)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].ErrorCount != kept[j].ErrorCount {
			return kept[i].ErrorCount > kept[j].ErrorCount
		}
		if kept[i].Count != kept[j].Count {
			return kept[i].Count > kept[j].Count
		}
		return kept[i].Key < kept[j].Key
	})
	nodes = make([]map[string]any, 0, len(kept))
	for _, a := range kept {
		nodes = append(nodes, a.toStats())
	}
	if len(rolled) == 0 {
		return nodes, nil
	}
	ot := &NodeAccum{Key: OtherKey}
	keys := make([]string, 0, len(rolled))
	for _, a := range rolled {
		keys = append(keys, a.Key)
		ot.Count += a.Count
		ot.ErrorCount += a.ErrorCount
		ot.InTok += a.InTok
		ot.OutTok += a.OutTok
		ot.TotTok += a.TotTok
		ot.Latencies = append(ot.Latencies, a.Latencies...)
	}
	sort.Strings(keys)
	stats := ot.toStats()
	stats["kind"] = "other"
	stats["label"] = formatMore(len(keys))
	stats["truncated"] = true
	stats["run_type"] = nil
	stats["rolled_keys"] = keys
	return nodes, stats
}

func formatMore(n int) string {
	return strconv.Itoa(n) + " more"
}

// SplitEdges partitions by min share. Returns visible, hidden (both sorted).
func SplitEdges(edges map[string]*EdgeAccum, traceCount int, minShare float64) (visible, hidden []map[string]any) {
	all := make([]map[string]any, 0, len(edges))
	for _, e := range edges {
		all = append(all, e.toStats(traceCount))
	}
	sort.Slice(all, func(i, j int) bool {
		ci, _ := all[i]["count"].(int)
		cj, _ := all[j]["count"].(int)
		if ci != cj {
			return ci > cj
		}
		fi, _ := all[i]["from"].(string)
		fj, _ := all[j]["from"].(string)
		if fi != fj {
			return fi < fj
		}
		ti, _ := all[i]["to"].(string)
		tj, _ := all[j]["to"].(string)
		return ti < tj
	})
	for _, e := range all {
		share, _ := e["share"].(float64)
		if minShare > 0 && share < minShare {
			hidden = append(hidden, e)
		} else {
			visible = append(visible, e)
		}
	}
	if visible == nil {
		visible = []map[string]any{}
	}
	if hidden == nil {
		hidden = []map[string]any{}
	}
	return visible, hidden
}

func percentile(vals []float64, p float64) int {
	if len(vals) == 0 {
		return 0
	}
	cp := append([]float64(nil), vals...)
	sort.Float64s(cp)
	if len(cp) == 1 {
		return int(math.Round(cp[0]))
	}
	idx := p * float64(len(cp)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return int(math.Round(cp[lo]))
	}
	w := idx - float64(lo)
	return int(math.Round(cp[lo]*(1-w) + cp[hi]*w))
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
