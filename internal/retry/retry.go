// Package retry holds helpers for retry decisions.
package retry

import (
	"errors"
	"net/http"
	"strconv"
	"syscall"
	"time"
)

// After returns the delay requested by the Retry-After header (seconds or an
// HTTP date), or fallback if it is absent or invalid.
func After(h http.Header, fallback time.Duration) time.Duration {
	after := h.Get("Retry-After")
	if after == "" {
		return fallback
	}
	if seconds, err := strconv.ParseInt(after, 10, 64); err == nil {
		if seconds < 0 || seconds > int64((1<<63-1)/time.Second) {
			return fallback
		}
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(after); err == nil {
		delay := time.Until(deadline)
		if delay < 0 {
			return 0
		}
		return delay
	}
	return fallback
}

// ConnReset recognizes connection resets through arbitrary error wrapping.
func ConnReset(err error) bool { return errors.Is(err, syscall.ECONNRESET) }
