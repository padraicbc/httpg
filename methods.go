package httpg

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/padraicbc/httpg/internal/bodyutil"
)

// Do executes a request. HTTP error statuses remain normal responses.
func (s *Session) Do(r *Rq) (*Responser, error) {
	if s == nil || s.cli == nil {
		return nil, fmt.Errorf("httpg: nil session")
	}
	if s.err != nil {
		return nil, s.err
	}
	req, err := r.prepare(s)
	if err != nil {
		return nil, err
	}
	if req.Body != nil {
		defer bodyutil.Close(req.Body)
	}
	client := *s.cli
	var output *outputTransport
	if r.output != nil {
		base := client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		output = &outputTransport{base: base, writer: r.output}
		client.Transport = output
	}
	if r.redirect != nil {
		client.CheckRedirect = s.redirectPolicy(*r.redirect)
	}
	attempts := r.retries
	if r.stream || !r.retry || (!r.retryUnsafe && !idempotent(req.Method)) || attempts < 1 {
		attempts = 1
	}
	policy := r.retryPolicy
	if policy == nil {
		policy = DefaultRetryPolicy
	}
	for attempt := 1; ; attempt++ {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		current := req.Clone(req.Context())
		if req.GetBody != nil {
			current.Body, err = bodyutil.Open(req.GetBody)
			if err != nil {
				return nil, err
			}
		}
		if output != nil {
			output.attempt = attempt
		}
		res, callErr := client.Do(current) //nolint:bodyclose // closed below or handed to the caller in the Responser
		if output != nil && output.err != nil {
			if res == nil {
				return nil, errors.Join(callErr, output.err)
			}
			return &Responser{Response: res, ReqBody: r.BodyBak}, errors.Join(callErr, output.err)
		}
		if req.Context().Err() != nil {
			if res != nil && res.Body != nil {
				bodyutil.Close(res.Body)
			}
			return nil, req.Context().Err()
		}
		retry, delay, reason := false, time.Duration(0), ""
		if attempt < attempts {
			retry, delay, reason = policy(res, r, callErr)
		}
		if retry && delay < 0 {
			policyErr := fmt.Errorf("httpg: retry policy returned a negative delay")
			if res == nil {
				return nil, errors.Join(callErr, policyErr)
			}
			return &Responser{Response: res, ReqBody: r.BodyBak}, errors.Join(callErr, policyErr)
		}
		if !retry || attempt >= attempts {
			if res == nil {
				return nil, callErr
			}
			return &Responser{Response: res, ReqBody: r.BodyBak}, callErr
		}
		if res != nil && res.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
			bodyutil.Close(res.Body)
		}
		if r.debug {
			if s.ProxyURL != "" {
				log.Printf("httpg: retry %d in %s: %s (proxy %s)", attempt, delay, reason, s.ProxyURL)
			} else {
				log.Printf("httpg: retry %d in %s: %s", attempt, delay, reason)
			}
		}
		if r.Backoff != nil {
			r.Backoff(attempt, delay, reason)
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
}

// idempotent limits automatic retries to methods safe to repeat by HTTP semantics.
func idempotent(method string) bool {
	switch method {
	case "", http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}
