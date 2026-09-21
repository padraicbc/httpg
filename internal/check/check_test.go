package check

import (
	"net/http"
	"net/url"
	"testing"
)

func TestHeaderValidation(t *testing.T) {
	if HeaderName("") || HeaderName("a b") || !HeaderName("X-Ok") {
		t.Error("HeaderName")
	}
	if HeaderValue("a\nb") || HeaderValue("a\x7f") || !HeaderValue("a\tb") {
		t.Error("HeaderValue")
	}
}

func TestNilLike(t *testing.T) {
	if !NilLike(nil) || NilLike(1) || !NilLike((*int)(nil)) || NilLike("x") {
		t.Error("NilLike")
	}
}

func TestRequest(t *testing.T) {
	if Request(nil) == nil {
		t.Error("nil request")
	}
	u, _ := url.Parse("http://h/")
	if err := Request(&http.Request{URL: u, Method: "GET", Header: http.Header{"X-Ok": {"v"}}}); err != nil {
		t.Errorf("valid request: %v", err)
	}
	bad := []*http.Request{
		{URL: &url.URL{Scheme: "ftp", Host: "h"}},
		{URL: &url.URL{Scheme: "http"}},
		{URL: u, Method: "BAD METHOD"},
		{URL: u, RequestURI: "/x"},
		{URL: u, Header: http.Header{"Bad Name": {"v"}}},
		{URL: u, Header: http.Header{"X": {"a\nb"}}},
		{URL: u, Host: "a\nb"},
	}
	for i, r := range bad {
		if Request(r) == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestJSONDestination(t *testing.T) {
	var v map[string]int
	if JSONDestination(&v, false) != nil || JSONDestination(nil, true) != nil {
		t.Error("valid destinations rejected")
	}
	if JSONDestination(nil, false) == nil || JSONDestination((*int)(nil), true) == nil || JSONDestination(v, true) == nil {
		t.Error("invalid destinations accepted")
	}
}
