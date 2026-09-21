package httpg

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// SessRedirect sets whether redirects are followed. The default is to follow up to
// SessMaxRedirects redirects, so this is only needed to disable them.
func SessRedirect(b bool) SessionConfig {
	return func(s *Session) *Session {
		s.cli.CheckRedirect = s.redirectPolicy(b)
		return s
	}
}

// SessMaxRedirects sets how many redirects SessRedirect(true) will follow before
// giving up. Default is 10.
func SessMaxRedirects(n int) SessionConfig {
	return func(s *Session) *Session {
		if n < 1 {
			return s.fail(fmt.Errorf("httpg: max redirects must be >= 1"))
		}
		s.maxRedirects = n
		return s
	}
}

// SessDebug enables retry logging for requests created by the session.
func SessDebug(b bool) SessionConfig {
	return func(s *Session) *Session {
		s.debug = b
		return s
	}
}

// SessSkipVerify sets the "InsecureSkipVerify" in the Transports tls.config, default is false.
func SessSkipVerify(b bool) SessionConfig {
	return func(s *Session) *Session {
		s.tls.InsecureSkipVerify = b
		return s
	}
}

// SessDisableKeepAlives  "if true, disables HTTP keep-alives and
// will only use the connection to the server for a single HTTP request.
// This is unrelated to the similarly named TCP keep-alives."
// This is like the "CloseConnection" request option getting set globally on the session.
func SessDisableKeepAlives(b bool) SessionConfig {
	return func(s *Session) *Session {
		s.tr.DisableKeepAlives = b
		return s
	}
}

// MaxTLS sets the "MaxVersion" in the Transports tls.config.
func MaxTLS(v uint16) SessionConfig {
	return func(s *Session) *Session {
		s.tls.MaxVersion = v
		return s
	}
}

// SessProxyURL to be used if required. i.e http://x.x.x.x:port
// Default will try to use ProxyFromEnvironment
func SessProxyURL(u string) SessionConfig {
	return func(s *Session) *Session {
		proxy, err := url.Parse(u)
		if err != nil {
			return s.fail(fmt.Errorf("httpg: invalid proxy URL: %w", err))
		}
		if proxy.Host == "" || (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") {
			return s.fail(fmt.Errorf("httpg: proxy URL must have a host and an http, https, socks5 or socks5h scheme"))
		}
		s.tr.Proxy = http.ProxyURL(proxy)

		s.ProxyURL = u
		return s
	}
}

// SessRequestTimeout sets the amount in seconds that the whole request will timeout,
// including reading headers and body.
func SessRequestTimeout(t time.Duration) SessionConfig {
	return func(s *Session) *Session {
		if t < 0 {
			return s.fail(fmt.Errorf("httpg: request timeout must be nonnegative"))
		}
		s.cli.Timeout = t
		return s
	}
}

// DialTimeOut sets the connection timeout.
func DialTimeOut(t time.Duration) SessionConfig {
	return func(s *Session) *Session {
		if t < 0 {
			return s.fail(fmt.Errorf("httpg: DialTimeOut must be nonnegative"))
		}
		s.dialer.Timeout = t
		return s
	}
}

// SessMaxIdleConns controls the maximum number of idle (keep-alive)
// connections across all hosts. Zero means no limit.
// Defaults is 100
func SessMaxIdleConns(i int) SessionConfig {
	return func(s *Session) *Session {
		if i < 0 {
			return s.fail(fmt.Errorf("httpg: maximum idle connections must be nonnegative"))
		}
		s.tr.MaxIdleConns = i
		return s
	}
}

// SessMaxIdleConnsPerHost set the maximum idle
// (keep-alive) connections per-host.
// Default is 100, ideally it should match the possible concurrency levels/ go routines per host.
func SessMaxIdleConnsPerHost(i int) SessionConfig {
	return func(s *Session) *Session {
		s.tr.MaxIdleConnsPerHost = i
		return s
	}
}

// SessTLSHandshakeTimeout specifies the maximum amount of time waiting to
// wait for a TLS handshake. Zero means no timeout.
// Default is 10 seconds
func SessTLSHandshakeTimeout(t time.Duration) SessionConfig {
	return func(s *Session) *Session {
		if t < 0 {
			return s.fail(fmt.Errorf("httpg: SessTLSHandshakeTimeout must be nonnegative"))
		}
		s.tr.TLSHandshakeTimeout = t
		return s
	}
}

// SessIdleConnTimeout https://golang.org/src/net/http/transport.go, sets the  maximum amount of time in seconds that an idle
// (keep-alive) connection will remain idle before closing
// itself. 0 is no limit. Default is 30 seconds.
func SessIdleConnTimeout(t time.Duration) SessionConfig {
	return func(s *Session) *Session {
		if t < 0 {
			return s.fail(fmt.Errorf("httpg: SessIdleConnTimeout must be nonnegative"))
		}
		s.tr.IdleConnTimeout = t
		return s
	}
}

// KeepAlive sets the keepalive value in seconds. Default 30 seconds
func KeepAlive(t time.Duration) SessionConfig {
	return func(s *Session) *Session {
		s.dialer.KeepAlive = t
		return s
	}
}

// SessDialContext specifies the dial function for creating unencrypted TCP connections.
// If DialContext is nil (and the deprecated Dial below is also nil),
// then the transport dials using package net.
//
// DialContext runs concurrently with calls to RoundTrip.
// A RoundTrip call that initiates a dial may end up using
// an connection dialed previously when the earlier connection
// becomes idle before the later DialContext completes.
func SessDialContext(dc func(ctx context.Context, network, addr string) (net.Conn, error)) SessionConfig {
	return func(s *Session) *Session {
		s.tr.DialContext = dc
		return s
	}
}

// SessExpectContinueTimeout sets the amount of
// time to wait for a server's first response headers after fully
// writing the request headers if the request has an
// "Expect: 100-continue" header. Zero means no timeout and
// causes the body to be sent immediately, without
// waiting for the server to approve.
// This time does not include the time to send the request header.
// Defaults is 1 second
func SessExpectContinueTimeout(t time.Duration) SessionConfig {
	return func(s *Session) *Session {
		if t < 0 {
			return s.fail(fmt.Errorf("httpg: SessExpectContinueTimeout must be nonnegative"))
		}
		s.tr.ExpectContinueTimeout = t
		return s
	}
}

// SessHeaders adds RequestHeaders to the session RequestHeaders map which are used across all requests.
func SessHeaders(hs map[string]string) SessionConfig {
	return func(s *Session) *Session {
		for k, v := range hs {
			s.sessionHeaders[http.CanonicalHeaderKey(k)] = v
		}
		return s
	}
}

// SetHeaders adds headers to the session  which are used across requests
func (s *Session) SetHeaders(hs map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range hs {
		s.sessionHeaders[http.CanonicalHeaderKey(k)] = v
	}
}

// SessBaseURL resolves relative request URLs against an absolute HTTP(S) URL.
// Include a trailing slash to append paths: https://example.com/api/ + items.
// A request path starting with / is relative to the origin, not the base path.
func SessBaseURL(base string) SessionConfig {
	return func(s *Session) *Session {
		u, err := url.Parse(base)
		if err != nil {
			return s.fail(fmt.Errorf("httpg: invalid base URL: %w", err))
		}
		if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return s.fail(fmt.Errorf("httpg: base URL must be absolute HTTP(S)"))
		}
		s.baseURL = u
		return s
	}
}
