package httpg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestInspectRetryReplay(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		data, _ := io.ReadAll(req.Body)
		if string(data) != `{"name":"test"}` {
			t.Errorf("body: %q", data)
		}
		if req.Header.Get("X-Test") != "request" {
			t.Errorf("request header overridden")
		}
		if calls == 1 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	s := New(SessHeaders(map[string]string{"X-Test": "session"}))
	r := s.Request("POST", server.URL).Header("X-Test", "request").JSON(map[string]string{"name": "test"}).Attempts(3).StatusRetrySleep(0)
	dump, err := r.Dump(true)
	if err != nil || !bytes.Contains(dump, []byte(`{"name":"test"}`)) {
		t.Fatalf("dump: %s %v", dump, err)
	}
	curl, err := r.Curl()
	if err != nil || !strings.Contains(curl, "--request 'POST'") {
		t.Fatalf("curl: %s %v", curl, err)
	}
	res, err := r.Do()
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var payload struct{ OK bool }
	if err := res.JSON(&payload); err != nil || !payload.OK {
		t.Fatalf("JSON: %+v %v", payload, err)
	}
	if _, err := res.Dump(true); err != nil {
		t.Fatal(err)
	}
	text, err := res.Text()
	if err != nil || text != `{"ok":true}` {
		t.Fatalf("text: %q %v", text, err)
	}
	res2, err := r.Replay()
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if calls != 3 {
		t.Fatalf("calls: %d", calls)
	}
}

func TestConfigurationErrors(t *testing.T) {
	s := New()
	var missingContext context.Context
	for _, r := range []*Rq{
		s.R("://bad").Query(url.Values{"x": {"y"}}),
		s.R("http://example.com").JSON(make(chan int)),
		s.R("http://example.com").Attempts(0),
		s.R("http://example.com").DontRetryStatus().Attempts(-1),
		s.R("http://example.com").Context(missingContext),
	} {
		if _, err := r.Do(); err == nil {
			t.Fatal("expected configuration error")
		}
	}
	if _, err := s.Do(nil); err == nil {
		t.Fatal("expected nil request error")
	}
}

func TestCurlLiteralBody(t *testing.T) {
	r := New().R("https://example.com").Body([]byte("@file's contents"), "text/plain")
	command, err := r.Curl()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(command, "printf '%s' ") || !strings.Contains(command, "--data-binary @-") {
		t.Fatalf("body must be piped literally: %s", command)
	}
	if strings.Contains(command, "--data-binary '@file") {
		t.Fatal("body would be interpreted as a file")
	}
}

func TestCancelRetryWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	r := New().R(server.URL).Context(ctx).StatusRetrySleep(time.Hour)
	r.retryPolicy = func(_ *http.Response, _ *Rq, _ error) (bool, time.Duration, string) {
		cancel()
		return true, time.Hour, "test"
	}
	started := time.Now()
	if _, err := r.Get(); !errors.Is(err, context.Canceled) {
		t.Fatalf("error: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancellation did not interrupt wait")
	}
}

func TestRedirectBodyAndBodyReplacement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Location", "/end")
			w.WriteHeader(307)
			return
		}
		io.Copy(w, r.Body)
	}))
	defer server.Close()
	r := New().R(server.URL).JSON(map[string]string{"old": "body"})
	if _, err := r.Dump(true); err != nil {
		t.Fatal(err)
	}
	r.OctetStream([]byte("replacement"))
	res, err := r.Post()
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	text, _ := res.Text()
	if text != "replacement" {
		t.Fatalf("body: %q", text)
	}
	res, err = r.Redirect(false).Post()
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 307 {
		t.Fatalf("status: %d", res.StatusCode)
	}
}
