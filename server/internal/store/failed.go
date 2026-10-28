package store

import "fmt"

// RunHasErrorExpr is the shared SQL predicate for a single runs-row alias:
// a run "errored" when it has a non-empty error string or status = 'error'.
// Use with fmt.Sprintf(RunHasErrorExpr, alias, alias, alias).
const RunHasErrorExpr = `((%s.error IS NOT NULL AND %s.error <> '') OR %s.status = 'error')`

// TraceFailedExistsSQL returns an EXISTS subquery: the trace is failed if ANY
// run in it matches RunHasErrorExpr. traceIDExpr is a SQL expression naming
// the trace_id to check (e.g. "r.trace_id").
func TraceFailedExistsSQL(traceIDExpr string) string {
	return fmt.Sprintf(
		`EXISTS (SELECT 1 FROM runs __fe WHERE __fe.trace_id = %s AND %s)`,
		traceIDExpr,
		fmt.Sprintf(RunHasErrorExpr, "__fe", "__fe", "__fe"),
	)
}

// RunHasError is the Go counterpart of RunHasErrorExpr.
func RunHasError(r map[string]any) bool {
	if asString(r["error"]) != "" {
		return true
	}
	return asString(r["status"]) == "error"
}

// TraceHasFailedRun reports whether any run in the slice counts as errored.
func TraceHasFailedRun(runs []map[string]any) bool {
	for _, r := range runs {
		if RunHasError(r) {
			return true
		}
	}
	return false
}
