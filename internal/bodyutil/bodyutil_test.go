package bodyutil

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type recorder struct {
	io.Reader
	closed bool
}

func (r *recorder) Close() error { r.closed = true; return nil }

func TestClose(t *testing.T) {
	Close(nil)
	var nilRec *recorder
	Close(nilRec)
	r := &recorder{}
	Close(r)
	if !r.closed {
		t.Error("not closed")
	}
}

func TestOpen(t *testing.T) {
	if _, err := Open(&http.Request{GetBody: func() (io.ReadCloser, error) { return nil, nil }}); err == nil {
		t.Error("nil body accepted")
	}
	if _, err := Open(&http.Request{GetBody: func() (io.ReadCloser, error) { return io.NopCloser(nil), errors.New("x") }}); err == nil {
		t.Error("error accepted")
	}
	body, err := Open(&http.Request{GetBody: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("ok")), nil }})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := io.ReadAll(body); string(data) != "ok" {
		t.Errorf("body %q", data)
	}
}

func TestRestore(t *testing.T) {
	orig := &recorder{Reader: strings.NewReader("world")}
	body := Restore([]byte("hello "), orig)
	if data, _ := io.ReadAll(body); string(data) != "hello world" {
		t.Errorf("body %q", data)
	}
	_ = body.Close()
	if !orig.closed {
		t.Error("original not closed")
	}
}
