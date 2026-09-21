package httpg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWrappedNetworkErrors(t *testing.T) {
	reset := fmt.Errorf("outer: %w", &url.Error{Op: "Get", URL: "http://example.com", Err: &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}})
	req := New().R("http://example.com").TempErrRetrySleep(17 * time.Millisecond)
	retry, delay, _ := DefaultRetryPolicy(nil, req, reset)
	if !retry || delay != 17*time.Millisecond {
		t.Fatalf("retry=%v delay=%v", retry, delay)
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		retry, _, _ := DefaultRetryPolicy(nil, req, fmt.Errorf("wrapped: %w", err))
		if retry {
			t.Errorf("retried %v", err)
		}
	}
}

func TestRetryDefaultsAndOptIn(t *testing.T) {
	for _, tc := range []struct {
		method   string
		attempts int
		want     int
	}{{"GET", 0, 3}, {"PUT", 0, 3}, {"DELETE", 0, 3}, {"POST", 0, 1}, {"PATCH", 0, 1}, {"POST", 2, 2}, {"GET", 1, 1}} {
		t.Run(fmt.Sprintf("%s/%d", tc.method, tc.attempts), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(503) }))
			defer server.Close()
			req := New().Request(tc.method, server.URL).StatusRetrySleep(0)
			if tc.attempts > 0 {
				req.Attempts(tc.attempts)
			}
			res, err := req.Do()
			if err != nil {
				t.Fatal(err)
			}
			res.Close()
			if calls != tc.want {
				t.Fatalf("calls=%d want=%d", calls, tc.want)
			}
		})
	}
}

func TestOutputRetriesRedirectsAndReplay(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		data, _ := io.ReadAll(r.Body)
		if string(data) != `{"name":"coffee"}` {
			t.Errorf("body=%s", data)
		}
		if calls == 1 {
			w.WriteHeader(503)
			io.WriteString(w, "try again")
			return
		}
		if r.URL.Path == "/start" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "cookie", Path: "/"})
			w.Header().Set("Location", "/end")
			w.WriteHeader(307)
			return
		}
		if c, err := r.Cookie("session"); err != nil || c.Value != "cookie" {
			t.Errorf("cookie=%v err=%v", c, err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	var output bytes.Buffer
	req := New().Request("POST", server.URL+"/start").JSON(map[string]string{"name": "coffee"}).Attempts(2).StatusRetrySleep(0).Output(&output)
	for i := 0; i < 2; i++ {
		res, err := req.Replay()
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct{ OK bool }
		if err := res.JSON(&decoded); err != nil || !decoded.OK {
			t.Fatalf("decode=%+v %v", decoded, err)
		}
		res.Close()
	}
	if calls != 5 {
		t.Fatalf("calls=%d", calls)
	}
	for _, part := range []string{"attempt 2", "503 Service Unavailable", "307 Temporary Redirect", "200 OK", "Cookie: session=cookie", "POST /end", "try again"} {
		if !strings.Contains(output.String(), part) {
			t.Errorf("output missing %q", part)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

type failWriter struct {
	calls, failAt int
	err           error
}

func (w *failWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == w.failAt {
		return 0, w.err
	}
	return len(p), nil
}

func TestOutputFailureStopsRetries(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			s := New()
			calls := 0
			body := &trackedBody{Reader: strings.NewReader("retry")}
			s.cli.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				r.Body.Close()
				return &http.Response{StatusCode: 503, Status: "503 Service Unavailable", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header), Body: body, ContentLength: 5, Request: r}, nil
			})
			failure := errors.New("disk full")
			res, err := s.R("http://example.com").Body([]byte("body"), "text/plain").Attempts(3).Output(&failWriter{failAt: failAt, err: failure}).Do()
			if !errors.Is(err, failure) {
				t.Fatalf("err=%v", err)
			}
			if calls != failAt-1 {
				t.Fatalf("calls=%d", calls)
			}
			if res != nil {
				res.Close()
				if !body.closed {
					t.Fatal("body not closed")
				}
			}
		})
	}
}

func TestDoJSONAndStatusErrors(t *testing.T) {
	for _, tc := range []struct {
		status  int
		body    string
		wantErr bool
	}{{200, `{"ok":true}`, false}, {204, "", false}, {200, "not JSON", true}, {400, strings.Repeat("x", 5000), true}} {
		t.Run(fmt.Sprint(tc.status, tc.wantErr), func(t *testing.T) {
			s := New()
			body := &trackedBody{Reader: strings.NewReader(tc.body)}
			s.cli.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Status: fmt.Sprintf("%d %s", tc.status, http.StatusText(tc.status)), Header: make(http.Header), Body: body, Request: r}, nil
			})
			var dst struct{ OK bool }
			err := s.R("http://example.com").DoJSON(&dst)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
			if !body.closed {
				t.Fatal("body not closed")
			}
			if tc.status == 400 {
				var status *StatusError
				if !errors.As(err, &status) || len(status.Body) != 4096 || !status.Truncated || status.Method != "GET" {
					t.Fatalf("status=%+v err=%v", status, err)
				}
			}
			if tc.status == 200 && tc.wantErr {
				var syntax *json.SyntaxError
				if !errors.As(err, &syntax) {
					t.Fatalf("lost JSON error: %v", err)
				}
			}
		})
	}
}

func TestCheckStatusPreservesBody(t *testing.T) {
	original := strings.Repeat("x", 5000)
	body := &trackedBody{Reader: strings.NewReader(original)}
	res := &Responser{Response: &http.Response{StatusCode: 404, Body: body}}
	if err := res.CheckStatus(404); err != nil {
		t.Fatal(err)
	}
	var status *StatusError
	if err := res.CheckStatus(); !errors.As(err, &status) {
		t.Fatal(err)
	}
	text, err := res.Text()
	if err != nil || text != original {
		t.Fatalf("lost body: len=%d err=%v", len(text), err)
	}
	res.Close()
	if !body.closed {
		t.Fatal("not closed")
	}
}

func TestSessionErrorsAndHeaders(t *testing.T) {
	for _, s := range []*Session{New(SessProxyURL("://bad")), New(SessProxyURL("example.com")), New(SessBaseURL("/relative")), New(SessRequestTimeout(-1)), New(nil)} {
		if s.Err() == nil {
			t.Fatal("missing configuration error")
		}
		if _, err := s.R("http://example.com").Do(); err == nil {
			t.Fatal("request ignored configuration error")
		}
	}
	s := New(SessHeaders(map[string]string{"user-agent": "custom", "x-test": "old"}), SessBaseURL("https://example.com/api/"))
	s.SetHeaders(map[string]string{"X-TEST": "new"})
	req := s.R("items").QueryParam("q", "coffee & tea").Bearer("token")
	prepared, err := req.prepare(s)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.URL.String() != "https://example.com/api/items?q=coffee+%26+tea" {
		t.Fatal(prepared.URL)
	}
	if prepared.Header.Get("User-Agent") != "custom" || prepared.Header.Get("X-Test") != "new" || prepared.Header.Get("Authorization") != "Bearer token" {
		t.Fatal(prepared.Header)
	}
	if len(prepared.Header.Values("User-Agent")) != 1 {
		t.Fatal("duplicate user-agent")
	}
}

func TestBodySettersCopyAndResetLength(t *testing.T) {
	data := []byte(`{"ok":true}`)
	req := New().R("http://example.com").JSON(data)
	data[0] = 'x'
	copied, _ := req.Request.GetBody()
	got, _ := io.ReadAll(copied)
	copied.Close()
	if string(got) != `{"ok":true}` {
		t.Fatalf("input aliased: %s", got)
	}
	req.MultiPartFromMap(map[string]string{"name": "coffee"})
	if req.Err() != nil {
		t.Fatal(req.Err())
	}
	copied, _ = req.Request.GetBody()
	got, _ = io.ReadAll(copied)
	copied.Close()
	if req.Request.ContentLength != int64(len(got)) {
		t.Fatalf("length=%d actual=%d", req.Request.ContentLength, len(got))
	}
	req.Form(url.Values{"tag": {"one", "two"}})
	copied, _ = req.Request.GetBody()
	got, _ = io.ReadAll(copied)
	copied.Close()
	if string(got) != "tag=one&tag=two" {
		t.Fatalf("form=%s", got)
	}
}

func TestOutputHEADAndEmptyResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/empty" {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Length", "100")
	}))
	defer server.Close()
	var output bytes.Buffer
	s := New()
	var dst map[string]string
	if err := s.Request("HEAD", server.URL).Output(&output).DoJSON(&dst); err != nil {
		t.Fatal(err)
	}
	if err := s.R(server.URL + "/empty").Output(&output).DoJSON(&dst); err != nil {
		t.Fatal(err)
	}
}
