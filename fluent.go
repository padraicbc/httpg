package httpg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"

	"github.com/padraicbc/httpg/internal/bodyutil"
	"github.com/padraicbc/httpg/internal/check"
)

var errStreamed = fmt.Errorf("httpg: streamed request bodies cannot be inspected or replayed")

// Request starts a fluent request. Configuration errors are returned by Do.
func (s *Session) Request(method, target string) *Rq { return s.R(target).Method(method) }

// Method sets the HTTP method to use.
func (r *Rq) Method(method string) *Rq { r.Request.Method = method; return r }

// Header sets a single request header, overwriting any previous value.
func (r *Rq) Header(key, value string) *Rq { r.Request.Header.Set(key, value); return r }

// Query merges values into the request URL's query string, replacing any existing keys.
func (r *Rq) Query(values url.Values) *Rq {
	q := r.Request.URL.Query()
	for k, vs := range values {
		q[k] = append([]string(nil), vs...)
	}
	r.Request.URL.RawQuery = q.Encode()
	return r
}

// Body copies data so subsequent execution, redirects and replay use independent readers.
func (r *Rq) Body(data []byte, contentType string) *Rq {
	r.stream = false
	data = append([]byte(nil), data...)
	r.BodyBak = bytes.NewReader(data)
	r.Request.ContentLength = int64(len(data))
	r.Request.GetBody = func() (io.ReadCloser, error) {
		if len(data) == 0 {
			return http.NoBody, nil
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	r.Request.Body, _ = r.Request.GetBody()
	if contentType != "" {
		r.Request.Header.Set("Content-Type", contentType)
	}
	return r
}

// StreamBody sends rd without buffering it, for large uploads. size is the
// content length, or -1 if unknown (chunked). A streamed body cannot be replayed,
// so the request is attempted once and Dump, Curl, WriteTo, Output and Replay
// are unavailable. rd is closed after sending if it implements io.Closer.
func (r *Rq) StreamBody(rd io.Reader, size int64, contentType string) *Rq {
	if check.NilLike(rd) {
		return r.fail(fmt.Errorf("httpg: nil stream body"))
	}
	body, ok := rd.(io.ReadCloser)
	if !ok {
		body = io.NopCloser(rd)
	}
	r.stream = true
	r.BodyBak = nil
	r.Request.Body = body
	r.Request.GetBody = nil
	r.Request.ContentLength = size
	if contentType != "" {
		r.Request.Header.Set("Content-Type", contentType)
	}
	return r
}

// Err returns the first configuration error recorded on this request, also returned by Do.
func (r *Rq) Err() error {
	if r == nil {
		return fmt.Errorf("httpg: nil request")
	}
	return r.err
}
func (r *Rq) fail(err error) *Rq {
	if r.err == nil {
		r.err = err
	}
	return r
}

// Do executes the request using its currently configured method.
func (r *Rq) Do() (*Responser, error) {
	if r == nil || r.session == nil {
		return nil, fmt.Errorf("httpg: request has no session")
	}
	return r.session.Do(r)
}

// Get executes the request using HTTP GET.
func (r *Rq) Get() (*Responser, error) { return r.execute(http.MethodGet) }

// Head executes the request using HTTP HEAD.
func (r *Rq) Head() (*Responser, error) { return r.execute(http.MethodHead) }

// Post executes the request using HTTP POST.
func (r *Rq) Post() (*Responser, error) { return r.execute(http.MethodPost) }

// Put executes the request using HTTP PUT.
func (r *Rq) Put() (*Responser, error) { return r.execute(http.MethodPut) }

// Patch executes the request using HTTP PATCH.
func (r *Rq) Patch() (*Responser, error) { return r.execute(http.MethodPatch) }

// Delete executes the request using HTTP DELETE.
func (r *Rq) Delete() (*Responser, error) { return r.execute(http.MethodDelete) }

// Options executes the request using HTTP OPTIONS.
func (r *Rq) Options() (*Responser, error) { return r.execute(http.MethodOptions) }

func (r *Rq) execute(method string) (*Responser, error) {
	if r == nil || r.Request == nil {
		return nil, fmt.Errorf("httpg: nil request")
	}
	return r.Method(method).Do()
}

// Replay sends this request again, including its original body. It may repeat side effects.
func (r *Rq) Replay() (*Responser, error) {
	if r != nil && r.stream {
		return nil, errStreamed
	}
	return r.Do()
}

func (r *Rq) prepare(s *Session) (*http.Request, error) {
	if r == nil || r.Request == nil {
		return nil, fmt.Errorf("httpg: nil request")
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.Request.URL == nil {
		return nil, fmt.Errorf("httpg: missing URL")
	}
	req := r.Request.Clone(r.Request.Context())
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	if s != nil {
		s.mu.RLock()
		for k, v := range s.sessionHeaders {
			if _, exists := req.Header[http.CanonicalHeaderKey(k)]; !exists {
				req.Header.Set(k, v)
			}
		}
		s.mu.RUnlock()
	}
	if err := check.Request(req); err != nil {
		return nil, err
	}
	// Normalize legacy body setters and manually supplied bodies once.
	if !r.stream && r.Request.GetBody == nil && r.Request.Body != nil && r.Request.Body != http.NoBody {
		var data []byte
		var err error
		if r.BodyBak != nil {
			pos, seekErr := r.BodyBak.Seek(0, io.SeekCurrent)
			if seekErr != nil {
				return nil, seekErr
			}
			if _, err = r.BodyBak.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
			data, err = io.ReadAll(r.BodyBak)
			_, restoreErr := r.BodyBak.Seek(pos, io.SeekStart)
			if err == nil {
				err = restoreErr
			}
		} else {
			data, err = io.ReadAll(r.Request.Body)
			r.Request.Body = bodyutil.Restore(data, r.Request.Body)
		}
		if err != nil {
			return nil, err
		}
		bodyutil.Close(r.Request.Body)
		r.Body(data, "")
	}
	req.Body = r.Request.Body
	req.GetBody = r.Request.GetBody
	req.ContentLength = r.Request.ContentLength
	if req.GetBody != nil {
		var err error
		req.Body, err = bodyutil.Open(req.GetBody)
		if err != nil {
			return nil, err
		}
	}
	return req, nil
}

// Dump returns an outgoing HTTP request without consuming its body.
// Dumps and Curl include credentials and body data; store them accordingly.
func (r *Rq) Dump(body bool) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("httpg: nil request")
	}
	if r.stream {
		return nil, errStreamed
	}
	req, err := r.prepare(r.session)
	if err != nil {
		return nil, err
	}
	if req.Body != nil {
		defer bodyutil.Close(req.Body)
	}
	return httputil.DumpRequestOut(req, body)
}

// WriteTo dumps the outgoing HTTP request to w without consuming its body.
func (r *Rq) WriteTo(w io.Writer) (int64, error) {
	if check.NilLike(w) {
		return 0, fmt.Errorf("httpg: nil output writer")
	}
	data, err := r.Dump(true)
	if err != nil {
		return 0, err
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return int64(n), err
}

// Curl returns a POSIX-shell command for this configured request, before redirects.
// Session cookies, proxies and transport settings are not exported.
func (r *Rq) Curl() (string, error) {
	if r == nil {
		return "", fmt.Errorf("httpg: nil request")
	}
	if r.stream {
		return "", errStreamed
	}
	req, err := r.prepare(r.session)
	if err != nil {
		return "", err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	args := []string{"curl", "--request", quote(method), "--url", quote(req.URL.String())}
	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range req.Header[k] {
			args = append(args, "--header", quote(k+": "+v))
		}
	}
	if req.Host != "" {
		args = append(args, "--header", quote("Host: "+req.Host))
	}
	if req.Body != nil && req.Body != http.NoBody {
		defer bodyutil.Close(req.Body)
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return "", err
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return "", fmt.Errorf("httpg: cURL export does not support NUL bytes; use Replay")
		}
		args = append(args, "--data-binary", "@-")
		return "printf '%s' " + quote(string(data)) + " | " + strings.Join(args, " "), nil
	}
	return strings.Join(args, " "), nil
}

// Bytes reads the remaining response body and restores it, including on read errors.
func (r *Responser) Bytes() ([]byte, error) {
	if r == nil || r.Response == nil || r.Body == nil {
		return nil, fmt.Errorf("httpg: missing response body")
	}
	data, err := io.ReadAll(r.Body)
	r.Body = bodyutil.Restore(data, r.Body)
	return data, err
}

// Text reads the remaining response body as a string and restores it.
func (r *Responser) Text() (string, error) { data, err := r.Bytes(); return string(data), err }

// JSON decodes the remaining response body as JSON into dst and restores the body.
func (r *Responser) JSON(dst any) error {
	if err := check.JSONDestination(dst, false); err != nil {
		return err
	}
	data, err := r.Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

// Dump returns the HTTP response without consuming its body.
func (r *Responser) Dump(body bool) ([]byte, error) {
	if r == nil || r.Response == nil {
		return nil, fmt.Errorf("httpg: nil response")
	}
	clone := new(http.Response)
	*clone = *r.Response
	if body {
		data, err := r.Bytes()
		if err != nil {
			return nil, err
		}
		clone.Body = io.NopCloser(bytes.NewReader(data))
	}
	return httputil.DumpResponse(clone, body)
}

// WriteTo dumps the HTTP response, including its body, to w.
func (r *Responser) WriteTo(w io.Writer) (int64, error) {
	if check.NilLike(w) {
		return 0, fmt.Errorf("httpg: nil output writer")
	}
	data, err := r.Dump(true)
	if err != nil {
		return 0, err
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return int64(n), err
}
