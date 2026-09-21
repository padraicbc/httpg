package retry

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestAfter(t *testing.T) {
	fallback := 9 * time.Second
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"", fallback}, {"120", 120 * time.Second}, {"0", 0}, {"-2", fallback}, {"9999999999999999999999", fallback}, {"nonsense", fallback}, {time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), 0}} {
		if got := After(http.Header{"Retry-After": {tc.value}}, fallback); got != tc.want {
			t.Errorf("%q = %s want %s", tc.value, got, tc.want)
		}
	}
	got := After(http.Header{"Retry-After": {time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)}}, fallback)
	if got < 58*time.Second || got > time.Minute {
		t.Fatalf("HTTP date delay: %s", got)
	}
	if got := After(http.Header{"Retry-After": {"9223372036854775807"}}, fallback); got != fallback {
		t.Errorf("overflowing seconds = %s", got)
	}
}

func TestConnReset(t *testing.T) {
	reset := fmt.Errorf("outer: %w", &url.Error{Op: "Get", URL: "http://example.com", Err: &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}})
	for _, tc := range []struct {
		err  error
		want bool
	}{{reset, true}, {syscall.ECONNRESET, true}, {nil, false}, {fmt.Errorf("other: %w", syscall.ECONNREFUSED), false}, {errors.New("connection reset by peer"), false}} {
		if got := ConnReset(tc.err); got != tc.want {
			t.Errorf("ConnReset(%v) = %v", tc.err, got)
		}
	}
}
