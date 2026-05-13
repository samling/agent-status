package bootstrap

import (
	"strings"
)

// unifiedDiff returns a unified-style line diff between a and b. Each
// changed region is shown together with `context` lines of surrounding
// unchanged text; runs of unchanged lines outside that window are
// elided with a "..." marker. An empty result means a and b are
// identical.
func unifiedDiff(a, b string, context int) string {
	if a == b {
		return ""
	}
	aLines := splitLines(a)
	bLines := splitLines(b)
	ops := lcsDiff(aLines, bLines)

	n := len(ops)
	keep := make([]bool, n)
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		lo := i - context
		if lo < 0 {
			lo = 0
		}
		hi := i + context
		if hi >= n {
			hi = n - 1
		}
		for j := lo; j <= hi; j++ {
			keep[j] = true
		}
	}

	var b2 strings.Builder
	prev := false
	for i, op := range ops {
		if !keep[i] {
			if prev {
				b2.WriteString("...\n")
			}
			prev = false
			continue
		}
		b2.WriteByte(op.kind)
		b2.WriteByte(' ')
		b2.WriteString(op.text)
		b2.WriteByte('\n')
		prev = true
	}
	return b2.String()
}

type editOp struct {
	kind byte // ' ', '-', or '+'
	text string
}

// lcsDiff returns the line-level diff between a and b using LCS DP.
// Suitable for files up to a few thousand lines; cost is O(len(a)*len(b))
// in both time and memory.
func lcsDiff(a, b []string) []editOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
				continue
			}
			if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	var rev []editOp
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && a[i-1] == b[j-1]:
			rev = append(rev, editOp{' ', a[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			rev = append(rev, editOp{'+', b[j-1]})
			j--
		default:
			rev = append(rev, editOp{'-', a[i-1]})
			i--
		}
	}
	ops := make([]editOp, len(rev))
	for k, op := range rev {
		ops[len(rev)-1-k] = op
	}
	return ops
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// A trailing newline yields an empty element; drop it so it doesn't
	// register as a separate diffed line.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// indent prefixes every non-empty line of s with prefix.
func indent(s, prefix string) string {
	if s == "" {
		return s
	}
	var out strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if line == "" {
			continue
		}
		out.WriteString(prefix)
		out.WriteString(line)
	}
	return out.String()
}
