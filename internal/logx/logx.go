// Package logx prints the timestamped progress lines that stages and adapters write to stderr.
package logx

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var mu sync.Mutex

// Printf prints one "[15:04:05] message" line to stderr. It is safe for concurrent use.
func Printf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(os.Stderr, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}
