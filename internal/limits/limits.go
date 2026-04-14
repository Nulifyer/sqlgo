// Package limits centralizes the byte-size knobs that gate large-data
// behavior across sqlgo: the TUI result buffer cap and the file
// driver's in-memory-vs-temp-file spill threshold both read from the
// same value so users only have one number to tune.
package limits

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

// defaultByteCap is the fallback when SQLGO_BYTE_CAP is unset or
// invalid. 2 GiB matches the file driver's disk-spill threshold and
// is well above what an interactive result grid can render.
const defaultByteCap int64 = 2 << 30

var (
	byteCapOnce sync.Once
	byteCapVal  int64
)

// ByteCap is the configured byte budget for big-data operations.
// Resolved once at first call from SQLGO_BYTE_CAP and cached.
func ByteCap() int64 {
	byteCapOnce.Do(func() {
		v := strings.TrimSpace(os.Getenv("SQLGO_BYTE_CAP"))
		if v == "" {
			byteCapVal = defaultByteCap
			return
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			byteCapVal = defaultByteCap
			return
		}
		byteCapVal = n
	})
	return byteCapVal
}
