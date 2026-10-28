// Package textsafe provides UTF-8-safe truncation and NUL stripping for
// values that land in Postgres text/jsonb columns or JSON API responses.
package textsafe

import (
	"strings"
	"unicode/utf8"
)

// TruncateUTF8 returns at most n bytes of b without splitting a UTF-8 rune.
// Unlike trimming until Valid (which can empty the whole buffer when invalid
// bytes appear early), this backs up to a RuneStart at the cut, then replaces
// any remaining invalid sequences with U+FFFD via strings.ToValidUTF8.
func TruncateUTF8(b []byte, n int) []byte {
	if n <= 0 {
		return []byte{}
	}
	if len(b) <= n {
		out := make([]byte, len(b))
		copy(out, b)
		return []byte(strings.ToValidUTF8(string(out), "\uFFFD"))
	}
	cut := b[:n]
	for len(cut) > 0 && !utf8.RuneStart(cut[len(cut)-1]) {
		cut = cut[:len(cut)-1]
	}
	s := strings.ToValidUTF8(string(cut), "\uFFFD")
	// ToValidUTF8 may grow slightly (FFFD); keep a hard cap at n when possible.
	for len(s) > n {
		s = s[:len(s)-1]
		for len(s) > 0 && !utf8.RuneStart(s[len(s)-1]) {
			s = s[:len(s)-1]
		}
		s = strings.ToValidUTF8(s, "")
	}
	return []byte(s)
}

// TruncateUTF8String is TruncateUTF8 for strings.
func TruncateUTF8String(s string, n int) string {
	return string(TruncateUTF8([]byte(s), n))
}

// StripNUL removes raw NUL bytes from s (Postgres text/jsonb reject \u0000).
func StripNUL(s string) string {
	if !strings.ContainsRune(s, 0) {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "")
}

// Sanitize walks maps/slices/strings and strips NUL from every string.
// Non-container values are returned unchanged. Keys with NUL are rewritten.
func Sanitize(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return StripNUL(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[StripNUL(k)] = Sanitize(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = Sanitize(vv)
		}
		return out
	default:
		return v
	}
}
