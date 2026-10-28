package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Anciank/agentTracer/server/internal/graph"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrProjectNotFound is returned when a UI project id/name does not exist.
var ErrProjectNotFound = errors.New("project not found")

// GraphOpts controls GET .../graph aggregation.
type GraphOpts struct {
	Project      string
	Window       string // 1h|24h|7d
	From, To     *time.Time
	TopN         int
	MinEdgeShare float64
	Compare      bool
}

// PathsOpts controls GET .../paths.
type PathsOpts struct {
	Project  string
	Window   string
	From, To *time.Time
	Limit    int
}

// RefreshPathFingerprint recomputes path_fingerprint on the root when the
// trace has a finished root (end_time set). Called after every UpsertRun that
// touches a run in the trace so late child PATCHes refresh the fingerprint
// (adopt-on-arrival for unfinished roots is out of scope; we only write when
// the root already has end_time, or this upsert just set it).
func (s *Store) RefreshPathFingerprint(ctx context.Context, traceID uuid.UUID) error {
	var rootID, sessionID uuid.UUID
	var rootEnd, rootStart *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id, session_id, end_time, start_time FROM runs
		WHERE trace_id=$1 AND parent_run_id IS NULL
		ORDER BY start_time ASC NULLS LAST
		LIMIT 1`, traceID).Scan(&rootID, &sessionID, &rootEnd, &rootStart)
	if err != nil {
		return nil // no root yet
	}
	if rootEnd == nil {
		return nil
	}
	visits, err := s.loadTraceVisits(ctx, traceID)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(visits))
	for _, v := range visits {
		keys = append(keys, v.key)
	}
	fp := graph.FingerprintFromKeys(keys)
	if len(keys) == 0 {
		fp = graph.FingerprintFromKeys([]string{graph.RootOnlyKey()})
	}
	if _, err := s.pool.Exec(ctx, `UPDATE runs SET path_fingerprint=$1, updated_at=now() WHERE id=$2`, fp, rootID); err != nil {
		return err
	}
	start := time.Now().UTC()
	if rootStart != nil {
		start = *rootStart
	}
	return s.upsertTraceGraphCache(ctx, traceID, sessionID, start, visits)
}

func (s *Store) computeFingerprint(ctx context.Context, traceID uuid.UUID) (string, error) {
	visits, err := s.loadTraceVisits(ctx, traceID)
	if err != nil {
		return "", err
	}
	keys := make([]string, 0, len(visits))
	for _, v := range visits {
		keys = append(keys, v.key)
	}
	if len(keys) == 0 {
		return graph.FingerprintFromKeys([]string{graph.RootOnlyKey()}), nil
	}
	return graph.FingerprintFromKeys(keys), nil
}

func (s *Store) upsertTraceGraphCache(ctx context.Context, traceID, sessionID uuid.UUID, rootStart time.Time, visits []visitKey) error {
	n := len(visits)
	keys := make([]string, n)
	fromLG := make([]bool, n)
	errs := make([]bool, n)
	lats := make([]float64, n)
	ins := make([]int32, n)
	outs := make([]int32, n)
	tots := make([]int32, n)
	for i, v := range visits {
		keys[i] = v.key
		fromLG[i] = v.fromLG
		errs[i] = v.errored
		lats[i] = v.latMs
		ins[i] = int32(v.in)
		outs[i] = int32(v.out)
		tots[i] = int32(v.tot)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO trace_graph_cache (
			trace_id, session_id, root_start,
			node_keys, node_from_lg, node_errors, node_lat_ms, node_in, node_out, node_tot, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now())
		ON CONFLICT (trace_id) DO UPDATE SET
			session_id=EXCLUDED.session_id,
			root_start=EXCLUDED.root_start,
			node_keys=EXCLUDED.node_keys,
			node_from_lg=EXCLUDED.node_from_lg,
			node_errors=EXCLUDED.node_errors,
			node_lat_ms=EXCLUDED.node_lat_ms,
			node_in=EXCLUDED.node_in,
			node_out=EXCLUDED.node_out,
			node_tot=EXCLUDED.node_tot,
			updated_at=now()`,
		traceID, sessionID, rootStart, keys, fromLG, errs, lats, ins, outs, tots)
	return err
}

// BackfillGraphCache recomputes cache rows for finished roots missing a cache entry.
// limit<=0 means no limit. Returns number of traces written.
func (s *Store) BackfillGraphCache(ctx context.Context, limit int) (int, error) {
	q := `
		SELECT r.trace_id FROM runs r
		LEFT JOIN trace_graph_cache c ON c.trace_id = r.trace_id
		WHERE r.parent_run_id IS NULL AND r.end_time IS NOT NULL AND r.trace_id IS NOT NULL
			AND c.trace_id IS NULL
		ORDER BY r.start_time DESC NULLS LAST`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	n := 0
	for _, id := range ids {
		if err := s.RefreshPathFingerprint(ctx, id); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Store) loadCachedVisitsInWindow(ctx context.Context, sessionID uuid.UUID, from, to time.Time) (map[uuid.UUID][]visitKey, int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT trace_id, node_keys, node_from_lg, node_errors, node_lat_ms, node_in, node_out, node_tot
		FROM trace_graph_cache
		WHERE session_id=$1 AND root_start >= $2 AND root_start < $3`, sessionID, from, to)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := map[uuid.UUID][]visitKey{}
	for rows.Next() {
		var tid uuid.UUID
		var keys []string
		var fromLG, errs []bool
		var lats []float64
		var ins, outs, tots []int32
		if err := rows.Scan(&tid, &keys, &fromLG, &errs, &lats, &ins, &outs, &tots); err != nil {
			return nil, 0, err
		}
		n := len(keys)
		visits := make([]visitKey, n)
		for i := 0; i < n; i++ {
			v := visitKey{key: keys[i]}
			if i < len(fromLG) {
				v.fromLG = fromLG[i]
			}
			if i < len(errs) {
				v.errored = errs[i]
			}
			if i < len(lats) {
				v.latMs = lats[i]
			}
			if i < len(ins) {
				v.in = int(ins[i])
			}
			if i < len(outs) {
				v.out = int(outs[i])
			}
			if i < len(tots) {
				v.tot = int(tots[i])
			}
			visits[i] = v
		}
		out[tid] = visits
	}
	return out, len(out), nil
}

type visitKey struct {
	key          string
	fromLG       bool
	errored      bool
	latMs        float64
	in, out, tot int
	start        *time.Time
}

func (s *Store) loadTraceVisits(ctx context.Context, traceID uuid.UUID) ([]visitKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, COALESCE(parent_run_id, '00000000-0000-0000-0000-000000000000'::uuid),
			parent_run_id IS NOT NULL,
			name, run_type, langgraph_node, langgraph_step, dotted_order,
			start_time, end_time, error, input_tokens, output_tokens, total_tokens, status
		FROM runs WHERE trace_id=$1
		ORDER BY langgraph_step NULLS LAST, dotted_order ASC, start_time ASC NULLS LAST`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type raw struct {
		id, parent                    uuid.UUID
		hasParent                     bool
		name, runType, dotted, status string
		lgNode                        *string
		lgStep                        *int
		start, end                    *time.Time
		errStr                        *string
		in, out, tot                  int
	}
	var all []raw
	roots := map[uuid.UUID]bool{}
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.id, &r.parent, &r.hasParent, &r.name, &r.runType, &r.lgNode, &r.lgStep,
			&r.dotted, &r.start, &r.end, &r.errStr, &r.in, &r.out, &r.tot, &r.status); err != nil {
			return nil, err
		}
		all = append(all, r)
		if !r.hasParent {
			roots[r.id] = true
		}
	}
	out := make([]visitKey, 0, len(all))
	for _, r := range all {
		lg := ""
		if r.lgNode != nil {
			lg = *r.lgNode
		}
		isRoot := !r.hasParent
		isDirect := r.hasParent && roots[r.parent]
		key, fromLG, ok := graph.VisitKey(lg, r.name, r.runType, isRoot, isDirect)
		if !ok {
			continue
		}
		vk := visitKey{key: key, fromLG: fromLG, in: r.in, out: r.out, tot: r.tot, start: r.start}
		if r.errStr != nil && *r.errStr != "" {
			vk.errored = true
		}
		if r.start != nil && r.end != nil {
			vk.latMs = float64(r.end.Sub(*r.start).Milliseconds())
		}
		out = append(out, vk)
	}
	return out, nil
}

func (s *Store) loadVisitsForTraces(ctx context.Context, traceIDs []uuid.UUID) (map[uuid.UUID][]visitKey, error) {
	out := map[uuid.UUID][]visitKey{}
	if len(traceIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT trace_id, id, COALESCE(parent_run_id, '00000000-0000-0000-0000-000000000000'::uuid),
			parent_run_id IS NOT NULL,
			name, run_type, langgraph_node, langgraph_step, dotted_order,
			start_time, end_time, error, input_tokens, output_tokens, total_tokens, status
		FROM runs WHERE trace_id = ANY($1)
		ORDER BY trace_id, langgraph_step NULLS LAST, dotted_order ASC, start_time ASC NULLS LAST`, traceIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type raw struct {
		traceID, id, parent           uuid.UUID
		hasParent                     bool
		name, runType, dotted, status string
		lgNode                        *string
		lgStep                        *int
		start, end                    *time.Time
		errStr                        *string
		in, out, tot                  int
	}
	var all []raw
	roots := map[uuid.UUID]map[uuid.UUID]bool{} // trace -> root ids
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.traceID, &r.id, &r.parent, &r.hasParent, &r.name, &r.runType, &r.lgNode, &r.lgStep,
			&r.dotted, &r.start, &r.end, &r.errStr, &r.in, &r.out, &r.tot, &r.status); err != nil {
			return nil, err
		}
		all = append(all, r)
		if !r.hasParent {
			m := roots[r.traceID]
			if m == nil {
				m = map[uuid.UUID]bool{}
				roots[r.traceID] = m
			}
			m[r.id] = true
		}
	}
	for _, r := range all {
		lg := ""
		if r.lgNode != nil {
			lg = *r.lgNode
		}
		isRoot := !r.hasParent
		isDirect := r.hasParent && roots[r.traceID] != nil && roots[r.traceID][r.parent]
		key, fromLG, ok := graph.VisitKey(lg, r.name, r.runType, isRoot, isDirect)
		if !ok {
			continue
		}
		vk := visitKey{key: key, fromLG: fromLG, in: r.in, out: r.out, tot: r.tot, start: r.start}
		if r.errStr != nil && *r.errStr != "" {
			vk.errored = true
		}
		if r.start != nil && r.end != nil {
			vk.latMs = float64(r.end.Sub(*r.start).Milliseconds())
		}
		out[r.traceID] = append(out[r.traceID], vk)
	}
	return out, nil
}

func parseWindow(window string, from, to *time.Time) (time.Time, time.Time, string, error) {
	now := time.Now().UTC()
	if from != nil && to != nil {
		w := "custom"
		if window != "" {
			w = window
		}
		return from.UTC(), to.UTC(), w, nil
	}
	switch window {
	case "", "24h":
		return now.Add(-24 * time.Hour), now, "24h", nil
	case "1h":
		return now.Add(-1 * time.Hour), now, "1h", nil
	case "7d":
		return now.Add(-7 * 24 * time.Hour), now, "7d", nil
	default:
		return time.Time{}, time.Time{}, "", fmt.Errorf("window must be 1h|24h|7d")
	}
}

// UIGraph aggregates node/edge stats for a project window.
func (s *Store) UIGraph(ctx context.Context, opts GraphOpts) (map[string]any, error) {
	from, to, win, err := parseWindow(opts.Window, opts.From, opts.To)
	if err != nil {
		return nil, err
	}
	if opts.TopN <= 0 {
		opts.TopN = 15
	}
	if opts.MinEdgeShare <= 0 {
		opts.MinEdgeShare = 0.01
	}
	sessionID, err := s.LookupSessionID(ctx, opts.Project)
	if err != nil {
		return nil, err
	}
	if sessionID == uuid.Nil {
		return nil, ErrProjectNotFound
	}

	// Roots in window.
	rootRows, err := s.pool.Query(ctx, `
		SELECT id, trace_id FROM runs
		WHERE session_id=$1 AND parent_run_id IS NULL AND trace_id IS NOT NULL
			AND start_time >= $2 AND start_time < $3`, sessionID, from, to)
	if err != nil {
		return nil, err
	}
	var rootTraceIDs []uuid.UUID
	for rootRows.Next() {
		var id, tid uuid.UUID
		if err := rootRows.Scan(&id, &tid); err != nil {
			rootRows.Close()
			return nil, err
		}
		rootTraceIDs = append(rootTraceIDs, tid)
	}
	rootRows.Close()
	traceCount := len(rootTraceIDs)

	nodesAcc := map[string]*graph.NodeAccum{}
	edgesAcc := map[string]*graph.EdgeAccum{}
	rootOnly := 0

	// Prefer per-trace visit cache (written when the root ends). Fall back to
	// live runs scan for any roots not yet cached (e.g. mid-backfill).
	byTrace, cachedN, err := s.loadCachedVisitsInWindow(ctx, sessionID, from, to)
	if err != nil {
		return nil, err
	}
	if cachedN < len(rootTraceIDs) {
		missing := make([]uuid.UUID, 0, len(rootTraceIDs)-cachedN)
		for _, tid := range rootTraceIDs {
			if _, ok := byTrace[tid]; !ok {
				missing = append(missing, tid)
			}
		}
		live, err := s.loadVisitsForTraces(ctx, missing)
		if err != nil {
			return nil, err
		}
		for tid, visits := range live {
			byTrace[tid] = visits
		}
	}
	for _, tid := range rootTraceIDs {
		visits := byTrace[tid]
		if len(visits) == 0 {
			rootOnly++
			k := graph.RootOnlyKey()
			a := nodesAcc[k]
			if a == nil {
				a = &graph.NodeAccum{Key: k, FromLG: false}
				nodesAcc[k] = a
			}
			a.Count++
			continue
		}
		keys := make([]string, 0, len(visits))
		errs := make([]bool, 0, len(visits))
		for _, v := range visits {
			keys = append(keys, v.key)
			errs = append(errs, v.errored)
			a := nodesAcc[v.key]
			if a == nil {
				a = &graph.NodeAccum{Key: v.key, FromLG: v.fromLG}
				nodesAcc[v.key] = a
			}
			a.FromLG = a.FromLG || v.fromLG
			a.Count++
			if v.errored {
				a.ErrorCount++
			}
			if v.latMs > 0 {
				a.Latencies = append(a.Latencies, v.latMs)
			}
			a.InTok += v.in
			a.OutTok += v.out
			a.TotTok += v.tot
		}
		graph.RecordTraceEdges(edgesAcc, keys, errs)
	}

	nodes, other := graph.SelectTopNodes(nodesAcc, opts.TopN)
	if other != nil {
		nodes = append(nodes, other)
	}
	// Remap edges whose endpoints were rolled into __other__.
	kept := map[string]bool{}
	for _, n := range nodes {
		kept[n["key"].(string)] = true
	}
	rolled := map[string]bool{}
	if other != nil {
		if rk, ok := other["rolled_keys"].([]string); ok {
			for _, k := range rk {
				rolled[k] = true
			}
		}
	}
	remap := func(k string) string {
		if rolled[k] {
			return graph.OtherKey
		}
		return k
	}
	remapped := map[string]*graph.EdgeAccum{}
	for _, e := range edgesAcc {
		f, t := remap(e.From), remap(e.To)
		if !kept[f] || !kept[t] {
			// endpoint not in nodes (shouldn't happen if other present)
			if !kept[f] {
				f = graph.OtherKey
			}
			if !kept[t] {
				t = graph.OtherKey
			}
		}
		k := f + ">" + t
		ex := remapped[k]
		if ex == nil {
			ex = &graph.EdgeAccum{From: f, To: t, Loop: f == t && e.Loop}
			remapped[k] = ex
		}
		ex.Count += e.Count
		ex.ErrorCount += e.ErrorCount
		ex.GapMs = append(ex.GapMs, e.GapMs...)
		ex.Repeats = append(ex.Repeats, e.Repeats...)
		if e.Loop && f == t {
			ex.Loop = true
		}
	}
	visible, hidden := graph.SplitEdges(remapped, traceCount, opts.MinEdgeShare)
	for _, n := range nodes {
		delete(n, "rolled_keys") // internal only; not in NodeStats shape
	}

	generated := time.Now().UTC()
	out := map[string]any{
		"project": opts.Project,
		"window": map[string]any{
			"from":   from.Format(time.RFC3339Nano),
			"to":     to.Format(time.RFC3339Nano),
			"preset": win,
		},
		"trace_count":  traceCount,
		"nodes":        nodes,
		"edges":        visible,
		"edges_hidden": hidden,
		"generated_at": generated.Format(time.RFC3339Nano),
	}
	if opts.Compare {
		prevDur := to.Sub(from)
		prevFrom, prevTo := from.Add(-prevDur), from
		prev, err := s.UIGraph(ctx, GraphOpts{
			Project: opts.Project, From: &prevFrom, To: &prevTo,
			TopN: opts.TopN, MinEdgeShare: opts.MinEdgeShare, Window: "custom",
		})
		if err == nil {
			out["compare"] = buildCompare(out, prev)
		}
	}
	_ = rootOnly
	return out, nil
}

func buildCompare(cur, prev map[string]any) map[string]any {
	prevNodes := map[string]map[string]any{}
	if ns, ok := prev["nodes"].([]map[string]any); ok {
		for _, n := range ns {
			prevNodes[n["key"].(string)] = n
		}
	} else if ns, ok := prev["nodes"].([]any); ok {
		for _, x := range ns {
			if n, ok := x.(map[string]any); ok {
				prevNodes[n["key"].(string)] = n
			}
		}
	}
	type delta struct {
		Key        string   `json:"key"`
		DCount     int      `json:"d_count"`
		DErrorRate float64  `json:"d_error_rate"`
		DShare     *float64 `json:"d_share,omitempty"`
	}
	var nodeDeltas []map[string]any
	if ns, ok := cur["nodes"].([]map[string]any); ok {
		for _, n := range ns {
			k := n["key"].(string)
			p := prevNodes[k]
			dc := asInt(n["count"])
			der := 0.0
			if er, ok := n["error_rate"].(float64); ok {
				der = er
			}
			if p != nil {
				dc -= asInt(p["count"])
				if er, ok := p["error_rate"].(float64); ok {
					der -= er
				}
			}
			nodeDeltas = append(nodeDeltas, map[string]any{
				"key": k, "d_count": dc, "d_error_rate": graphRound4(der),
			})
		}
	}
	prevEdges := map[string]map[string]any{}
	collectEdges := func(src map[string]any, field string) {
		switch es := src[field].(type) {
		case []map[string]any:
			for _, e := range es {
				prevEdges[e["from"].(string)+">"+e["to"].(string)] = e
			}
		case []any:
			for _, x := range es {
				if e, ok := x.(map[string]any); ok {
					prevEdges[e["from"].(string)+">"+e["to"].(string)] = e
				}
			}
		}
	}
	collectEdges(prev, "edges")
	collectEdges(prev, "edges_hidden")

	var edgeDeltas []map[string]any
	addEdgeDelta := func(e map[string]any) {
		k := e["from"].(string) + ">" + e["to"].(string)
		p := prevEdges[k]
		dc := asInt(e["count"])
		var dShare float64
		if sh, ok := e["share"].(float64); ok {
			dShare = sh
		}
		if p != nil {
			dc -= asInt(p["count"])
			if sh, ok := p["share"].(float64); ok {
				dShare -= sh
			}
		}
		edgeDeltas = append(edgeDeltas, map[string]any{
			"from": e["from"], "to": e["to"], "d_count": dc, "d_share": graphRound4(dShare),
		})
	}
	switch es := cur["edges"].(type) {
	case []map[string]any:
		for _, e := range es {
			addEdgeDelta(e)
		}
	}
	switch es := cur["edges_hidden"].(type) {
	case []map[string]any:
		for _, e := range es {
			addEdgeDelta(e)
		}
	}
	return map[string]any{"nodes": nodeDeltas, "edges": edgeDeltas}
}

func graphRound4(v float64) float64 {
	return float64(int(v*1e4+0.5)) / 1e4
}

// UIPaths returns top path fingerprints for a project window.
func (s *Store) UIPaths(ctx context.Context, opts PathsOpts) (map[string]any, error) {
	from, to, win, err := parseWindow(opts.Window, opts.From, opts.To)
	if err != nil {
		return nil, err
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	sessionID, err := s.LookupSessionID(ctx, opts.Project)
	if err != nil {
		return nil, err
	}
	if sessionID == uuid.Nil {
		return nil, ErrProjectNotFound
	}

	rows, err := s.pool.Query(ctx, `
		SELECT path_fingerprint, COUNT(*)::int,
			COUNT(*) FILTER (WHERE error IS NOT NULL AND error <> '')::int
		FROM runs
		WHERE session_id=$1 AND parent_run_id IS NULL AND trace_id IS NOT NULL
			AND start_time >= $2 AND start_time < $3
			AND path_fingerprint IS NOT NULL AND path_fingerprint <> ''
		GROUP BY path_fingerprint
		ORDER BY COUNT(*) DESC, path_fingerprint ASC
		LIMIT $4`, sessionID, from, to, opts.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var total int
	type pathRow struct {
		fp              string
		count, errCount int
	}
	var list []pathRow
	for rows.Next() {
		var p pathRow
		if err := rows.Scan(&p.fp, &p.count, &p.errCount); err != nil {
			return nil, err
		}
		list = append(list, p)
		total += p.count
	}
	// Also count roots without fingerprint for share denominator.
	var allRoots int
	_ = s.pool.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM runs
		WHERE session_id=$1 AND parent_run_id IS NULL AND trace_id IS NOT NULL
			AND start_time >= $2 AND start_time < $3`, sessionID, from, to).Scan(&allRoots)
	if allRoots == 0 {
		allRoots = total
	}

	paths := make([]map[string]any, 0, len(list))
	for _, p := range list {
		steps := graph.ParseFingerprint(p.fp)
		share := 0.0
		er := 0.0
		if allRoots > 0 {
			share = float64(p.count) / float64(allRoots)
		}
		if p.count > 0 {
			er = float64(p.errCount) / float64(p.count)
		}
		stepMaps := make([]map[string]any, 0, len(steps))
		for _, st := range steps {
			stepMaps = append(stepMaps, map[string]any{"key": st.Key, "repeat": st.Repeat})
		}
		paths = append(paths, map[string]any{
			"fingerprint": p.fp,
			"label":       graph.LabelFromSteps(steps),
			"count":       p.count,
			"share":       graphRound4(share),
			"error_rate":  graphRound4(er),
			"steps":       stepMaps,
		})
	}
	sort.SliceStable(paths, func(i, j int) bool {
		return asInt(paths[i]["count"]) > asInt(paths[j]["count"])
	})
	return map[string]any{
		"project": opts.Project,
		"window": map[string]any{
			"from":   from.Format(time.RFC3339Nano),
			"to":     to.Format(time.RFC3339Nano),
			"preset": win,
		},
		"paths": paths,
	}, nil
}

// LookupSessionID resolves a project path id that may be either a session UUID or a
// session name. When the value parses as a UUID, an exact id match is preferred;
// if no row has that id, it falls back to treating the value as a name.
// Returns (uuid.Nil, nil) when nothing matches (callers that need 404 should check).
func (s *Store) LookupSessionID(ctx context.Context, idOrName string) (uuid.UUID, error) {
	if idOrName == "" {
		return uuid.Nil, nil
	}
	if uid, err := uuid.Parse(idOrName); err == nil {
		var id uuid.UUID
		err := s.pool.QueryRow(ctx, `SELECT id FROM sessions WHERE id=$1`, uid).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, err
		}
		// fall through to name match (session name might coincidentally be a UUID string)
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM sessions WHERE name=$1`, idOrName).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}
	return id, nil
}

func TraceVisitsMatch(visits []visitKey, nodeKey string, requireFail bool) bool {
	for _, v := range visits {
		if v.key == nodeKey {
			if !requireFail || v.errored {
				return true
			}
		}
	}
	return false
}

func TraceHasEdge(visits []visitKey, from, to string) bool {
	keys := make([]string, len(visits))
	for i, v := range visits {
		keys[i] = v.key
	}
	edges := map[string]*graph.EdgeAccum{}
	graph.RecordTraceEdges(edges, keys, nil)
	_, ok := edges[from+">"+to]
	return ok
}

// used by filters — silence unused import guard
var _ = strings.Contains
