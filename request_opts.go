package httpg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http/httptrace"
	"time"

	"github.com/padraicbc/httpg/internal/check"
)

// defaultDontRetryStatus lists status codes the default retry policy never
// retries. It is copied per request and never modified after init.
var defaultDontRetryStatus = map[int]bool{
	505: true, // HTTP Version Not Supported
	506: true, // Variant Also Negotiates.
	507: true, // Insufficient Storage.
	508: true, // Loop Detected
	510: true, // Not Extended
	511: true, // Network Authentication Required

}

// DontRetryStatus replaces the set of status codes the default retry policy
// will not retry for this request.
func (r *Rq) DontRetryStatus(codes ...int) *Rq {
	r.dontRetryStatus = make(map[int]bool, len(codes))
	for _, c := range codes {
		r.dontRetryStatus[c] = true
	}
	return r
}

// TempErrRetrySleep sets the time to wait before
// retrying on temporary/connection error.
// Default is 1 sec.
func (r *Rq) TempErrRetrySleep(t time.Duration) *Rq {
	if t < 0 {
		return r.fail(fmt.Errorf("httpg: retry delay must be nonnegative"))
	}
	r.connRetrySleep = t
	return r
}

// StatusRetrySleep is the time we wait between appropriate errors before retrying.
// Default is 1 sec.
func (r *Rq) StatusRetrySleep(t time.Duration) *Rq {
	if t < 0 {
		return r.fail(fmt.Errorf("httpg: retry delay must be nonnegative"))
	}
	r.retryStatusSleep = t
	return r
}

// Redirect is a per request redirect option
func (r *Rq) Redirect(b bool) *Rq {
	r.redirect = &b
	return r
}

// Retry enables or disables retries. Enabling explicitly permits any method.
func (r *Rq) Retry(enabled bool) *Rq {
	r.retry = enabled
	r.retryUnsafe = enabled
	return r
}

// WithRetryPolicy replaces the policy used to decide whether to retry.
func (r *Rq) WithRetryPolicy(policy RetryPolicy) *Rq {
	if policy == nil {
		return r.fail(fmt.Errorf("httpg: nil retry policy"))
	}
	r.retryPolicy = policy
	return r
}

// TooManyRequestsSleep sets the time to sleep on TooManyRequests errors.
// Default is 60sec.
func (r *Rq) TooManyRequestsSleep(t time.Duration) *Rq {
	if t < 0 {
		return r.fail(fmt.Errorf("httpg: retry delay must be nonnegative"))
	}
	r.retryRateLimitSleep = t
	return r
}

// CloseConnection prevents the re-use of
// TCP connections between requests to the same hosts (after sending this
// request and reading its response), as if
// Transport.DisableKeepAlives were set.
// You might see the number of go routines/memory rising if you are doing a lot of concurrent work,
// Calling CloseConnection may significantly reduce both.
func (r *Rq) CloseConnection() *Rq {
	r.Request.Close = true
	return r
}

// Debug sets whether to see error/retry output
func (r *Rq) Debug(b bool) *Rq {
	r.debug = b
	return r
}

// Headers sets Request Headers on a request basis and/or overwriting any previously set on the session.
func (r *Rq) Headers(hs map[string]string) *Rq {
	for k, v := range hs {
		r.Request.Header.Set(k, v)
	}
	return r
}

// OctetStream adds the byte/OctetStream string to the body.
func (r *Rq) OctetStream(b []byte) *Rq {
	return r.Body(b, "application/octet-stream")
}

// JSON encodes a value as JSON, or sends []byte as raw JSON.
func (r *Rq) JSON(value any) *Rq {
	data, ok := value.([]byte)
	if !ok {
		var err error
		data, err = json.Marshal(value)
		if err != nil {
			return r.fail(fmt.Errorf("httpg: encode JSON: %w", err))
		}
	}
	return r.Body(data, "application/json; charset=UTF-8")
}

// MultiPartFromMap is just a helper calls MultiPartFormData but creates MultiPartDatas to pass from passed in map
func (r *Rq) MultiPartFromMap(mp map[string]string) *Rq {
	parts := []MultiPartData{}

	for k, v := range mp {
		parts = append(parts, MultiPartData{FieldName: k, Val: v})
	}
	return r.MultiPartFormData(parts...)
}

// MultiPartFormData adds
func (r *Rq) MultiPartFormData(mps ...MultiPartData) *Rq {
	var b bytes.Buffer
	wr := multipart.NewWriter(&b)
	for _, m := range mps {
		switch {
		case m.R != nil:

			part, err := wr.CreateFormFile(m.FileFieldName, m.FileName)
			if err != nil {
				return r.fail(err)
			}
			if _, err := io.Copy(part, m.R); err != nil {
				return r.fail(err)
			}

		case m.R == nil && m.FieldName != "":

			part, err := wr.CreateFormField(m.FieldName)
			if err != nil {
				return r.fail(err)
			}
			if m.Val != "" {
				if _, err := part.Write([]byte(m.Val)); err != nil {
					return r.fail(err)
				}
			}
		default:
			return r.fail(fmt.Errorf("httpg: reader or field name is required"))
		}
	}
	// close now not with defer
	if err := wr.Close(); err != nil {
		return r.fail(err)
	}
	return r.Body(b.Bytes(), wr.FormDataContentType())
}

// Context adds optional context to a request.
func (r *Rq) Context(ctx context.Context) *Rq {
	if ctx == nil {
		return r.fail(fmt.Errorf("httpg: nil context"))
	}
	*r.Request = *r.Request.WithContext(ctx)
	return r
}

// Trace writes connection and DNS events to w. It composes with any trace
// already on the request context. Hooks may run on other goroutines, so w must
// be safe for concurrent use if the request can dial in parallel.
func (r *Rq) Trace(w io.Writer) *Rq {
	if check.NilLike(w) {
		return r.fail(fmt.Errorf("httpg: nil trace writer"))
	}
	trace := &httptrace.ClientTrace{
		GotConn: func(connInfo httptrace.GotConnInfo) {
			_, _ = fmt.Fprintf(w, "Got Conn: %+v\n", connInfo)
		},
		DNSDone: func(dnsInfo httptrace.DNSDoneInfo) {
			_, _ = fmt.Fprintf(w, "DNS Info: %+v\n", dnsInfo)
		},
	}

	*r.Request = *r.Request.WithContext(
		httptrace.WithClientTrace(r.Request.Context(), trace))
	return r
}
