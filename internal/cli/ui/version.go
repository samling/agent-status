package ui

import (
	"strconv"
	"strings"

	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/state"
)

// compareVersions compares two dotted-numeric version strings (e.g. "2.1.128"
// vs "2.1.120"). Returns -1 if a<b, 0 if equal, 1 if a>b. Empty strings sort
// lowest. Missing trailing segments are treated as 0 so "2.1" == "2.1.0".
// Non-numeric segments fall back to lexical comparison.
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
