package store

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// LangSmith dotted_order segment: YYYYMMDDTHHMMSSffffffZ + run UUID.
// Example: 20260101T000000000000Zaaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee
const dottedOrderTimestampLen = len("20060102T150405000000Z") // 22

// rootTraceIDFromDottedOrder returns the UUID trailing the first dotted_order
// segment (the root run id, which is the trace id).
func rootTraceIDFromDottedOrder(dotted string) *uuid.UUID {
	dotted = strings.TrimSpace(dotted)
	if dotted == "" {
		return nil
	}
	first, _, _ := strings.Cut(dotted, ".")
	if len(first) <= dottedOrderTimestampLen {
		return nil
	}
	return asUUID(first[dottedOrderTimestampLen:])
}

// resolveTraceID picks a deterministic trace id for ingest.
//
// If trace_id is missing, OR it equals the run's own id while parent_run_id is
// set (SDK often stamps child.trace_id = child.id), derive it from
// dotted_order's first-segment UUID. Fall back to the parent's stored
// trace_id only when dotted_order is absent. Otherwise keep the provided
// value, defaulting roots to their own id.
func (s *Store) resolveTraceID(ctx context.Context, runID uuid.UUID, merged map[string]any) *uuid.UUID {
	traceID := asUUID(merged["trace_id"])
	parentID := asUUID(merged["parent_run_id"])

	needDerive := traceID == nil || (parentID != nil && *traceID == runID)
	if !needDerive {
		return traceID
	}

	if derived := rootTraceIDFromDottedOrder(asString(merged["dotted_order"])); derived != nil {
		return derived
	}

	if parentID != nil && s != nil {
		parent, err := s.GetRun(ctx, parentID.String())
		if err == nil && parent != nil {
			if tid := asUUID(parent["trace_id"]); tid != nil {
				return tid
			}
			// Parent may itself be the root.
			if pid := asUUID(parent["id"]); pid != nil {
				return pid
			}
		}
	}

	if traceID != nil {
		return traceID
	}
	return &runID
}
