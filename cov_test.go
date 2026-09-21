package httpg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Method", r.Method)
		w.Header().Set("X-Query", r.URL.RawQuery)
		w.Header().Set("X-CT", r.Header.Get("Content-Type"))
		w.Header().Set("X-Auth", r.Header.Get("Authorization"))
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestRqMethods(t *testing.T) {
	srv := echoServer(t)
	s := New()
	for name, call := range map[string]func(*Rq) (*Responser, error){
		"GET": (*Rq).Get, "HEAD": (*Rq).Head, "POST": (*Rq).Post, "PUT": (*Rq).Put,
		"PATCH": (*Rq).Patch, "DELETE": (*Rq).Delete, "OPTIONS": (*Rq).Options,
	} {
		res, err := call(s.R(srv.URL))
		if err != nil {
			t.Fatal(name, err)
		}
		if got := res.Header.Get("X-Method"); got != name {
			t.Errorf("%s: got %s", name, got)
		}
		_ = res.Close()
	}
}

func TestRequestOptions(t *testing.T) {
	srv := echoServer(t)
	s := New()

	res, err := s.R(srv.URL).
		Headers(map[string]string{"X-A": "1"}).
		Query(url.Values{"a": {"1"}, "b": {"x", "y"}}).
		BasicAuth("u", "p").
		CloseConnection().
		Debug(true).
		Retry(true).
		TooManyRequestsSleep(time.Millisecond).
		Form(url.Values{"k": {"v"}, "l": {"1", "2"}}).
		Post()
	if err != nil {
		t.Fatal(err)
	}
	if q := res.Header.Get("X-Query"); !strings.Contains(q, "a=1") || !strings.Contains(q, "b=x") {
		t.Errorf("query %q", q)
	}
	if !strings.HasPrefix(res.Header.Get("X-Auth"), "Basic ") {
		t.Error("basic auth")
	}
	body, _ := res.Text()
	if v, _ := url.ParseQuery(body); v.Get("k") != "v" || len(v["l"]) != 2 {
		t.Errorf("form %q", body)
	}
	_ = res.Close()

	if s.R(srv.URL).TooManyRequestsSleep(-1).Err() == nil {
		t.Error("negative sleep")
	}
	if s.R(srv.URL).WithRetryPolicy(nil).Err() == nil {
		t.Error("nil policy")
	}
	r := s.R(srv.URL).WithRetryPolicy(DefaultRetryPolicy).Attempts(2)
	if r.Err() != nil || r.retries != 2 {
		t.Error("retries")
	}
	if s.R(srv.URL).Retry(false).retry {
		t.Error("Retry(false)")
	}
	if s.R(srv.URL).TempErrRetrySleep(-1).Err() == nil || s.R(srv.URL).StatusRetrySleep(-1).Err() == nil {
		t.Error("negative sleeps")
	}
	r = s.R(srv.URL).Redirect(false)
	if r.redirect == nil || *r.redirect {
		t.Error("redirect")
	}
	if s.R(srv.URL).Context(nil).Err() == nil { //nolint:staticcheck // exercising nil check
		t.Error("nil context")
	}
	if s.R(srv.URL).Attempts(0).Err() == nil {
		t.Error("attempts 0")
	}
	if s.R(srv.URL).JSON(make(chan int)).Err() == nil {
		t.Error("json chan")
	}
	if s.R(srv.URL).Method("GET").OctetStream([]byte("x")).Request.Header.Get("Content-Type") != "application/octet-stream" {
		t.Error("octet")
	}
	if s.R(srv.URL).QueryParam("a", "b").Request.URL.RawQuery != "a=b" {
		t.Error("QueryParam")
	}
	if s.R(srv.URL).Bearer("t").Request.Header.Get("Authorization") != "Bearer t" {
		t.Error("Bearer")
	}
}

func TestTrace(t *testing.T) {
	srv := echoServer(t)
	var tb bytes.Buffer
	res, err := New().R(srv.URL).Trace(&tb).Get()
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Close()
	if !strings.Contains(tb.String(), "Got Conn") {
		t.Errorf("trace output %q", tb.String())
	}
	var nb *bytes.Buffer
	if New().R(srv.URL).Trace(nb).Err() == nil {
		t.Error("nil trace writer")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read boom") }

type errWriter struct{ n int }

func (w errWriter) Write(_ []byte) (int, error) { return w.n, errors.New("write boom") }

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestMultiPart(t *testing.T) {
	srv := echoServer(t)
	s := New()
	res, err := s.R(srv.URL).MultiPartFormData(
		MultiPartData{FieldName: "a", Val: "1"},
		MultiPartData{FieldName: "empty"},
		MultiPartData{R: strings.NewReader("file"), FileFieldName: "f", FileName: "f.txt"},
	).Post()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := res.Text()
	if !strings.Contains(body, "file") || !strings.Contains(body, `name="a"`) {
		t.Errorf("body %q", body)
	}
	_ = res.Close()
	if s.R(srv.URL).MultiPartFormData(MultiPartData{}).Err() == nil {
		t.Error("empty part")
	}
	if s.R(srv.URL).MultiPartFormData(MultiPartData{R: errReader{}, FileFieldName: "f", FileName: "x"}).Err() == nil {
		t.Error("reader error")
	}
	if r := s.R(srv.URL).MultiPartFromMap(map[string]string{"a": "b"}); r.Err() != nil {
		t.Error(r.Err())
	}
}

func TestSessionOptions(t *testing.T) {
	srv := echoServer(t)
	dialed := false
	s := New(
		SessRedirect(true), SessMaxRedirects(3), SessDebug(true), SessSkipVerify(true),
		SessDisableKeepAlives(true), MaxTLS(0x0303), SessRequestTimeout(time.Second),
		DialTimeOut(time.Second), SessMaxIdleConns(5), SessMaxIdleConnsPerHost(5),
		SessTLSHandshakeTimeout(time.Second), SessIdleConnTimeout(time.Second),
		KeepAlive(time.Second), SessExpectContinueTimeout(time.Second),
		SessHeaders(map[string]string{"x-s": "1"}),
		SessDialContext(func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialed = true
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}),
		SessBaseURL(srv.URL+"/"),
	)
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	s.SetHeaders(map[string]string{"x-t": "2"})
	res, err := s.R("items").Get()
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Close()
	if !dialed {
		t.Error("dial context unused")
	}
	s.CloseIdleConnections()
	(*Session)(nil).CloseIdleConnections()

	if err := s.cli.CheckRedirect(nil, make([]*http.Request, 3)); err == nil {
		t.Error("redirect limit")
	}
	if err := s.cli.CheckRedirect(nil, nil); err != nil {
		t.Error(err)
	}
	s2 := New(SessRedirect(false))
	if err := s2.cli.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Error("no redirect")
	}

	if err := New(SessProxyURL("http://127.0.0.1:1")).Err(); err != nil {
		t.Error(err)
	}
	if New(SessProxyURL("http://127.0.0.1:1")).ProxyURL == "" {
		t.Error("ProxyURL")
	}
	for name, opt := range map[string]SessionConfig{
		"maxredir":    SessMaxRedirects(0),
		"proxyparse":  SessProxyURL("::"),
		"proxyscheme": SessProxyURL("ftp://x"),
		"timeout":     SessRequestTimeout(-1),
		"dial":        DialTimeOut(-1),
		"idle":        SessMaxIdleConns(-1),
		"tls":         SessTLSHandshakeTimeout(-1),
		"idleto":      SessIdleConnTimeout(-1),
		"expect":      SessExpectContinueTimeout(-1),
		"baseparse":   SessBaseURL("::"),
		"basehost":    SessBaseURL("/relative"),
	} {
		if New(opt).Err() == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if New(nil).Err() == nil {
		t.Error("nil option")
	}
	if err := (*Session)(nil).Err(); err == nil {
		t.Error("nil session Err")
	}
	if err := (*Rq)(nil).Err(); err == nil {
		t.Error("nil rq Err")
	}
	s3 := New(SessMaxRedirects(0))
	if _, err := s3.R(srv.URL).Get(); err == nil {
		t.Error("session error should surface")
	}
	s3.fail(errors.New("second")) // first error wins
}

func TestBackoffAndDoErrors(t *testing.T) {
	if _, err := (*Session)(nil).Do(nil); err == nil {
		t.Error("nil session")
	}
	if _, err := (&Session{}).Do(nil); err == nil {
		t.Error("nil client")
	}
	if _, err := New().Do(nil); err == nil {
		t.Error("nil rq")
	}
	if _, err := (*Rq)(nil).Do(); err == nil {
		t.Error("nil rq Do")
	}
	if _, err := (*Rq)(nil).Get(); err == nil {
		t.Error("nil rq Get")
	}
	if _, err := (&Rq{}).Get(); err == nil {
		t.Error("empty rq Get")
	}
	if _, err := (*Rq)(nil).Dump(true); err == nil {
		t.Error("nil Dump")
	}
	if _, err := (*Rq)(nil).Curl(); err == nil {
		t.Error("nil Curl")
	}
	if _, err := New().R("ftp://x").Curl(); err == nil {
		t.Error("bad url Curl")
	}
	if _, err := New().R("ftp://x").Dump(true); err == nil {
		t.Error("bad url Dump")
	}
	if _, err := New().R("ftp://x").WriteTo(io.Discard); err == nil {
		t.Error("bad url WriteTo")
	}
	var nilSess *Session
	if nilSess.R("http://x").Err() != nil {
		t.Error("nil session NewRq")
	}
	if _, err := nilSess.R("http://x").Get(); err == nil {
		t.Error("nil session Get")
	}
	if _, err := (&Rq{Request: &http.Request{}}).prepare(nil); err == nil {
		t.Error("missing URL")
	}
	if _, err := New().R("http://x").Method("GET").Body(nil, "").prepare(nil); err != nil {
		t.Error(err)
	}
}

func TestRetryLoopBranches(t *testing.T) {
	// negative delay from policy
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	s := New()
	r := s.R(srv.URL).WithRetryPolicy(func(*http.Response, *Rq, error) (bool, time.Duration, string) {
		return true, -1, "neg"
	})
	res, err := r.Get()
	if err == nil || res == nil {
		t.Fatalf("negative delay: %v %v", res, err)
	}
	_ = res.Close()

	// negative delay with no response
	r = s.R("http://127.0.0.1:1").WithRetryPolicy(func(*http.Response, *Rq, error) (bool, time.Duration, string) {
		return true, -1, "neg"
	})
	if res, err := r.Get(); err == nil || res != nil {
		t.Fatalf("negative delay no res: %v %v", res, err)
	}

	// default backoff with debug + custom backoff, both exercised
	calls := 0
	r = s.R(srv.URL).Debug(true).Attempts(2).StatusRetrySleep(time.Millisecond)
	res, err = r.Get()
	if err != nil || res.StatusCode != 503 {
		t.Fatal(err)
	}
	_ = res.Close()
	r = s.R(srv.URL).Attempts(2).StatusRetrySleep(time.Millisecond)
	r.Backoff = func(int, time.Duration, string) { calls++ }
	res, _ = r.Get()
	_ = res.Close()
	if calls != 1 {
		t.Errorf("custom backoff calls=%d", calls)
	}

	// context cancelled before start
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.R(srv.URL).Context(ctx).Get(); !errors.Is(err, context.Canceled) {
		t.Errorf("pre-cancelled: %v", err)
	}

	// context cancelled mid-request
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.R(slow.URL).Context(ctx).Get(); err == nil {
		t.Error("expected timeout")
	}

	// per-request redirect toggle
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/next", http.StatusFound)
			return
		}
		w.WriteHeader(200)
	}))
	defer redir.Close()
	res, err = s.R(redir.URL).Redirect(false).Get()
	if err != nil || res.StatusCode != http.StatusFound {
		t.Fatalf("no follow: %v", err)
	}
	_ = res.Close()
	res, err = s.R(redir.URL).Redirect(true).Get()
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("follow: %v", err)
	}
	_ = res.Close()
}

func TestDefaultRetryPolicyBranches(t *testing.T) {
	r := New().R("http://x")
	if ok, _, _ := DefaultRetryPolicy(nil, r, nil); ok {
		t.Error("nil res")
	}
	if ok, _, _ := DefaultRetryPolicy(&http.Response{StatusCode: 200}, r, nil); ok {
		t.Error("200")
	}
	if ok, _, _ := DefaultRetryPolicy(&http.Response{StatusCode: 505}, r, nil); ok {
		t.Error("505")
	}
	if ok, d, _ := DefaultRetryPolicy(&http.Response{StatusCode: 429, Header: http.Header{}}, r, nil); !ok || d != time.Minute {
		t.Error("429")
	}
	if ok, _, _ := DefaultRetryPolicy(&http.Response{StatusCode: 500, Header: http.Header{}}, r, nil); !ok {
		t.Error("500")
	}
	if ok, _, _ := DefaultRetryPolicy(nil, r, errors.New("plain")); ok {
		t.Error("plain error")
	}
	if ok, _, _ := DefaultRetryPolicy(nil, r, timeoutErr{}); !ok {
		t.Error("timeout")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestOutputAndDumps(t *testing.T) {
	srv := echoServer(t)
	s := New()
	var buf bytes.Buffer
	res, err := s.R(srv.URL).JSON(map[string]int{"a": 1}).Output(&buf).Post()
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Close()
	if !strings.Contains(buf.String(), "REQUEST") || !strings.Contains(buf.String(), "RESPONSE") {
		t.Error("output missing")
	}
	// nil / typed nil
	var nb *bytes.Buffer
	if s.R(srv.URL).Output(nb).Err() == nil {
		t.Error("typed nil writer")
	}
	if s.R(srv.URL).Output(nil).Err() != nil {
		t.Error("nil disables")
	}
	// failing writer
	res, err = s.R(srv.URL).Output(errWriter{}).Get()
	if err == nil {
		t.Error("writer error")
	}
	_ = res.Close()
	res, err = s.R(srv.URL).Output(shortWriter{}).Get()
	if err == nil {
		t.Error("short write")
	}
	_ = res.Close()
	// writer failing only after the request is dumped (second write)
	fw := &failAfter{n: 1}
	res, err = s.R(srv.URL).Output(fw).Get()
	_ = res.Close()
	_ = err

	// output against a dead server records ERROR
	buf.Reset()
	if _, err := s.R("http://127.0.0.1:1").Retry(false).Output(&buf).Get(); err == nil {
		t.Error("dead server")
	}
	if !strings.Contains(buf.String(), "ERROR") {
		t.Errorf("no ERROR in %q", buf.String())
	}

	// Rq dumps
	rq := s.R(srv.URL).Method("POST").JSON(map[string]int{"a": 1}).Bearer("tok")
	if _, err := rq.Dump(true); err != nil {
		t.Fatal(err)
	}
	curl, err := rq.Curl()
	if err != nil || !strings.Contains(curl, "--data-binary") {
		t.Errorf("curl %q %v", curl, err)
	}
	rq2 := s.R(srv.URL)
	rq2.Request.Host = "example.test"
	curl, err = rq2.Curl()
	if err != nil || !strings.Contains(curl, "Host: example.test") {
		t.Errorf("curl host %q", curl)
	}
	if _, err := s.R(srv.URL).Body([]byte("a\x00b"), "").Curl(); err == nil {
		t.Error("NUL curl")
	}
	if _, err := rq.WriteTo(nb); err == nil {
		t.Error("nil WriteTo")
	}
	if _, err := rq.WriteTo(errWriter{}); err == nil {
		t.Error("write err")
	}
	if _, err := rq.WriteTo(shortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Error("short write")
	}
	var out bytes.Buffer
	if n, err := rq.WriteTo(&out); err != nil || n == 0 {
		t.Error("WriteTo")
	}
	if _, err := rq.Replay(); err != nil {
		t.Error(err)
	}
}

type failAfter struct{ n, calls int }

func (f *failAfter) Write(p []byte) (int, error) {
	f.calls++
	if f.calls > f.n {
		return 0, errors.New("late boom")
	}
	return len(p), nil
}

func TestResponserHelpers(t *testing.T) {
	srv := echoServer(t)
	s := New()
	res, err := s.R(srv.URL).JSON(map[string]int{"a": 1}).Post()
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	if _, err := res.Dump(true); err != nil {
		t.Error(err)
	}
	if _, err := res.Dump(false); err != nil {
		t.Error(err)
	}
	var out bytes.Buffer
	if _, err := res.WriteTo(&out); err != nil {
		t.Error(err)
	}
	var nb *bytes.Buffer
	if _, err := res.WriteTo(nb); err == nil {
		t.Error("nil writer")
	}
	if _, err := res.WriteTo(errWriter{}); err == nil {
		t.Error("write error")
	}
	if _, err := res.WriteTo(shortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Error("short")
	}
	var v map[string]int
	if err := res.JSON(&v); err != nil || v["a"] != 1 {
		t.Errorf("json %v %v", v, err)
	}
	if err := res.JSON(nil); err == nil {
		t.Error("nil dst")
	}
	if _, err := (*Responser)(nil).Bytes(); err == nil {
		t.Error("nil Bytes")
	}
	if _, err := (*Responser)(nil).Dump(true); err == nil {
		t.Error("nil Dump")
	}
	if err := (*Responser)(nil).Close(); err != nil {
		t.Error("nil Close")
	}
	if err := (&Responser{}).Close(); err != nil {
		t.Error("empty Close")
	}
	if err := (*Responser)(nil).CheckStatus(); err == nil {
		t.Error("nil CheckStatus")
	}

	// read errors are surfaced and body restored
	bad := &Responser{Response: &http.Response{Body: io.NopCloser(errReader{})}}
	if _, err := bad.Bytes(); err == nil {
		t.Error("read error")
	}
	if err := bad.JSON(&v); err == nil {
		t.Error("json read error")
	}
	if _, err := bad.Dump(true); err == nil {
		t.Error("dump read error")
	}
	// ShowBody
	pr := &Responser{ReqBody: strings.NewReader("hello")}
	pr.ShowBody()
	(*Responser)(nil).ShowBody()
	(&Responser{}).ShowBody()
	var logbuf bytes.Buffer
	log.SetOutput(&logbuf)
	defer log.SetOutput(io.Discard)
	(&Responser{ReqBody: seekFail{}}).ShowBody()
	(&Responser{ReqBody: &seekFail{afterCurrent: true}}).ShowBody()
	(&Responser{ReqBody: &seekFail{afterCurrent: true, readOK: false, skipStart: true}}).ShowBody()
}

type seekFail struct {
	afterCurrent, readOK, skipStart bool
}

func (s seekFail) Read([]byte) (int, error) { return 0, errors.New("read fail") }
func (s seekFail) Seek(_ int64, whence int) (int64, error) {
	if !s.afterCurrent || (whence == io.SeekStart && !s.skipStart) {
		return 0, errors.New("seek fail")
	}
	return 0, nil
}

func TestCheckStatusAndDoJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/204":
			w.WriteHeader(204)
		case "/big":
			w.WriteHeader(500)
			_, _ = w.Write(bytes.Repeat([]byte("x"), 5000))
		case "/bad":
			_, _ = w.Write([]byte("nope"))
		default:
			_, _ = w.Write([]byte(`{"a":1}`))
		}
	}))
	defer srv.Close()
	s := New()
	var v map[string]int
	if err := s.R(srv.URL).DoJSON(&v); err != nil || v["a"] != 1 {
		t.Fatal(err)
	}
	if err := s.R(srv.URL).DoJSON(nil); err != nil {
		t.Error(err)
	}
	if err := s.R(srv.URL + "/204").DoJSON(&v); err != nil {
		t.Error(err)
	}
	if err := s.R(srv.URL).Method("HEAD").DoJSON(&v); err != nil {
		t.Error(err)
	}
	if err := s.R(srv.URL + "/bad").DoJSON(&v); err == nil {
		t.Error("bad json")
	}
	if err := s.R(srv.URL).DoJSON(v); err == nil {
		t.Error("non-pointer")
	}
	if err := s.R("ftp://x").DoJSON(&v); err == nil {
		t.Error("request error")
	}
	err := s.R(srv.URL + "/big").Retry(false).DoJSON(&v)
	var se *StatusError
	if !errors.As(err, &se) || !se.Truncated || len(se.Body) != 4096 || !strings.Contains(se.Error(), "500") {
		t.Errorf("status error %v", err)
	}
	res, err := s.R(srv.URL).Get()
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	if err := res.CheckStatus(200); err != nil {
		t.Error(err)
	}
	if err := res.CheckStatus(201); err == nil {
		t.Error("wanted mismatch")
	}
	// no Request, no Body
	if err := (&Responser{Response: &http.Response{StatusCode: 500}}).CheckStatus(); err == nil {
		t.Error("no body")
	}
	e := (&Responser{Response: &http.Response{StatusCode: 500, Body: io.NopCloser(errReader{}), Request: &http.Request{Method: "GET"}}}).CheckStatus()
	if e == nil || !strings.Contains(e.Error(), "read boom") {
		t.Errorf("read error not joined: %v", e)
	}
}

func TestBodyNormalization(t *testing.T) {
	srv := echoServer(t)
	s := New()
	// manually supplied Body without GetBody or BodyBak
	r := s.R(srv.URL).Method("POST")
	r.Request.Body = io.NopCloser(strings.NewReader("manual"))
	res, err := r.Do()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := res.Text(); b != "manual" {
		t.Errorf("body %q", b)
	}
	_ = res.Close()
	// manual body with BodyBak
	r = s.R(srv.URL).Method("POST")
	r.Request.Body = io.NopCloser(strings.NewReader("ignored"))
	r.BodyBak = strings.NewReader("baked")
	res, err = r.Do()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := res.Text(); b != "baked" {
		t.Errorf("body %q", b)
	}
	_ = res.Close()
	// failing BodyBak
	r = s.R(srv.URL).Method("POST")
	r.Request.Body = io.NopCloser(strings.NewReader("x"))
	r.BodyBak = seekFail{}
	if _, err := r.Do(); err == nil {
		t.Error("seek fail")
	}
	r = s.R(srv.URL).Method("POST")
	r.Request.Body = io.NopCloser(strings.NewReader("x"))
	r.BodyBak = seekFail{afterCurrent: true}
	if _, err := r.Do(); err == nil {
		t.Error("seek start fail")
	}
	r = s.R(srv.URL).Method("POST")
	r.Request.Body = io.NopCloser(strings.NewReader("x"))
	r.BodyBak = &seekFail{afterCurrent: true, skipStart: true}
	if _, err := r.Do(); err == nil {
		t.Error("read fail")
	}
	// manual body that fails to read
	r = s.R(srv.URL).Method("POST")
	r.Request.Body = io.NopCloser(errReader{})
	if _, err := r.Do(); err == nil {
		t.Error("read fail")
	}
	// GetBody failure at prepare and in loop
	r = s.R(srv.URL).Method("POST").Body([]byte("x"), "")
	r.Request.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("nope") }
	if _, err := r.Do(); err == nil {
		t.Error("GetBody fail")
	}
	n := 0
	r = s.R(srv.URL).Method("POST").Body([]byte("x"), "")
	r.Request.GetBody = func() (io.ReadCloser, error) {
		n++
		if n > 1 {
			return nil, errors.New("nope")
		}
		return io.NopCloser(strings.NewReader("x")), nil
	}
	if _, err := r.Do(); err == nil {
		t.Error("loop GetBody fail")
	}
	// output requires replayable body
	r = s.R(srv.URL).Method("POST").Output(io.Discard)
	r.Request.Body = io.NopCloser(strings.NewReader("x"))
	r.Request.GetBody = nil
	_ = r // prepare normalises this; exercise transport directly instead
	ot := &outputTransport{base: http.DefaultTransport, writer: io.Discard}
	req, _ := http.NewRequest("POST", srv.URL, io.NopCloser(strings.NewReader("x")))
	if res, err := ot.RoundTrip(req); err == nil { //nolint:bodyclose // error path, no response
		_ = res
		t.Error("non-replayable")
	}
	if res, err := ot.RoundTrip(req); err == nil { //nolint:bodyclose // error path, no response
		_ = res
		t.Error("sticky error")
	}
	ot = &outputTransport{base: http.DefaultTransport, writer: io.Discard}
	req, _ = http.NewRequest("POST", srv.URL, strings.NewReader("x"))
	req.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("nope") }
	if res, err := ot.RoundTrip(req); err == nil { //nolint:bodyclose // error path, no response
		_ = res
		t.Error("GetBody fail")
	}
}

func TestDefaultUserAgentIncludesVersion(t *testing.T) {
	req, err := New().R("http://example.com").prepare(New())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := req.Header.Get("User-Agent"), "httpg/"+Version; got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestRedirectLimitAppliesPerRequest(t *testing.T) {
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	}))
	defer loop.Close()
	// Limit applies by default and after per-request Redirect(true), regardless of option order.
	for _, r := range []*Rq{
		New(SessMaxRedirects(2)).R(loop.URL),
		New(SessMaxRedirects(2)).R(loop.URL).Redirect(true),
	} {
		res, err := r.Get()
		if err == nil || !strings.Contains(err.Error(), "stopped after 2 redirects") {
			t.Errorf("err = %v", err)
		}
		if res != nil {
			_ = res.Close()
		}
	}
}

func TestDontRetryStatus(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(503) }))
	defer srv.Close()
	res, err := New().R(srv.URL).DontRetryStatus(503).Attempts(3).Get()
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Close()
	if calls != 1 {
		t.Errorf("calls = %d", calls)
	}
}

func TestStreamBody(t *testing.T) {
	srv := echoServer(t)
	s := New()
	closed := false
	res, err := s.R(srv.URL).StreamBody(&closeRecorder{Reader: strings.NewReader("streamed"), closed: &closed}, 8, "text/plain").Post()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := res.Text(); b != "streamed" {
		t.Errorf("body %q", b)
	}
	_ = res.Close()
	if !closed {
		t.Error("body not closed")
	}
	res, err = s.R(srv.URL).StreamBody(strings.NewReader("plain"), -1, "").Post()
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Close()

	r := s.R(srv.URL).StreamBody(strings.NewReader("x"), 1, "")
	if _, err := r.Dump(true); err == nil {
		t.Error("Dump")
	}
	if _, err := r.Curl(); err == nil {
		t.Error("Curl")
	}
	if _, err := r.Replay(); err == nil {
		t.Error("Replay")
	}
	if res, err := r.Output(io.Discard).Post(); err == nil {
		t.Error("Output with stream")
	} else if res != nil {
		_ = res.Close()
	}
	var nr *bytes.Reader
	if s.R(srv.URL).StreamBody(nr, 0, "").Err() == nil {
		t.Error("nil stream")
	}
	// Body after StreamBody returns to a replayable body.
	r = s.R(srv.URL).StreamBody(strings.NewReader("x"), 1, "").Body([]byte("y"), "")
	if r.stream {
		t.Error("stream flag not cleared")
	}
	// Streamed requests are attempted once even on retryable status.
	calls := 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(503) }))
	defer bad.Close()
	res, err = s.R(bad.URL).StreamBody(strings.NewReader("x"), 1, "").Attempts(3).Put()
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Close()
	if calls != 1 {
		t.Errorf("calls = %d", calls)
	}
}

type closeRecorder struct {
	io.Reader
	closed *bool
}

func (c *closeRecorder) Close() error { *c.closed = true; return nil }
