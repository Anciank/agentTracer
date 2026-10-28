package migrations

import (
	"fmt"
	"regexp"
	"strings"
)

// ExpandContractMarker must appear in a migration that intentionally drops/rewrites columns.
const ExpandContractMarker = "-- agenttracer:expand-contract"

var destructivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bDROP\s+COLUMN\b`),
	regexp.MustCompile(`(?i)\bALTER\s+COLUMN\b.+\bTYPE\b`),
	regexp.MustCompile(`(?i)\bALTER\s+COLUMN\b.+\bSET\s+DATA\s+TYPE\b`),
	regexp.MustCompile(`(?i)\bRENAME\s+COLUMN\b`),
	regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`),
	regexp.MustCompile(`(?i)\bTRUNCATE\s+TABLE\b`),
	regexp.MustCompile(`(?i)\bTRUNCATE\s+[a-zA-Z_][a-zA-Z0-9_]*`),
}

// CheckDestructive rejects migrations that drop/rewrite columns/tables unless they
// explicitly opt in with ExpandContractMarker (two-step expand/contract).
func CheckDestructive(filename, sqlText string) error {
	if strings.Contains(sqlText, ExpandContractMarker) {
		return nil
	}
	// Strip line comments so markers in comments still count, but patterns in comments don't false-positive hard.
	stripped := stripSQLLineComments(sqlText)
	for _, re := range destructivePatterns {
		if re.MatchString(stripped) {
			return fmt.Errorf(
				"migration %s: destructive change matched %q without %q (use expand/contract two-step)",
				filename, re.String(), ExpandContractMarker,
			)
		}
	}
	return nil
}

func stripSQLLineComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
