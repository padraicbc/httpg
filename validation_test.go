package httpg

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type countedReader struct{ reads int }

func (r *countedReader) Read(_ []byte) (int, error) { r.reads++; return 0, io.EOF }
func (r *countedReader) Close() error               { return nil }

func TestInvalidRequestsFailBeforeBodyRead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(*Session) *Rq
	}{
		{"relative URL", func(s *Session) *Rq { return s.R("/items") }},
		{"scheme", func(s *Session) *Rq { return s.R("ftp://example.com/items") }},
		{"hostname", func(s *Session) *Rq { return s.R("http://:80/items") }},
		{"method", func(s *Session) *Rq { return s.Request("BAD METHOD", "http://example.com") }},
		{"header name", func(s *Session) *Rq { return s.R("http://example.com").Header("bad name", "value") }},
		{"header value", func(s *Session) *Rq { return s.R("http://example.com").Header("X-Test", "one\r\ntwo") }},
		{"session header", func(s *Session) *Rq {
			s.SetHeaders(map[string]string{"X-Test": "bad\nvalue"})
			return s.R("http://example.com")
		}},
		{"request URI", func(s *Session) *Rq { r := s.R("http://example.com"); r.Request.RequestURI = "/items"; return r }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			calls := 0
			s.cli.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("unexpected network call")
			})
			req := tc.build(s)
			body := new(countedReader)
			req.Request.Body = body
			if _, err := req.Curl(); err == nil {
				t.Fatal("Curl accepted invalid request")
			}
			if _, err := req.Dump(true); err == nil {
				t.Fatal("Dump accepted invalid request")
			}
			if _, err := req.Do(); err == nil {
				t.Fatal("Do accepted invalid request")
			}
			if body.reads != 0 || calls != 0 {
				t.Fatalf("reads=%d calls=%d", body.reads, calls)
			}
		})
	}
}

func TestJSONDestinationCheckedBeforeSending(t *testing.T) {
	s := New()
	calls := 0
	s.cli.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected network call")
	})
	var missing *map[string]string
	for _, dst := range []interface{}{42, map[string]string{}, missing} {
		err := s.Request("POST", "http://example.com").DoJSON(dst)
		var invalid *json.InvalidUnmarshalError
		if !errors.As(err, &invalid) {
			t.Fatalf("error=%v", err)
		}
	}
	if calls != 0 {
		t.Fatalf("sent %d invalid requests", calls)
	}
	body := new(countedReader)
	res := &Responser{Response: &http.Response{Body: body}}
	if err := res.JSON(nil); err == nil {
		t.Fatal("accepted nil destination")
	}
	if body.reads != 0 {
		t.Fatal("read response before validating destination")
	}
}

func TestNilWritersAndVerbReceivers(t *testing.T) {
	req := New().R("http://example.com")
	var writer *bytes.Buffer
	for _, w := range []io.Writer{nil, writer} {
		if _, err := req.WriteTo(w); err == nil {
			t.Fatal("request accepted nil writer")
		}
		if _, err := (&Responser{}).WriteTo(w); err == nil {
			t.Fatal("response accepted nil writer")
		}
	}
	if req.Output(writer).Err() == nil {
		t.Fatal("Output accepted typed nil writer")
	}
	if New().R("http://example.com").Output(nil).Err() != nil {
		t.Fatal("Output(nil) should disable output")
	}
	for _, r := range []*Rq{nil, {}} {
		for _, run := range []func() (*Responser, error){r.Get, r.Head, r.Post, r.Put, r.Patch, r.Delete, r.Options} {
			if _, err := run(); err == nil {
				t.Fatal("nil request accepted")
			}
		}
	}
}

func TestNegativeConfiguration(t *testing.T) {
	for _, configure := range []func(*Rq) *Rq{
		func(r *Rq) *Rq { return r.TempErrRetrySleep(-time.Second) },
		func(r *Rq) *Rq { return r.StatusRetrySleep(-time.Second) },
		func(r *Rq) *Rq { return r.TooManyRequestsSleep(-time.Second) },
	} {
		if configure(New().R("http://example.com")).Err() == nil {
			t.Fatal("negative retry delay accepted")
		}
	}
	for _, opt := range []SessionConfig{DialTimeOut(-1), SessTLSHandshakeTimeout(-1), SessIdleConnTimeout(-1), SessExpectContinueTimeout(-1), SessMaxIdleConns(-1)} {
		if New(opt).Err() == nil {
			t.Fatal("invalid session option accepted")
		}
	}
	if err := New(KeepAlive(-1), SessMaxIdleConnsPerHost(-1)).Err(); err != nil {
		t.Fatalf("valid disable flags rejected: %v", err)
	}
}

func TestNegativePolicyDelayReturnsResponseWithoutRetry(t *testing.T) {
	s := New()
	calls := 0
	body := &trackedBody{Reader: strings.NewReader("busy")}
	s.cli.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 503, Status: "503 Service Unavailable", Body: body, Header: make(http.Header), Request: r}, nil
	})
	res, err := s.R("http://example.com").WithRetryPolicy(func(*http.Response, *Rq, error) (bool, time.Duration, string) {
		return true, -time.Second, "broken policy"
	}).Do()
	if err == nil || calls != 1 || res == nil {
		t.Fatalf("res=%v err=%v calls=%d", res, err, calls)
	}
	text, readErr := res.Text()
	if readErr != nil || text != "busy" {
		t.Fatalf("body=%q error=%v", text, readErr)
	}
	res.Close()
	if !body.closed {
		t.Fatal("body not closed")
	}
}

type partialErrorReader struct {
	sent    bool
	failure error
}

func (r *partialErrorReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	return copy(p, "partial"), r.failure
}
func (r *partialErrorReader) Close() error { return nil }

func TestBodyErrorsPreserved(t *testing.T) {
	failure := errors.New("body interrupted")
	res := &Responser{Response: &http.Response{Body: &partialErrorReader{failure: failure}}}
	data, err := res.Bytes()
	if !errors.Is(err, failure) || string(data) != "partial" {
		t.Fatalf("data=%s err=%v", data, err)
	}
	data, err = res.Bytes()
	if err != nil || string(data) != "partial" {
		t.Fatalf("body not restored: %s %v", data, err)
	}
	for _, getBody := range []func() (io.ReadCloser, error){
		func() (io.ReadCloser, error) { return nil, failure },
		func() (io.ReadCloser, error) { return nil, nil },
		func() (io.ReadCloser, error) { return (*trackedBody)(nil), failure },
	} {
		req := New().R("http://example.com")
		req.Request.GetBody = getBody
		if _, err := req.Dump(true); err == nil {
			t.Fatal("broken GetBody accepted")
		}
	}
	req := New().R("http://example.com")
	req.Request.GetBody = func() (io.ReadCloser, error) { return nil, failure }
	if _, err := req.Do(); !errors.Is(err, failure) {
		t.Fatalf("lost body error: %v", err)
	}
}
