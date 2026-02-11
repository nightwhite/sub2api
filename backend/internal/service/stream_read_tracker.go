package service

import (
	"io"
	"sync/atomic"
	"time"
)

// upstreamReadTracker wraps an upstream reader and updates lastReadAt whenever any bytes are read.
// This helps avoid false "idle" timeouts when the upstream is slowly streaming a very long SSE line
// (bytes are arriving, but a line delimiter hasn't arrived yet).
type upstreamReadTracker struct {
	r          io.Reader
	lastReadAt *int64
	bytesRead  *int64
}

func (t upstreamReadTracker) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		if t.lastReadAt != nil {
			atomic.StoreInt64(t.lastReadAt, time.Now().UnixNano())
		}
		if t.bytesRead != nil {
			atomic.AddInt64(t.bytesRead, int64(n))
		}
	}
	return n, err
}
