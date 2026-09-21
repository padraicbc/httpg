// Package check holds request validation and nil-detection helpers.
package check

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
)

// Request validates req before inspection or execution can consume a body.
func Request(req *http.Request) error {
	if req == nil || req.URL == nil {
		return fmt.Errorf("httpg: missing request URL")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("httpg: request URL must use http or https")
	}
	if req.URL.Hostname() == "" {
		return fmt.Errorf("httpg: request URL must have a hostname")
	}
	// Let net/http validate method syntax and URL encoding rather than duplicating it.
	if _, err := http.NewRequest(req.Method, req.URL.String(), nil); err != nil {
		return fmt.Errorf("httpg: invalid request: %w", err)
	}
	if req.RequestURI != "" {
		return fmt.Errorf("httpg: client request must not set RequestURI")
	}
	for name, values := range req.Header {
		if !HeaderName(name) {
			return fmt.Errorf("httpg: invalid header name %q", name)
		}
		for _, value := range values {
			if !HeaderValue(value) {
				return fmt.Errorf("httpg: invalid value for header %q", name)
			}
		}
	}
	if !HeaderValue(req.Host) {
		return fmt.Errorf("httpg: invalid Host value")
	}
	return nil
}

// HeaderName reports whether name is a valid HTTP header field name.
func HeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			continue
		}
		return false
	}
	return true
}

// HeaderValue reports whether value contains no control characters other than tab.
func HeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 32 && value[i] != '\t' || value[i] == 127 {
			return false
		}
	}
	return true
}

// JSONDestination requires dst to be a non-nil pointer, or nil when allowDiscard is set.
func JSONDestination(dst any, allowDiscard bool) error {
	if dst == nil && allowDiscard {
		return nil
	}
	value := reflect.ValueOf(dst)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return fmt.Errorf("httpg: JSON destination must be a non-nil pointer: %w", &json.InvalidUnmarshalError{Type: reflect.TypeOf(dst)})
	}
	return nil
}

// NilLike also catches interfaces containing typed nil writers.
func NilLike(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}
