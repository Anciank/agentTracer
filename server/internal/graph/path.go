package graph

import (
	"fmt"
	"strings"
)

// Step is one collapsed path segment.
type Step struct {
	Key    string `json:"key"`
	Repeat int    `json:"repeat"`
}

// CollapseConsecutive merges runs of the same key into Steps with Repeat>=1.
func CollapseConsecutive(keys []string) []Step {
	if len(keys) == 0 {
		return nil
	}
	out := make([]Step, 0, len(keys))
	cur := keys[0]
	n := 1
	for i := 1; i < len(keys); i++ {
		if keys[i] == cur {
			n++
			continue
		}
		out = append(out, Step{Key: cur, Repeat: n})
		cur = keys[i]
		n = 1
	}
	out = append(out, Step{Key: cur, Repeat: n})
	return out
}

// Fingerprint returns the stable collapsed path identity string.
// Example: supervisor>tools×3>respond
// Choice: we store this readable collapsed form (not a digest). It is a
// deterministic content fingerprint of the node sequence and matches UIBro's
// shapes doc; /paths and path= filters stay human-readable.
func Fingerprint(steps []Step) string {
	if len(steps) == 0 {
		return ""
	}
	parts := make([]string, 0, len(steps))
	for _, s := range steps {
		if s.Repeat <= 1 {
			parts = append(parts, s.Key)
		} else {
			parts = append(parts, fmt.Sprintf("%s×%d", s.Key, s.Repeat))
		}
	}
	return strings.Join(parts, ">")
}

// FingerprintFromKeys collapses then fingerprints.
func FingerprintFromKeys(keys []string) string {
	return Fingerprint(CollapseConsecutive(keys))
}

// LabelFromSteps builds "a → b ×3 → c".
func LabelFromSteps(steps []Step) string {
	parts := make([]string, 0, len(steps))
	for _, s := range steps {
		lab := LabelFor(s.Key)
		if s.Repeat <= 1 {
			parts = append(parts, lab)
		} else {
			parts = append(parts, fmt.Sprintf("%s ×%d", lab, s.Repeat))
		}
	}
	return strings.Join(parts, " → ")
}

// ParseFingerprint reverses Fingerprint (best-effort) into Steps.
func ParseFingerprint(fp string) []Step {
	fp = strings.TrimSpace(fp)
	if fp == "" {
		return nil
	}
	segs := strings.Split(fp, ">")
	out := make([]Step, 0, len(segs))
	for _, seg := range segs {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		// split on × (multiplication sign U+00D7)
		if i := strings.LastIndex(seg, "×"); i > 0 {
			key := seg[:i]
			var n int
			if _, err := fmt.Sscanf(seg[i+len("×"):], "%d", &n); err == nil && n >= 1 {
				out = append(out, Step{Key: key, Repeat: n})
				continue
			}
		}
		out = append(out, Step{Key: seg, Repeat: 1})
	}
	return out
}
