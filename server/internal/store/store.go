package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Anciank/agentTracer/server/internal/ingest"
	"github.com/Anciank/agentTracer/server/internal/live"
)

type BlobStore interface {
	Put(ctx context.Context, key string, contentType string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, string, error)
	EnsureBucket(ctx context.Context) error
}

type Store struct {
	pool *pgxpool.Pool
	blob BlobStore
	hub  *live.Hub
}

func New(pool *pgxpool.Pool, blob BlobStore) *Store {
	return &Store{pool: pool, blob: blob, hub: live.NewHub()}
}

func (s *Store) Hub() *live.Hub { return s.hub }

func (s *Store) SetHub(h *live.Hub) { s.hub = h }

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func Migrate(ctx context.Context, pool *pgxpool.Pool, sql string) error {
	_, err := pool.Exec(ctx, sql)
	return err
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		return fmt.Sprint(v)
	}
}

func asUUID(v any) *uuid.UUID {
	s := strings.TrimSpace(asString(v))
	if s == "" || s == "<nil>" {
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}

func parseTime(v any) *time.Time {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case time.Time:
		tt := t.UTC()
		return &tt
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil
		}
		s = strings.ReplaceAll(s, "Z", "+00:00")
		layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999"}
		for _, layout := range layouts {
			if tt, err := time.Parse(layout, s); err == nil {
				tt = tt.UTC()
				return &tt
			}
		}
		return nil
	default:
		return parseTime(asString(v))
	}
}

func toJSON(v any) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

func toJSONOr(v any, def string) []byte {
	if v == nil {
		return []byte(def)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(def)
	}
	return b
}

func preview(v any, n int) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Store) EnsureSession(ctx context.Context, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM sessions WHERE name=$1`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return uuid.Nil, err
	}
	id = uuid.New()
	_, err = s.pool.Exec(ctx, `
		INSERT INTO sessions (id, name, description, start_time)
		VALUES ($1, $2, '', now())
		ON CONFLICT (name) DO NOTHING`, id, name)
	if err != nil {
		return uuid.Nil, err
	}
	err = s.pool.QueryRow(ctx, `SELECT id FROM sessions WHERE name=$1`, name).Scan(&id)
	return id, err
}

func (s *Store) CreateSession(ctx context.Context, payload map[string]any) (map[string]any, error) {
	name := asString(payload["name"])
	if name != "" {
		var existing uuid.UUID
		err := s.pool.QueryRow(ctx, `SELECT id FROM sessions WHERE name=$1`, name).Scan(&existing)
		if err == nil {
			if desc, ok := payload["description"]; ok && desc != nil {
				_, _ = s.pool.Exec(ctx, `UPDATE sessions SET description=$1 WHERE id=$2`, asString(desc), existing)
			}
			return s.GetSession(ctx, existing.String())
		}
	}
	id := uuid.New()
	if u := asUUID(payload["id"]); u != nil {
		id = *u
	}
	start := parseTime(payload["start_time"])
	if start == nil {
		now := time.Now().UTC()
		start = &now
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (id, name, description, start_time, end_time, extra)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name,
			description=EXCLUDED.description,
			extra=EXCLUDED.extra,
			end_time=COALESCE(EXCLUDED.end_time, sessions.end_time)`,
		id, name, asString(payload["description"]), start, parseTime(payload["end_time"]), toJSONOr(payload["extra"], "{}"))
	if err != nil {
		return nil, err
	}
	return s.GetSession(ctx, id.String())
}

func (s *Store) GetSession(ctx context.Context, id string) (map[string]any, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	var name, desc string
	var start time.Time
	var end *time.Time
	var extra []byte
	err = s.pool.QueryRow(ctx, `
		SELECT name, description, start_time, end_time, extra FROM sessions WHERE id=$1`, uid).
		Scan(&name, &desc, &start, &end, &extra)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"id": id, "name": name, "description": desc,
		"start_time": start.UTC().Format(time.RFC3339Nano),
		"tenant_id":  "00000000-0000-0000-0000-000000000000",
	}
	if end != nil {
		out["end_time"] = end.UTC().Format(time.RFC3339Nano)
	}
	if len(extra) > 0 {
		var e any
		_ = json.Unmarshal(extra, &e)
		out["extra"] = e
	}
	return out, nil
}

func (s *Store) ListSessions(ctx context.Context, limit int, name string) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id, name, description, start_time, end_time, extra FROM sessions`
	args := []any{}
	if name != "" {
		q += ` WHERE name=$1`
		args = append(args, name)
	}
	q += fmt.Sprintf(` ORDER BY start_time DESC LIMIT %d`, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id uuid.UUID
		var n, d string
		var start time.Time
		var end *time.Time
		var extra []byte
		if err := rows.Scan(&id, &n, &d, &start, &end, &extra); err != nil {
			return nil, err
		}
		m := map[string]any{
			"id": id.String(), "name": n, "description": d,
			"start_time": start.UTC().Format(time.RFC3339Nano),
			"tenant_id":  "00000000-0000-0000-0000-000000000000",
		}
		if end != nil {
			m["end_time"] = end.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, m)
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, rows.Err()
}

var mergedKeys = []string{
	"name", "run_type", "start_time", "end_time", "dotted_order", "status", "error",
	"inputs", "outputs", "events", "extra", "serialized", "tags",
	"parent_run_id", "trace_id", "reference_example_id", "session_id", "session_name",
}

func (s *Store) UpsertRun(ctx context.Context, payload map[string]any, op string) error {
	runID := asUUID(payload["id"])
	if runID == nil {
		return fmt.Errorf("run id required")
	}

	// Resolve session
	var sessionID *uuid.UUID
	if sid := asUUID(payload["session_id"]); sid != nil {
		sessionID = sid
	} else if sn := asString(payload["session_name"]); sn != "" {
		id, err := s.EnsureSession(ctx, sn)
		if err != nil {
			return err
		}
		sessionID = &id
	}

	existing, _ := s.getRunRaw(ctx, *runID)

	merged := map[string]any{}
	if existing != nil {
		for k, v := range existing {
			merged[k] = v
		}
	}
	for _, k := range mergedKeys {
		if v, ok := payload[k]; ok {
			if op == "patch" && v == nil {
				continue // null must not wipe
			}
			merged[k] = v
		}
	}
	// Always take id
	merged["id"] = runID.String()
	if sessionID != nil {
		merged["session_id"] = sessionID.String()
	} else if existing != nil {
		if sid, ok := existing["session_id"].(string); ok && sid != "" {
			sessionID = asUUID(sid)
		}
	}

	name := asString(merged["name"])
	runType := asString(merged["run_type"])
	if runType == "" {
		runType = "chain"
	}
	status := asString(merged["status"])
	if status == "" {
		status = "pending"
	}
	if merged["end_time"] != nil && status == "pending" {
		if asString(merged["error"]) != "" {
			status = "error"
		} else {
			status = "success"
		}
	}
	errorStr := asString(merged["error"])
	inTok, outTok, totTok, modelName := extractUsage(merged)
	lgNode, lgStep := extractLangGraph(ctx, s, merged)
	if lgNode == nil && existing != nil {
		if n := asString(existing["langgraph_node"]); n != "" {
			lgNode = &n
		}
	}
	if lgStep == nil && existing != nil {
		if v, ok := existing["langgraph_step"]; ok && v != nil {
			n := asInt(v)
			lgStep = &n
		}
	}
	dotted := asString(merged["dotted_order"])
	parentID := asUUID(merged["parent_run_id"])
	traceID := s.resolveTraceID(ctx, *runID, merged)
	merged["trace_id"] = traceID.String()
	refID := asUUID(merged["reference_example_id"])
	start := parseTime(merged["start_time"])
	end := parseTime(merged["end_time"])

	// Shallow-merge extra on patch
	if op == "patch" && existing != nil {
		if incoming, ok := payload["extra"].(map[string]any); ok {
			base := map[string]any{}
			if em, ok := existing["extra"].(map[string]any); ok {
				for k, v := range em {
					base[k] = v
				}
			}
			for k, v := range incoming {
				if v != nil {
					base[k] = v
				}
			}
			merged["extra"] = base
		}
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO runs (
			id, name, run_type, session_id, parent_run_id, dotted_order, trace_id,
			start_time, end_time, status, error, inputs, outputs, extra, events, tags,
			serialized, inputs_preview, outputs_preview, reference_example_id,
			input_tokens, output_tokens, total_tokens, model,
			langgraph_node, langgraph_step, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,now(),now()
		)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name,
			run_type=EXCLUDED.run_type,
			session_id=COALESCE(EXCLUDED.session_id, runs.session_id),
			parent_run_id=COALESCE(EXCLUDED.parent_run_id, runs.parent_run_id),
			dotted_order=CASE WHEN EXCLUDED.dotted_order='' THEN runs.dotted_order ELSE EXCLUDED.dotted_order END,
			trace_id=COALESCE(EXCLUDED.trace_id, runs.trace_id),
			start_time=COALESCE(EXCLUDED.start_time, runs.start_time),
			end_time=COALESCE(EXCLUDED.end_time, runs.end_time),
			status=EXCLUDED.status,
			error=CASE WHEN EXCLUDED.error='' THEN runs.error ELSE EXCLUDED.error END,
			inputs=COALESCE(EXCLUDED.inputs, runs.inputs),
			outputs=COALESCE(EXCLUDED.outputs, runs.outputs),
			extra=COALESCE(EXCLUDED.extra, runs.extra),
			events=COALESCE(EXCLUDED.events, runs.events),
			tags=COALESCE(EXCLUDED.tags, runs.tags),
			serialized=COALESCE(EXCLUDED.serialized, runs.serialized),
			inputs_preview=CASE WHEN EXCLUDED.inputs_preview='' THEN runs.inputs_preview ELSE EXCLUDED.inputs_preview END,
			outputs_preview=CASE WHEN EXCLUDED.outputs_preview='' THEN runs.outputs_preview ELSE EXCLUDED.outputs_preview END,
			reference_example_id=COALESCE(EXCLUDED.reference_example_id, runs.reference_example_id),
			input_tokens=CASE WHEN EXCLUDED.input_tokens=0 THEN runs.input_tokens ELSE EXCLUDED.input_tokens END,
			output_tokens=CASE WHEN EXCLUDED.output_tokens=0 THEN runs.output_tokens ELSE EXCLUDED.output_tokens END,
			total_tokens=CASE WHEN EXCLUDED.total_tokens=0 THEN runs.total_tokens ELSE EXCLUDED.total_tokens END,
			model=CASE WHEN EXCLUDED.model='' THEN runs.model ELSE EXCLUDED.model END,
			langgraph_node=COALESCE(EXCLUDED.langgraph_node, runs.langgraph_node),
			langgraph_step=COALESCE(EXCLUDED.langgraph_step, runs.langgraph_step),
			updated_at=now()`,
		*runID, name, runType, sessionID, parentID, dotted, traceID,
		start, end, status, errorStr,
		toJSON(merged["inputs"]), toJSON(merged["outputs"]), toJSONOr(merged["extra"], "{}"),
		toJSONOr(merged["events"], "[]"), toJSONOr(merged["tags"], "[]"), toJSON(merged["serialized"]),
		preview(merged["inputs"], 200), preview(merged["outputs"], 200), refID,
		inTok, outTok, totTok, modelName,
		lgNode, lgStep,
	)
	if err != nil {
		return err
	}
	s.publishRun(ctx, *runID, op)
	return nil
}

func (s *Store) publishRun(ctx context.Context, runID uuid.UUID, op string) {
	if s.hub == nil {
		return
	}
	run, err := s.GetRun(ctx, runID.String())
	if err != nil || run == nil {
		return
	}
	project := ""
	if sid, ok := run["session_id"].(string); ok && sid != "" {
		if sess, e := s.GetSession(ctx, sid); e == nil {
			project = asString(sess["name"])
		}
	}
	tid, _ := run["trace_id"].(string)
	if tid == "" {
		tid = runID.String()
	}
	s.hub.Publish(live.RunEvent{
		TraceID: tid,
		RunID:   runID.String(),
		Project: project,
		Op:      op,
		Run:     run,
	})
}

func (s *Store) getRunRaw(ctx context.Context, id uuid.UUID) (map[string]any, error) {
	return s.GetRun(ctx, id.String())
}

func (s *Store) GetRun(ctx context.Context, id string) (map[string]any, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id, name, run_type, session_id, parent_run_id, dotted_order, trace_id,
			start_time, end_time, status, error, inputs, outputs, extra, events, tags,
			serialized, inputs_preview, outputs_preview, reference_example_id,
			input_tokens, output_tokens, total_tokens, model,
			langgraph_node, langgraph_step
		FROM runs WHERE id=$1`, uid)
	return scanRun(row)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanRun(row scannable) (map[string]any, error) {
	var (
		id                                    uuid.UUID
		name, runType, dotted, status, errStr string
		inPrev, outPrev, model                string
		sessionID, parentID, traceID, refID   *uuid.UUID
		start, end                            *time.Time
		inputs, outputs, extra, events, tags  []byte
		serialized                            []byte
		inTok, outTok, totTok                 int
		lgNode                                *string
		lgStep                                *int
	)
	if err := row.Scan(&id, &name, &runType, &sessionID, &parentID, &dotted, &traceID,
		&start, &end, &status, &errStr, &inputs, &outputs, &extra, &events, &tags,
		&serialized, &inPrev, &outPrev, &refID, &inTok, &outTok, &totTok, &model,
		&lgNode, &lgStep); err != nil {
		return nil, err
	}
	out := map[string]any{
		"id": id.String(), "name": name, "run_type": runType,
		"dotted_order": dotted, "status": status, "error": nilIfEmpty(errStr),
		"inputs_preview": inPrev, "outputs_preview": outPrev,
		"input_tokens": inTok, "output_tokens": outTok, "total_tokens": totTok,
		"model": nilIfEmpty(model),
	}
	if lgNode != nil {
		out["langgraph_node"] = *lgNode
	}
	if lgStep != nil {
		out["langgraph_step"] = *lgStep
	}
	if sessionID != nil {
		out["session_id"] = sessionID.String()
	}
	if parentID != nil {
		out["parent_run_id"] = parentID.String()
	}
	if traceID != nil {
		out["trace_id"] = traceID.String()
	}
	if refID != nil {
		out["reference_example_id"] = refID.String()
	}
	if start != nil {
		out["start_time"] = start.UTC().Format(time.RFC3339Nano)
	}
	if end != nil {
		out["end_time"] = end.UTC().Format(time.RFC3339Nano)
	}
	setJSON(out, "inputs", inputs)
	setJSON(out, "outputs", outputs)
	setJSON(out, "extra", extra)
	setJSON(out, "events", events)
	setJSON(out, "tags", tags)
	setJSON(out, "serialized", serialized)
	return out, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func setJSON(out map[string]any, key string, raw []byte) {
	if raw == nil {
		return
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		out[key] = v
	}
}

func (s *Store) QueryRuns(ctx context.Context, opts QueryOpts) ([]map[string]any, error) {
	where := []string{"1=1"}
	args := []any{}
	n := 1
	if opts.Trace != "" {
		where = append(where, fmt.Sprintf("trace_id=$%d", n))
		args = append(args, opts.Trace)
		n++
	}
	if len(opts.Traces) > 0 {
		where = append(where, fmt.Sprintf("trace_id = ANY($%d)", n))
		args = append(args, opts.Traces)
		n++
	}
	if len(opts.SessionIDs) > 0 {
		where = append(where, fmt.Sprintf("session_id = ANY($%d)", n))
		args = append(args, opts.SessionIDs)
		n++
	}
	if opts.ErrorOnly {
		where = append(where, "error IS NOT NULL AND error <> ''")
	}
	if opts.ParentRun != "" {
		where = append(where, fmt.Sprintf("parent_run_id=$%d", n))
		args = append(args, opts.ParentRun)
		n++
	}
	if opts.RunType != "" {
		where = append(where, fmt.Sprintf("run_type=$%d", n))
		args = append(args, opts.RunType)
		n++
	}
	if opts.Name != "" {
		where = append(where, fmt.Sprintf("name=$%d", n))
		args = append(args, opts.Name)
		n++
	}
	if len(opts.IDs) > 0 {
		where = append(where, fmt.Sprintf("id = ANY($%d)", n))
		args = append(args, opts.IDs)
		n++
	}
	if opts.IsRoot != nil {
		if *opts.IsRoot {
			where = append(where, "parent_run_id IS NULL")
		} else {
			where = append(where, "parent_run_id IS NOT NULL")
		}
	}
	if opts.StartAfter != nil {
		where = append(where, fmt.Sprintf("start_time >= $%d", n))
		args = append(args, *opts.StartAfter)
		n++
	}
	limit := opts.Limit
	if limit == 0 {
		limit = 100
	}
	if opts.Unlimited {
		limit = 100000
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	q := fmt.Sprintf(`SELECT id, name, run_type, session_id, parent_run_id, dotted_order, trace_id,
		start_time, end_time, status, error, inputs, outputs, extra, events, tags,
		serialized, inputs_preview, outputs_preview, reference_example_id,
		input_tokens, output_tokens, total_tokens, model,
		langgraph_node, langgraph_step
		FROM runs WHERE %s ORDER BY start_time DESC NULLS LAST LIMIT %d OFFSET %d`,
		strings.Join(where, " AND "), limit, offset)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		m, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, rows.Err()
}

type QueryOpts struct {
	Trace      string
	Traces     []string
	SessionIDs []string
	ParentRun  string
	RunType    string
	Name       string
	IDs        []string
	IsRoot     *bool
	ErrorOnly  bool
	StartAfter *time.Time
	Limit      int
	Offset     int
	Unlimited  bool
}

func (s *Store) ListTraceIDs(ctx context.Context, sessionIDs []string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `
		SELECT trace_id FROM (
			SELECT DISTINCT ON (trace_id) trace_id, start_time
			FROM runs
			WHERE trace_id IS NOT NULL`
	args := []any{}
	if len(sessionIDs) > 0 {
		q += ` AND session_id = ANY($1)`
		args = append(args, sessionIDs)
	}
	q += `
			ORDER BY trace_id, start_time DESC NULLS LAST
		) t ORDER BY start_time DESC NULLS LAST LIMIT ` + fmt.Sprint(limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id.String())
	}
	return out, rows.Err()
}

func (s *Store) CreateFeedback(ctx context.Context, payload map[string]any) error {
	_, err := s.CreateFeedbackFull(ctx, payload)
	return err
}

func (s *Store) SaveAttachment(ctx context.Context, a ingest.Attachment) error {
	runID, err := uuid.Parse(a.RunID)
	if err != nil {
		return err
	}
	ct := a.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	key := fmt.Sprintf("attachments/%s/%s", a.RunID, a.Name)
	if s.blob != nil {
		if err := s.blob.Put(ctx, key, ct, a.Data); err != nil {
			return err
		}
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO attachments (run_id, name, content_type, size_bytes, storage_key)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (run_id, name) DO UPDATE SET
			content_type=EXCLUDED.content_type,
			size_bytes=EXCLUDED.size_bytes,
			storage_key=EXCLUDED.storage_key`,
		runID, a.Name, ct, len(a.Data), key)
	return err
}

func (s *Store) ListAttachments(ctx context.Context, runID string) ([]map[string]any, error) {
	uid, err := uuid.Parse(runID)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT name, content_type, size_bytes, storage_key FROM attachments WHERE run_id=$1`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var name, ct, key string
		var size int64
		if err := rows.Scan(&name, &ct, &size, &key); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"name": name, "content_type": ct, "size": size, "storage_key": key,
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, rows.Err()
}

func (s *Store) Stats(ctx context.Context) map[string]any {
	var runs, sessions, feedback, attachments int64
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM runs`).Scan(&runs)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&sessions)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM feedback`).Scan(&feedback)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM attachments`).Scan(&attachments)
	return map[string]any{
		"runs": runs, "sessions": sessions, "feedback": feedback, "attachments": attachments,
	}
}

func (s *Store) PersistBatch(ctx context.Context, batch *ingest.Batch) map[string]any {
	written := 0
	var errors []string
	for runID, payload := range batch.Creates {
		p := payload
		if _, ok := p["id"]; !ok {
			p["id"] = runID
		}
		if err := s.UpsertRun(ctx, p, "post"); err != nil {
			errors = append(errors, fmt.Sprintf("post %s: %v", runID, err))
		} else {
			written++
		}
	}
	for runID, payload := range batch.Updates {
		p := payload
		if _, ok := p["id"]; !ok {
			p["id"] = runID
		}
		if err := s.UpsertRun(ctx, p, "patch"); err != nil {
			errors = append(errors, fmt.Sprintf("patch %s: %v", runID, err))
		} else {
			written++
		}
	}
	for _, fb := range batch.Feedback {
		if err := s.CreateFeedback(ctx, fb); err != nil {
			errors = append(errors, fmt.Sprintf("feedback: %v", err))
		}
	}
	for _, a := range batch.Attachments {
		if err := s.SaveAttachment(ctx, a); err != nil {
			errors = append(errors, fmt.Sprintf("attachment %s: %v", a.Name, err))
		}
	}
	if errors == nil {
		errors = []string{}
	}
	return map[string]any{"written": written, "errors": errors}
}
