package store

import (
	"context"
	"log"

	"github.com/google/uuid"
)

const threadIDBackfillBatch = 50

// BackfillThreadIDs fills nullable thread_id / thread_key_source on root runs
// that still lack a key, by digging extra.metadata (same resolver as ingest).
//
// Idempotent: only updates rows WHERE thread_id IS NULL. limit<=0 means no cap.
// Returns (rootsUpdated, failed, scanned).
func (s *Store) BackfillThreadIDs(ctx context.Context, limit int) (written, failed, scanned int, err error) {
	remaining := limit
	for {
		batch := threadIDBackfillBatch
		if remaining > 0 && remaining < batch {
			batch = remaining
		}
		ids, err := s.listRootsNeedingThreadID(ctx, batch)
		if err != nil {
			return written, failed, scanned, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			scanned++
			ok, ferr := s.backfillOneRootThreadID(ctx, id)
			if ferr != nil {
				log.Printf("thread_id backfill: root %s: %v", id, ferr)
				failed++
				continue
			}
			if ok {
				written++
			}
			if remaining > 0 {
				remaining--
				if remaining == 0 {
					log.Printf("thread_id backfill summary: wrote %d, failed %d, scanned %d (hit limit)", written, failed, scanned)
					return written, failed, scanned, nil
				}
			}
		}
		if len(ids) < batch {
			break
		}
	}
	log.Printf("thread_id backfill summary: wrote %d, failed %d, scanned %d", written, failed, scanned)
	return written, failed, scanned, nil
}

func (s *Store) listRootsNeedingThreadID(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = threadIDBackfillBatch
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM runs
		WHERE parent_run_id IS NULL
			AND thread_id IS NULL
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

func (s *Store) backfillOneRootThreadID(ctx context.Context, rootID uuid.UUID) (wrote bool, err error) {
	run, err := s.GetRun(ctx, rootID.String())
	if err != nil || run == nil {
		return false, err
	}
	tid, src := resolveThreadKey(run)
	if tid == nil {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE runs SET
			thread_id = $2,
			thread_key_source = $3,
			updated_at = now()
		WHERE id = $1 AND parent_run_id IS NULL AND thread_id IS NULL`,
		rootID, *tid, *src)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
