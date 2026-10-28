package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (s *Store) UpdateSession(ctx context.Context, id string, payload map[string]any) (map[string]any, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	cur, err := s.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	name := asString(cur["name"])
	if v, ok := payload["name"]; ok && v != nil {
		name = asString(v)
	}
	desc := asString(cur["description"])
	if v, ok := payload["description"]; ok && v != nil {
		desc = asString(v)
	}
	end := parseTime(cur["end_time"])
	if v, ok := payload["end_time"]; ok {
		end = parseTime(v)
	}
	extra := cur["extra"]
	if v, ok := payload["extra"]; ok && v != nil {
		extra = v
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE sessions SET name=$1, description=$2, end_time=$3, extra=$4 WHERE id=$5`,
		name, desc, end, toJSON(extra), uid)
	if err != nil {
		return nil, err
	}
	return s.GetSession(ctx, id)
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM sessions WHERE id=$1`, uid)
	return err
}

func (s *Store) DeleteRun(ctx context.Context, id string) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, uid)
	return err
}

func (s *Store) CreateFeedbackFull(ctx context.Context, payload map[string]any) (map[string]any, error) {
	id := asUUID(payload["id"])
	if id == nil {
		u := uuid.New()
		id = &u
	}
	runID := asUUID(payload["run_id"])
	traceID := asUUID(payload["trace_id"])
	var score *float64
	switch v := payload["score"].(type) {
	case float64:
		score = &v
	case json.Number:
		f, _ := v.Float64()
		score = &f
	case int:
		f := float64(v)
		score = &f
	}
	now := time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO feedback (id, run_id, trace_id, key, score, value, comment, correction, feedback_source, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET
			score=EXCLUDED.score, value=EXCLUDED.value, comment=EXCLUDED.comment,
			correction=EXCLUDED.correction, feedback_source=EXCLUDED.feedback_source`,
		*id, runID, traceID, asString(payload["key"]), score,
		toJSON(payload["value"]), asString(payload["comment"]),
		toJSON(payload["correction"]), toJSON(payload["feedback_source"]), now)
	if err != nil {
		return nil, err
	}
	created := now.Format(time.RFC3339Nano)
	out := map[string]any{
		"id": id.String(), "key": asString(payload["key"]),
		"score": score, "value": payload["value"], "comment": asString(payload["comment"]),
		"correction": payload["correction"], "feedback_source": payload["feedback_source"],
		"created_at": created, "modified_at": created,
	}
	if runID != nil {
		out["run_id"] = runID.String()
	}
	if traceID != nil {
		out["trace_id"] = traceID.String()
	}
	return out, nil
}

func (s *Store) ListFeedback(ctx context.Context, runID, key string) ([]map[string]any, error) {
	where := []string{"1=1"}
	args := []any{}
	n := 1
	if runID != "" {
		where = append(where, fmt.Sprintf("run_id=$%d", n))
		args = append(args, runID)
		n++
	}
	if key != "" {
		where = append(where, fmt.Sprintf("key=$%d", n))
		args = append(args, key)
		n++
	}
	q := fmt.Sprintf(`SELECT id, run_id, trace_id, key, score, value, comment, correction, feedback_source, created_at
		FROM feedback WHERE %s ORDER BY created_at DESC LIMIT 500`, joinAnd(where))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id uuid.UUID
		var rid, tid *uuid.UUID
		var k, comment string
		var score *float64
		var value, corr, src []byte
		var created time.Time
		if err := rows.Scan(&id, &rid, &tid, &k, &score, &value, &comment, &corr, &src, &created); err != nil {
			return nil, err
		}
		m := map[string]any{
			"id": id.String(), "key": k, "comment": comment,
			"score": score, "created_at": created.UTC().Format(time.RFC3339Nano),
			"modified_at": created.UTC().Format(time.RFC3339Nano),
		}
		if rid != nil {
			m["run_id"] = rid.String()
		}
		if tid != nil {
			m["trace_id"] = tid.String()
		}
		setJSON(m, "value", value)
		setJSON(m, "correction", corr)
		setJSON(m, "feedback_source", src)
		out = append(out, m)
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, rows.Err()
}

func joinAnd(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " AND "
		}
		out += p
	}
	return out
}

func (s *Store) GetAttachment(ctx context.Context, runID, name string) ([]byte, string, error) {
	uid, err := uuid.Parse(runID)
	if err != nil {
		return nil, "", err
	}
	var ct, key string
	err = s.pool.QueryRow(ctx, `
		SELECT content_type, storage_key FROM attachments WHERE run_id=$1 AND name=$2`, uid, name).
		Scan(&ct, &key)
	if err != nil {
		return nil, "", err
	}
	if s.blob == nil {
		return nil, ct, fmt.Errorf("no blob store")
	}
	data, _, err := s.blob.Get(ctx, key)
	return data, ct, err
}
