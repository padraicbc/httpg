package httpg

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/padraicbc/httpg/internal/bodyutil"
	"github.com/padraicbc/httpg/internal/check"
)

// Bearer sets the Authorization header to a bearer token.
func (r *Rq) Bearer(token string) *Rq { return r.Header("Authorization", "Bearer "+token) }

// BasicAuth sets HTTP Basic authentication.
func (r *Rq) BasicAuth(username, password string) *Rq {
	r.Request.SetBasicAuth(username, password)
	return r
}

// QueryParam replaces a query parameter, preserving all other keys.
func (r *Rq) QueryParam(key, value string) *Rq {
	return r.Query(url.Values{key: {value}})
}

// Form encodes form values, including repeated keys, into a replayable body.
func (r *Rq) Form(values url.Values) *Rq {
	return r.Body([]byte(values.Encode()), "application/x-www-form-urlencoded; charset=UTF-8")
}

// Attempts caps the total number of attempts, including the initial request.
// It also enables retries when n > 1.
func (r *Rq) Attempts(n int) *Rq {
	if n < 1 {
		return r.fail(fmt.Errorf("httpg: attempts must be >= 1"))
	}
	r.retries = n
	r.retry = n > 1
	r.retryUnsafe = n > 1
	return r
}

// Close closes the response body. A nil response is safe to close.
func (r *Responser) Close() error {
	if r == nil || r.Response == nil || r.Body == nil {
		return nil
	}
	return r.Body.Close()
}

// StatusError describes an unexpected HTTP status. Body contains at most 4 KiB
// of the remaining response body; Truncated reports whether more was available.
// Use errors.As to inspect it. Error includes the URL and body preview.
type StatusError struct {
	StatusCode int
	Status     string
	Method     string
	URL        string
	Body       string
	Truncated  bool
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("httpg: %s %s: unexpected HTTP %d (%s): %q", e.Method, e.URL, e.StatusCode, e.Status, e.Body)
}

// CheckStatus accepts any 2xx status by default, or exactly the supplied codes.
// On failure it returns a *StatusError without consuming the remaining body.
func (r *Responser) CheckStatus(codes ...int) error {
	if r == nil || r.Response == nil {
		return fmt.Errorf("httpg: nil response")
	}
	if len(codes) == 0 && r.StatusCode >= 200 && r.StatusCode < 300 {
		return nil
	}
	for _, code := range codes {
		if r.StatusCode == code {
			return nil
		}
	}
	e := &StatusError{StatusCode: r.StatusCode, Status: http.StatusText(r.StatusCode)}
	if r.Request != nil {
		e.Method = r.Request.Method
		if r.Request.URL != nil {
			e.URL = r.Request.URL.Redacted()
		}
	}
	if r.Body == nil {
		return e
	}
	const limit = 4096
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	r.Body = bodyutil.Restore(data, r.Body)
	if len(data) > limit {
		e.Truncated = true
		data = data[:limit]
	}
	e.Body = string(data)
	if err != nil {
		return errors.Join(e, err)
	}
	return e
}

// DoJSON sends the request, requires a 2xx status, decodes JSON, and closes the
// response on every path. A nil dst discards the body. HEAD and 204/205 responses
// succeed without decoding. Use Do and CheckStatus for other accepted statuses
// or to retain access to the response. Errors preserve their underlying cause.
func (r *Rq) DoJSON(dst any) (err error) {
	if err := check.JSONDestination(dst, true); err != nil {
		return err
	}
	res, err := r.Do()
	if res != nil {
		defer func() { err = errors.Join(err, res.Close()) }()
	}
	if err != nil {
		return err
	}
	if err := res.CheckStatus(); err != nil {
		return err
	}
	if dst == nil {
		_, err := io.Copy(io.Discard, res.Body)
		return err
	}
	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusResetContent ||
		(res.Request != nil && res.Request.Method == http.MethodHead) {
		return nil
	}
	if err := res.JSON(dst); err != nil {
		return fmt.Errorf("httpg: decode JSON from %s: %w", res.Status, err)
	}
	return nil
}
