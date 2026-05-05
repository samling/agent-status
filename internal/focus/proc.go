package focus

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// walkAncestors returns pid followed by its parents up to init or until
// the chain breaks. Capped at 32 levels to bound runaway loops.
func walkAncestors(pid int) []int {
	out := []int{pid}
	cur := pid
	for i := 0; i < 32 && cur > 1; i++ {
		ppid, err := readPPID(cur)
		if err != nil || ppid <= 1 || ppid == cur {
			break
		}
		out = append(out, ppid)
		cur = ppid
	}
	return out
}

func readPPID(pid int) (int, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	s := string(data)
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 {
		return 0, fmt.Errorf("malformed /proc/<pid>/stat")
	}
	fields := strings.Fields(s[rparen+1:])
	if len(fields) < 2 {
		return 0, fmt.Errorf("not enough fields in /proc/<pid>/stat")
	}
	return strconv.Atoi(fields[1])
}
