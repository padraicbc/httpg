package httpg

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"time"

	"golang.org/x/net/publicsuffix"
)

// New creates a new Session using any config passed in and/or
// sets the defaults for timeouts/RequestHeaders/proxy/logging.
func New(opts ...SessionConfig) *Session {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})

	sess := Session{
		err:            err,
		sessionHeaders: map[string]string{"User-Agent": "httpg/" + Version},
		maxRedirects:   10,
	}

	sess.dialer = &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	sess.tls = &tls.Config{
		InsecureSkipVerify: false,
	}

	sess.tr = &http.Transport{
		DialContext:           sess.dialer.DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       time.Second * 30,
		TLSClientConfig:       sess.tls,
		TLSHandshakeTimeout:   time.Second * 10,
		ExpectContinueTimeout: 1 * time.Second,
	}
	sess.tr.Proxy = http.ProxyFromEnvironment
	sess.tr.ForceAttemptHTTP2 = true

	// Set/update any config configs
	sess.cli = &http.Client{
		Jar:       jar,
		Timeout:   time.Second * 30,
		Transport: sess.tr,
	}
	sess.cli.CheckRedirect = sess.redirectPolicy(true)
	// Update any session configs
	for _, opt := range opts {
		if opt == nil {
			sess.fail(fmt.Errorf("httpg: nil session option"))
			continue
		}
		opt(&sess)
	}

	return &sess
}

// Err returns the first session configuration error, also returned by requests.
func (s *Session) Err() error {
	if s == nil {
		return fmt.Errorf("httpg: nil session")
	}
	return s.err
}

func (s *Session) fail(err error) *Session {
	if s.err == nil {
		s.err = err
	}
	return s
}

// CloseIdleConnections releases pooled connections without interrupting requests.
func (s *Session) CloseIdleConnections() {
	if s != nil && s.cli != nil {
		s.cli.CloseIdleConnections()
	}
}

// redirectPolicy follows up to the session's maxRedirects, or none if !follow.
// It reads maxRedirects when invoked so option order does not matter.
func (s *Session) redirectPolicy(follow bool) func(*http.Request, []*http.Request) error {
	return func(_ *http.Request, via []*http.Request) error {
		if !follow {
			return http.ErrUseLastResponse
		}
		if len(via) >= s.maxRedirects {
			return fmt.Errorf("httpg: stopped after %d redirects", s.maxRedirects)
		}
		return nil
	}
}
