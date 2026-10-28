package store

import (
	"context"
	"encoding/json"
	"log"

	"github.com/google/uuid"
)

// chatPreviewBackfillBatch is the number of root traces processed per SELECT.
// Keeps peak memory low on a ~1.9 GiB box (~2k runs total).
const chatPreviewBackfillBatch = 50

// BackfillChatPreviews fills empty first_user_preview / last_ai_preview on root
// runs by scanning each trace's inputs/outputs (resolving {"_s3": key} cold
// pointers via the blob store when present).
//
// Idempotent: only writes when a column is still empty; safe to run twice.
// limit<=0 means no overall cap. Per-trace failures are logged and skipped.
// Returns (rootsUpdated, failed, scanned).
func (s *Store) BackfillChatPreviews(ctx context.Context, limit int) (written, failed, scanned int, err error) {
	remaining := limit
	for {
		batch := chatPreviewBackfillBatch
		if remaining > 0 && remaining < batch {
			batch = remaining
		}
		ids, err := s.listRootsNeedingChatPreviews(ctx, batch)
		if err != nil {
			return written, failed, scanned, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			scanned++
			ok, ferr := s.backfillOneTraceChatPreviews(ctx, id)
			if ferr != nil {
				log.Printf("chat preview backfill: trace %s: %v", id, ferr)
				failed++
				continue
			}
			if ok {
				written++
			}
			if remaining > 0 {
				remaining--
				if remaining == 0 {
					log.Printf("chat preview backfill summary: wrote %d, failed %d, scanned %d (hit limit)", written, failed, scanned)
					return written, failed, scanned, nil
				}
			}
		}
		if len(ids) < batch {
			break
		}
	}
	log.Printf("chat preview backfill summary: wrote %d, failed %d, scanned %d", written, failed, scanned)
	return written, failed, scanned, nil
}

func (s *Store) listRootsNeedingChatPreviews(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = chatPreviewBackfillBatch
	}
	rows, err := s.pool.Query(ctx, `
		SELECT trace_id FROM runs
		WHERE parent_run_id IS NULL
			AND trace_id IS NOT NULL
			AND (first_user_preview = '' OR last_ai_preview = '')
		ORDER BY start_time DESC NULLS LAST
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) backfillOneTraceChatPreviews(ctx context.Context, traceID uuid.UUID) (wrote bool, err error) {
	runs, err := s.QueryRuns(ctx, QueryOpts{Trace: traceID.String(), Unlimited: true})
	if err != nil {
		return false, err
	}
	if len(runs) == 0 {
		return false, nil
	}
	// Resolve cold pointers in place for preview extraction only.
	for _, r := range runs {
		r["inputs"] = s.resolveMaybeCold(ctx, r["inputs"])
		r["outputs"] = s.resolveMaybeCold(ctx, r["outputs"])
	}
	fu, la := pickTraceChatPreviews(runs)
	if fu == "" && la == "" {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE runs SET
			first_user_preview = CASE
				WHEN first_user_preview = '' AND $2 <> '' THEN $2
				ELSE first_user_preview
			END,
			last_ai_preview = CASE
				WHEN last_ai_preview = '' AND $3 <> '' THEN $3
				ELSE last_ai_preview
			END,
			updated_at = now()
		WHERE trace_id = $1 AND parent_run_id IS NULL
			AND (first_user_preview = '' OR last_ai_preview = '')`,
		traceID, fu, la)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// resolveMaybeCold returns the JSON payload, fetching from blob when v is {"_s3": key}.
// On fetch failure or missing blob store, returns v unchanged (preview may stay empty).
func (s *Store) resolveMaybeCold(ctx context.Context, v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	key, ok := m["_s3"].(string)
	if !ok || key == "" {
		return v
	}
	if s.blob == nil {
		return v
	}
	data, _, err := s.blob.Get(ctx, key)
	if err != nil || len(data) == 0 {
		return v
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return v
	}
	return decoded
}
