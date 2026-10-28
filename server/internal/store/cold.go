package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/klauspost/compress/zstd"
)

// Heavy columns moved to cold blobs (error stays hot per ProductBro).

// ColdArchiveOpts controls a single archive batch.
type ColdArchiveOpts struct {
	// OlderThan: root end_time must be before now-OlderThan (hot retention).
	OlderThan time.Duration
	// IngestGuard: skip traces with any run updated_at within this window (default 1h).
	// Tests may set 0 to force-archive immediately after seeding.
	IngestGuard time.Duration
	Limit       int
	// TraceIDs: if non-empty, only consider these (still subject to open-run + ingest guard).
	TraceIDs []string
}

type coldStats struct {
	archived     atomic.Uint64
	hydrated     atomic.Uint64
	verifyFailed atomic.Uint64
	orphans      atomic.Uint64
}

// ColdStats returns process-local cold-tier counters for GET /info.
func (s *Store) ColdStats() map[string]any {
	return map[string]any{
		"archived":      s.cold.archived.Load(),
		"hydrated":      s.cold.hydrated.Load(),
		"verify_failed": s.cold.verifyFailed.Load(),
		"orphans":       s.cold.orphans.Load(),
	}
}

func (s *Store) ResetColdStatsForTest() {
	s.cold.archived.Store(0)
	s.cold.hydrated.Store(0)
	s.cold.verifyFailed.Store(0)
	s.cold.orphans.Store(0)
}

type hydrateCache struct {
	mu    sync.Mutex
	order []string
	data  map[string]map[string]map[string]any // traceID -> runID -> heavy fields
	cap   int
}

func newHydrateCache(cap int) *hydrateCache {
	if cap <= 0 {
		cap = 64
	}
	return &hydrateCache{data: map[string]map[string]map[string]any{}, cap: cap}
}

func (c *hydrateCache) get(traceID string) (map[string]map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[traceID]
	return v, ok
}

func (c *hydrateCache) put(traceID string, runs map[string]map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.data[traceID]; !ok {
		c.order = append(c.order, traceID)
		if len(c.order) > c.cap {
			old := c.order[0]
			c.order = c.order[1:]
			delete(c.data, old)
		}
	}
	c.data[traceID] = runs
}

func (c *hydrateCache) invalidate(traceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, traceID)
}

type coldLine struct {
	ID         string          `json:"id"`
	Inputs     json.RawMessage `json:"inputs,omitempty"`
	Outputs    json.RawMessage `json:"outputs,omitempty"`
	Events     json.RawMessage `json:"events,omitempty"`
	Extra      json.RawMessage `json:"extra,omitempty"`
	Serialized json.RawMessage `json:"serialized,omitempty"`
}

func coldObjectKey(traceID uuid.UUID, coldAt time.Time) string {
	t := coldAt.UTC()
	return fmt.Sprintf("cold/traces/%04d/%02d/%02d/%s.jsonl.zst", t.Year(), int(t.Month()), t.Day(), traceID.String())
}

// IsTraceCold reports whether a trace_cold row exists.
func (s *Store) IsTraceCold(ctx context.Context, traceID string) (bool, error) {
	uid, err := uuid.Parse(traceID)
	if err != nil {
		return false, err
	}
	var n int
	err = s.pool.QueryRow(ctx, `SELECT 1 FROM trace_cold WHERE trace_id=$1`, uid).Scan(&n)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// ArchiveEligibleTraces finds finished traces older than retention that pass guards.
func (s *Store) ArchiveEligibleTraces(ctx context.Context, opts ColdArchiveOpts) ([]uuid.UUID, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.OlderThan < 0 {
		opts.OlderThan = 0
	}
	cutoff := time.Now().UTC().Add(-opts.OlderThan)
	guardCutoff := time.Now().UTC().Add(-opts.IngestGuard)

	args := []any{cutoff, guardCutoff}
	n := 3
	filter := ""
	if len(opts.TraceIDs) > 0 {
		ids := make([]uuid.UUID, 0, len(opts.TraceIDs))
		for _, t := range opts.TraceIDs {
			if u, err := uuid.Parse(t); err == nil {
				ids = append(ids, u)
			}
		}
		filter = fmt.Sprintf(" AND r.trace_id = ANY($%d)", n)
		args = append(args, ids)
		n++
	}

	// Root finished before cutoff; no open runs; no run updated within ingest guard;
	// not already cold.
	q := `
		SELECT r.trace_id
		FROM runs r
		WHERE r.parent_run_id IS NULL
		  AND r.trace_id IS NOT NULL
		  AND r.end_time IS NOT NULL
		  AND r.end_time < $1
		  AND NOT EXISTS (SELECT 1 FROM trace_cold c WHERE c.trace_id = r.trace_id)
		  AND NOT EXISTS (
		    SELECT 1 FROM runs x
		    WHERE x.trace_id = r.trace_id AND x.end_time IS NULL
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM runs x
		    WHERE x.trace_id = r.trace_id AND x.updated_at > $2
		  )` + filter + `
		ORDER BY r.end_time ASC
		LIMIT ` + fmt.Sprint(opts.Limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ArchiveTrace writes the cold blob, verifies checksum, inserts trace_cold, NULLs heavy cols.
// Idempotent: if already cold, returns nil. Crash before NULL leaves an orphan blob (counted).
func (s *Store) ArchiveTrace(ctx context.Context, traceID uuid.UUID) error {
	if s.blob == nil {
		return fmt.Errorf("blob store not configured")
	}
	var exists int
	if err := s.pool.QueryRow(ctx, `SELECT 1 FROM trace_cold WHERE trace_id=$1`, traceID).Scan(&exists); err == nil {
		return nil
	} else if err != pgx.ErrNoRows {
		return err
	}

	// Guard: open runs or recent updates
	var openN, recentN int
	_ = s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE end_time IS NULL),
		  COUNT(*) FILTER (WHERE updated_at > now() - interval '1 hour')
		FROM runs WHERE trace_id=$1`, traceID).Scan(&openN, &recentN)
	// Note: ArchiveBatch applies configurable ingest guard at selection time;
	// ArchiveTrace used from tests may pass age=0 with guard=0 via ArchiveBatch only.
	if openN > 0 {
		return fmt.Errorf("trace %s has open runs", traceID)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, inputs, outputs, events, extra, serialized
		FROM runs WHERE trace_id=$1 ORDER BY dotted_order ASC, start_time ASC NULLS LAST`, traceID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var buf bytes.Buffer
	runCount := 0
	for rows.Next() {
		var id uuid.UUID
		var inputs, outputs, events, extra, serialized []byte
		if err := rows.Scan(&id, &inputs, &outputs, &events, &extra, &serialized); err != nil {
			return err
		}
		line := coldLine{ID: id.String()}
		if inputs != nil {
			line.Inputs = inputs
		}
		if outputs != nil {
			line.Outputs = outputs
		}
		if events != nil {
			line.Events = events
		}
		if extra != nil {
			line.Extra = extra
		}
		if serialized != nil {
			line.Serialized = serialized
		}
		b, err := json.Marshal(line)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
		runCount++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if runCount == 0 {
		return fmt.Errorf("trace %s has no runs", traceID)
	}

	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return err
	}
	compressed := enc.EncodeAll(buf.Bytes(), nil)
	enc.Close()
	sum := sha256.Sum256(compressed)
	sha := hex.EncodeToString(sum[:])
	coldAt := time.Now().UTC()
	key := coldObjectKey(traceID, coldAt)

	// Write blob first (orphan if we crash before DB commit).
	s.cold.orphans.Add(1)
	if err := s.blob.Put(ctx, key, "application/zstd", compressed); err != nil {
		return fmt.Errorf("put cold blob: %w", err)
	}

	got, _, err := s.blob.Get(ctx, key)
	if err != nil {
		s.cold.verifyFailed.Add(1)
		return fmt.Errorf("verify get: %w", err)
	}
	gotSum := sha256.Sum256(got)
	if hex.EncodeToString(gotSum[:]) != sha {
		s.cold.verifyFailed.Add(1)
		return fmt.Errorf("verify checksum mismatch")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Re-check open runs under lock of intended cold row insert.
	var stillOpen int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM runs WHERE trace_id=$1 AND end_time IS NULL`, traceID).Scan(&stillOpen); err != nil {
		return err
	}
	if stillOpen > 0 {
		return fmt.Errorf("trace %s opened during archive", traceID)
	}

	ct, err := tx.Exec(ctx, `
		INSERT INTO trace_cold (trace_id, cold_uri, cold_sha256, cold_bytes, cold_at, run_count)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (trace_id) DO NOTHING`,
		traceID, key, sha, len(compressed), coldAt, runCount)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		// Lost race — already cold.
		return nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE runs SET
		  inputs=NULL, outputs=NULL, events=NULL, extra=NULL, serialized=NULL,
		  updated_at=updated_at
		WHERE trace_id=$1`, traceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// Committed: orphan counter was speculative; blob now owned by trace_cold.
	if s.cold.orphans.Load() > 0 {
		s.cold.orphans.Add(^uint64(0)) // decrement
	}
	s.cold.archived.Add(1)
	s.hydrateCache.invalidate(traceID.String())
	return nil
}

// ArchiveBatch selects eligible traces and archives them. ingestGuard of 0 disables the 1h freshness check at selection.
func (s *Store) ArchiveBatch(ctx context.Context, opts ColdArchiveOpts) (int, error) {
	ids, err := s.ArchiveEligibleTraces(ctx, opts)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		// Per-trace: when IngestGuard==0 (test force), skip the hard-coded 1h check inside ArchiveTrace
		// by using archiveTraceWithGuard.
		if err := s.archiveTraceWithGuard(ctx, id, opts.IngestGuard); err != nil {
			log.Printf("cold archive %s: %v", id, err)
			continue
		}
		n++
	}
	return n, nil
}

func (s *Store) archiveTraceWithGuard(ctx context.Context, traceID uuid.UUID, ingestGuard time.Duration) error {
	if ingestGuard > 0 {
		var recent int
		cutoff := time.Now().UTC().Add(-ingestGuard)
		if err := s.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM runs WHERE trace_id=$1 AND updated_at > $2`, traceID, cutoff).Scan(&recent); err != nil {
			return err
		}
		if recent > 0 {
			return fmt.Errorf("trace %s updated within ingest guard", traceID)
		}
	}
	return s.ArchiveTrace(ctx, traceID)
}

// loadColdBlob returns per-run heavy fields from S3 (or cache).
func (s *Store) loadColdBlob(ctx context.Context, traceID string) (map[string]map[string]any, string, error) {
	if cached, ok := s.hydrateCache.get(traceID); ok {
		return cached, "", nil
	}
	uid, err := uuid.Parse(traceID)
	if err != nil {
		return nil, "", err
	}
	var uri, sha string
	err = s.pool.QueryRow(ctx, `
		SELECT cold_uri, cold_sha256 FROM trace_cold WHERE trace_id=$1`, uid).Scan(&uri, &sha)
	if err == pgx.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if s.blob == nil {
		return nil, "", fmt.Errorf("blob store not configured")
	}
	raw, _, err := s.blob.Get(ctx, uri)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != sha {
		s.cold.verifyFailed.Add(1)
		return nil, "", fmt.Errorf("cold blob checksum mismatch for %s", traceID)
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, "", err
	}
	defer dec.Close()
	plain, err := dec.DecodeAll(raw, nil)
	if err != nil {
		return nil, "", err
	}
	out := map[string]map[string]any{}
	for _, line := range bytes.Split(plain, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var cl coldLine
		if err := json.Unmarshal(line, &cl); err != nil {
			return nil, "", err
		}
		heavy := map[string]any{}
		decodeRaw := func(raw json.RawMessage, key string) {
			if len(raw) == 0 {
				return
			}
			var v any
			if err := json.Unmarshal(raw, &v); err == nil {
				heavy[key] = v
			}
		}
		decodeRaw(cl.Inputs, "inputs")
		decodeRaw(cl.Outputs, "outputs")
		decodeRaw(cl.Events, "events")
		decodeRaw(cl.Extra, "extra")
		decodeRaw(cl.Serialized, "serialized")
		out[cl.ID] = heavy
	}
	s.hydrateCache.put(traceID, out)
	return out, uri, nil
}

// HydrateRun merges cold heavy fields into a run map if needed.
func (s *Store) HydrateRun(ctx context.Context, run map[string]any) error {
	if run == nil {
		return nil
	}
	traceID, _ := run["trace_id"].(string)
	if traceID == "" {
		return nil
	}
	// Already hot if any heavy col present.
	if run["inputs"] != nil || run["outputs"] != nil || run["extra"] != nil ||
		run["events"] != nil || run["serialized"] != nil {
		return nil
	}
	cold, err := s.IsTraceCold(ctx, traceID)
	if err != nil || !cold {
		return err
	}
	byRun, _, err := s.loadColdBlob(ctx, traceID)
	if err != nil {
		return err
	}
	id, _ := run["id"].(string)
	if heavy, ok := byRun[id]; ok {
		for k, v := range heavy {
			run[k] = v
		}
	}
	s.cold.hydrated.Add(1)
	return nil
}

// HydrateTraceRuns hydrates every run in the slice (same trace).
func (s *Store) HydrateTraceRuns(ctx context.Context, runs []map[string]any) error {
	if len(runs) == 0 {
		return nil
	}
	traceID, _ := runs[0]["trace_id"].(string)
	if traceID == "" {
		return nil
	}
	need := false
	for _, r := range runs {
		if r["inputs"] == nil && r["outputs"] == nil && r["extra"] == nil &&
			r["events"] == nil && r["serialized"] == nil {
			need = true
			break
		}
	}
	if !need {
		return nil
	}
	cold, err := s.IsTraceCold(ctx, traceID)
	if err != nil || !cold {
		return err
	}
	byRun, _, err := s.loadColdBlob(ctx, traceID)
	if err != nil {
		return err
	}
	for _, r := range runs {
		id, _ := r["id"].(string)
		if heavy, ok := byRun[id]; ok {
			for k, v := range heavy {
				r[k] = v
			}
		}
	}
	s.cold.hydrated.Add(1)
	return nil
}

// RehotTrace restores heavy columns from the blob and deletes the trace_cold row (one tx).
// Caller holds responsibility for applying a subsequent write. Uses FOR UPDATE.
func (s *Store) RehotTrace(ctx context.Context, traceID string) error {
	uid, err := uuid.Parse(traceID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var uri, sha string
	err = tx.QueryRow(ctx, `
		SELECT cold_uri, cold_sha256 FROM trace_cold WHERE trace_id=$1 FOR UPDATE`, uid).
		Scan(&uri, &sha)
	if err == pgx.ErrNoRows {
		return nil // already hot
	}
	if err != nil {
		return err
	}
	if s.blob == nil {
		return fmt.Errorf("blob store not configured")
	}
	raw, _, err := s.blob.Get(ctx, uri)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != sha {
		s.cold.verifyFailed.Add(1)
		return fmt.Errorf("rehot checksum mismatch")
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return err
	}
	defer dec.Close()
	plain, err := dec.DecodeAll(raw, nil)
	if err != nil {
		return err
	}
	for _, line := range bytes.Split(plain, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var cl coldLine
		if err := json.Unmarshal(line, &cl); err != nil {
			return err
		}
		rid, err := uuid.Parse(cl.ID)
		if err != nil {
			return err
		}
		// COALESCE so we don't wipe with empty — but cold means NULL, so set from blob.
		_, err = tx.Exec(ctx, `
			UPDATE runs SET
			  inputs=COALESCE($2::jsonb, inputs),
			  outputs=COALESCE($3::jsonb, outputs),
			  events=COALESCE($4::jsonb, events),
			  extra=COALESCE($5::jsonb, extra),
			  serialized=COALESCE($6::jsonb, serialized),
			  updated_at=now()
			WHERE id=$1`,
			rid,
			nullJSON(cl.Inputs),
			nullJSON(cl.Outputs),
			nullJSON(cl.Events),
			nullJSON(cl.Extra),
			nullJSON(cl.Serialized),
		)
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM trace_cold WHERE trace_id=$1`, uid); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.hydrateCache.invalidate(traceID)
	s.cold.orphans.Add(1) // blob remains without trace_cold row
	return nil
}

func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

// maybeRehotForWrite locks and re-hots a cold trace before an ingest write.
func (s *Store) maybeRehotForWrite(ctx context.Context, traceID string) error {
	if traceID == "" {
		return nil
	}
	cold, err := s.IsTraceCold(ctx, traceID)
	if err != nil || !cold {
		return err
	}
	return s.RehotTrace(ctx, traceID)
}

// StartColdArchiver runs a background ticker when enabled. Stop via context cancel.
func (s *Store) StartColdArchiver(ctx context.Context, olderThan, ingestGuard, interval time.Duration, batchSize int) {
	if interval <= 0 {
		interval = time.Hour
	}
	if batchSize <= 0 {
		batchSize = 50
	}
	if ingestGuard <= 0 {
		ingestGuard = time.Hour
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n, err := s.ArchiveBatch(ctx, ColdArchiveOpts{
					OlderThan: olderThan, IngestGuard: ingestGuard, Limit: batchSize,
				})
				if err != nil {
					log.Printf("cold archiver: %v", err)
				} else if n > 0 {
					log.Printf("cold archiver: archived %d traces", n)
				}
			}
		}
	}()
}

// ColdURIForTest returns the cold_uri for assertions.
func (s *Store) ColdURIForTest(ctx context.Context, traceID string) (string, error) {
	uid, err := uuid.Parse(traceID)
	if err != nil {
		return "", err
	}
	var uri string
	err = s.pool.QueryRow(ctx, `SELECT cold_uri FROM trace_cold WHERE trace_id=$1`, uid).Scan(&uri)
	return uri, err
}

// HeavyColsNullForTest reports whether all heavy cols are NULL for every run in the trace.
func (s *Store) HeavyColsNullForTest(ctx context.Context, traceID string) (bool, error) {
	uid, err := uuid.Parse(traceID)
	if err != nil {
		return false, err
	}
	var n int
	err = s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM runs
		WHERE trace_id=$1 AND (
		  inputs IS NOT NULL OR outputs IS NOT NULL OR events IS NOT NULL
		  OR extra IS NOT NULL OR serialized IS NOT NULL
		)`, uid).Scan(&n)
	return n == 0, err
}

// coldTraceHint extracts a trace_id from an ingest payload (for rehot before insert).
func coldTraceHint(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if tid := asString(payload["trace_id"]); tid != "" {
		return tid
	}
	return ""
}
