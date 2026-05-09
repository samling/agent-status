package ui

import (
	"strconv"
	"strings"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

// compareVersions compares two version strings (e.g. "2.1.128" vs "2.1.120",
// or "2.1.128-rc1" vs "2.1.128"). Returns -1 if a<b, 0 if equal, 1 if a>b.
//
// Rules (a pragmatic subset of SemVer 2.0.0):
//   - Empty strings sort lowest.
//   - The portion before the first "-" is split on "." and compared numerically;
//     missing trailing segments are treated as 0 ("2.1" == "2.1.0"). Non-numeric
//     segments fall back to lexical comparison.
//   - A version without a pre-release tag is higher precedence than the same
//     version with one ("2.1.128" > "2.1.128-rc1").
//   - When both have pre-release tags, identifiers are compared field-by-field:
//     numeric identifiers compare numerically and are lower precedence than
//     alphanumeric ones; a shorter set of identifiers is lower precedence when
//     all leading identifiers are equal.
//
// Build metadata (anything after a "+") is ignored for ordering.
func compareVersions(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	// Strip build metadata: it does not affect precedence.
	if i := strings.IndexByte(a, '+'); i >= 0 {
		a = a[:i]
	}
	if i := strings.IndexByte(b, '+'); i >= 0 {
		b = b[:i]
	}
	aMain, aPre, aHasPre := strings.Cut(a, "-")
	bMain, bPre, bHasPre := strings.Cut(b, "-")
	if c := compareDottedNumeric(aMain, bMain); c != 0 {
		return c
	}
	switch {
	case !aHasPre && !bHasPre:
		return 0
	case !aHasPre:
		return 1
	case !bHasPre:
		return -1
	}
	return comparePrerelease(aPre, bPre)
}

// compareDottedNumeric compares two dotted version strings field-by-field.
// Missing trailing fields are treated as 0; non-numeric fields fall back to
// lexical comparison.
func compareDottedNumeric(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for len(ap) < len(bp) {
		ap = append(ap, "0")
	}
	for len(bp) < len(ap) {
		bp = append(bp, "0")
	}
	for i := range ap {
		ai, bi := ap[i], bp[i]
		an, aerr := strconv.Atoi(ai)
		bn, berr := strconv.Atoi(bi)
		if aerr == nil && berr == nil {
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
			continue
		}
		if ai != bi {
			if ai < bi {
				return -1
			}
			return 1
		}
	}
	return 0
}

// comparePrerelease compares the dot-separated identifier lists of two
// pre-release suffixes per SemVer 2.0.0 §11.
func comparePrerelease(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	n := min(len(ap), len(bp))
	for i := range n {
		ai, bi := ap[i], bp[i]
		an, aerr := strconv.Atoi(ai)
		bn, berr := strconv.Atoi(bi)
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aerr == nil:
			// Numeric identifiers always have lower precedence than alphanumeric.
			return -1
		case berr == nil:
			return 1
		default:
			if ai != bi {
				if ai < bi {
					return -1
				}
				return 1
			}
		}
	}
	if len(ap) != len(bp) {
		if len(ap) < len(bp) {
			return -1
		}
		return 1
	}
	return 0
}

// maxVersionByAgent returns the highest version observed per agent across the
// given sessions, using the supplied meta map. Sessions with no recorded
// version are ignored.
func maxVersionByAgent(sessions []state.Session, meta map[string]source.SessionMeta) map[string]string {
	out := map[string]string{}
	for _, s := range sessions {
		v := meta[s.SessionID].Version
		if v == "" {
			continue
		}
		if compareVersions(v, out[s.Agent]) > 0 {
			out[s.Agent] = v
		}
	}
	return out
}
