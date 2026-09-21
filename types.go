package httpg

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/padraicbc/httpg/internal/retry"
)

// RetryPolicy is the Policy to retry errored requests
type RetryPolicy func(*http.Response, *Rq, error) (bool, time.Duration, string)

// DefaultRetryPolicy retries temporary network errors, selected 5xx responses,
// and rate limiting. Cancellation and context deadlines are never retried.
func DefaultRetryPolicy(res *http.Response, r *Rq, err error) (bool, time.Duration, string) {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, 0, ""
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return true, r.connRetrySleep, fmt.Sprintf("network timeout: %v", err)
		}
		if retry.ConnReset(err) {
			return true, r.connRetrySleep, "connection reset by peer"
		}
		return false, 0, ""
	}
	if res == nil || r.dontRetryStatus[res.StatusCode] {
		return false, 0, ""
	}
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
		delay := r.retryStatusSleep
		if res.StatusCode == http.StatusTooManyRequests {
			delay = r.retryRateLimitSleep
		}
		return true, retry.After(res.Header, delay), fmt.Sprintf("HTTP %d", res.StatusCode)
	}
	return false, 0, ""
}

// SessionConfig is the type used for various optional args set for the session using associated methods.
type SessionConfig func(*Session) *Session

// MultiPartData is used  for a multipart/form-data request
//
//	Fieldname is the name= and R is an io.Reader that holds the data#
//
// If R is nil then fieldname and val are required. Both val and R cannot be nil
type MultiPartData struct {
	R             io.Reader
	FileFieldName string
	FileName      string
	FieldName     string
	Val           string
}

// Responser wraps an HTTP response with inspection and decoding helpers.
type Responser struct {
	*http.Response
	ReqBody io.ReadSeeker
}

// ShowBody logs the request body associated with this response, restoring its read position.
func (r *Responser) ShowBody() {
	if r == nil || r.ReqBody == nil {
		return
	}
	pos, err := r.ReqBody.Seek(0, io.SeekCurrent)
	if err != nil {
		log.Print(err)
		return
	}
	defer func() { _, _ = r.ReqBody.Seek(pos, io.SeekStart) }()
	if _, err := r.ReqBody.Seek(0, io.SeekStart); err != nil {
		log.Printf("cannot seek back body: %v", err)
		return
	}

	var b bytes.Buffer
	if _, err := io.Copy(&b, r.ReqBody); err != nil {
		log.Printf("cannot read body: %v", err)
		return
	}

	log.Println(b.String())
}

// Rq is used to create a http.Request with a few added features.
// A Rq is not safe for concurrent use: configure and execute it from one
// goroutine at a time, or build a separate Rq per goroutine.
type Rq struct {
	Request *http.Request

	BodyBak io.ReadSeeker
	// Whether to retry failed requests, default is true
	retry       bool
	retryUnsafe bool
	output      io.Writer
	// Num of times to retry, default is to use the default/set amount or can be altered with
	retries                                               int
	retryPolicy                                           RetryPolicy
	dontRetryStatus                                       map[int]bool
	retryRateLimitSleep, retryStatusSleep, connRetrySleep time.Duration
	// Backoff, if set, replaces the built-in context-aware sleep between attempts.
	// It must honour cancellation itself.
	Backoff  func(attempt int, delay time.Duration, reason string)
	debug    bool
	session  *Session
	err      error
	redirect *bool
	stream   bool
}

// R starts a request for target, resolving it against the session's base URL if set.
func (s *Session) R(target string) *Rq {
	if s != nil && s.baseURL != nil {
		if reference, err := url.Parse(target); err == nil {
			target = s.baseURL.ResolveReference(reference).String()
		}
	}
	req, err := http.NewRequest("", target, nil)
	if err == nil && s != nil {
		err = s.err
	}
	if err != nil {
		req = &http.Request{Header: make(http.Header), URL: &url.URL{}}
	}
	rr := &Rq{
		session:             s,
		err:                 err,
		Request:             req,
		retries:             3,
		retry:               true,
		retryStatusSleep:    1 * time.Second,
		connRetrySleep:      1 * time.Second,
		retryRateLimitSleep: 60 * time.Second,
		retryPolicy:         DefaultRetryPolicy,

		dontRetryStatus: make(map[int]bool),
	}

	if s != nil {
		rr.debug = s.debug
	}
	for code, skip := range defaultDontRetryStatus {
		rr.dontRetryStatus[code] = skip
	}
	return rr
}

// Session is the base of all operations, configuration can be made using SessionConfig methods.
type Session struct {
	cli *http.Client
	mu  sync.RWMutex

	dialer *net.Dialer
	tls    *tls.Config
	tr     *http.Transport

	debug   bool
	err     error
	baseURL *url.URL

	// RequestHeaders used on all requests, can be overridden per request.
	sessionHeaders map[string]string

	// Will be set to whatever proxy is passed(if any).
	// Useful for debugging/logging which proxy was used when using go routines with different proxies.
	// Proxy to be used if required. i.e http://x.x.x.x:port
	// Default will try to use ProxyFromEnvironment
	ProxyURL string

	maxRedirects int
}
