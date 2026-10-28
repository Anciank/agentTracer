package store

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// LangSmith dotted_order segment: YYYYMMDDTHHMMSSffffffZ + run UUID.
// Example: 20260101T000000000000Zaaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee
//
// Some clients (and older fixtures) append only the undashed first 8 hex of
// the UUID: 20260101T000000000000Zd23b9ff8. Those are not parseable as a
// UUID on their own; resolveTraceID matches them against parent_run_id.
const dottedOrderTimestampLen = len("20060102T150405000000Z") // 22

// dottedOrderFirstSuffix returns the raw id suffix after the compact
// timestamp in the first dotted_order segment (may be a full UUID, undashed
// 32-hex, or a short hex prefix).
func dottedOrderFirstSuffix(dotted string) string {
	dotted = strings.TrimSpace(dotted)
	if dotted == "" {
		return ""
	}
	first, _, _ := strings.Cut(dotted, ".")
	if len(first) <= dottedOrderTimestampLen {
		return ""
	}
	return first[dottedOrderTimestampLen:]
}

// rootTraceIDFromDottedOrder returns the UUID trailing the first dotted_order
// segment (the root run id, which is the trace id) when it parses as a full
// UUID (dashed or 32-hex undashed). Short prefixes return nil.
func rootTraceIDFromDottedOrder(dotted string) *uuid.UUID {
	suf := dottedOrderFirstSuffix(dotted)
	if suf == "" {
		return nil
	}
	return asUUID(suf)
}

// uuidUndashed returns the lowercase hex form of id with dashes stripped.
func uuidUndashed(id uuid.UUID) string {
	return strings.ReplaceAll(id.String(), "-", "")
}

// suffixPrefixesUUID reports whether raw (possibly dashed, any case) is a
// non-empty prefix of id's undashed hex, of at least 8 hex chars.
func suffixPrefixesUUID(raw string, id uuid.UUID) bool {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), "-", ""))
	if len(norm) < 8 {
		return false
	}
	for _, r := range norm {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return strings.HasPrefix(uuidUndashed(id), norm)
}

// resolveTraceID picks a deterministic trace id for ingest.
//
// If trace_id is missing, OR it equals the run's own id while parent_run_id is
// set (SDK often stamps child.trace_id = child.id), derive it from
// dotted_order's first-segment UUID. When that suffix is only a short hex
// prefix (UIBro / compact fixtures), match it against parent_run_id — for the
// common child-before-parent case the parent is the root. Fall back to the
// parent's stored trace_id only when dotted_order yields nothing. Otherwise
// keep the provided value, defaulting roots to their own id.
func (s *Store) resolveTraceID(ctx context.Context, runID uuid.UUID, merged map[string]any) *uuid.UUID {
	traceID := asUUID(merged["trace_id"])
	parentID := asUUID(merged["parent_run_id"])

	needDerive := traceID == nil || (parentID != nil && *traceID == runID)
	if !needDerive {
		return traceID
	}

	dotted := asString(merged["dotted_order"])
	if derived := rootTraceIDFromDottedOrder(dotted); derived != nil {
		return derived
	}

	// Short / non-UUID suffix: if it prefixes parent_run_id, the parent is the
	// root (UIBro early_child_llm before oo_parent_chain).
	if parentID != nil {
		if suf := dottedOrderFirstSuffix(dotted); suf != "" && suffixPrefixesUUID(suf, *parentID) {
			return parentID
		}
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
