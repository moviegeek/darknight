package api

import (
	"context"
	"time"
)

// contextWithTimeout returns a fresh context that cancels after d, suitable for
// background scans triggered from request handlers (which themselves may have
// already been cancelled by the time the scan runs).
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
