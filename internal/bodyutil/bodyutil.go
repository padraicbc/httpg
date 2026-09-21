// Package bodyutil holds helpers for request and response bodies.
package bodyutil

import (
	"bytes"
	"fmt"
	"io"

	"github.com/padraicbc/httpg/internal/check"
)

// Close is a best-effort close for bodies we have already finished reading.
func Close(c io.Closer) {
	if !check.NilLike(c) {
		_ = c.Close()
	}
}

// Open reopens a body through getBody, rejecting nil bodies.
func Open(getBody func() (io.ReadCloser, error)) (io.ReadCloser, error) {
	body, err := getBody()
	if err != nil {
		if !check.NilLike(body) {
			_ = body.Close()
		}
		return nil, fmt.Errorf("httpg: reopen request body: %w", err)
	}
	if check.NilLike(body) {
		return nil, fmt.Errorf("httpg: GetBody returned a nil body without an error")
	}
	return body, nil
}

type restored struct {
	io.Reader
	io.Closer
}

// Restore returns a body that yields data followed by whatever remains in orig,
// and closes orig. Use it after reading part of a body.
func Restore(data []byte, orig io.ReadCloser) io.ReadCloser {
	return &restored{Reader: io.MultiReader(bytes.NewReader(data), orig), Closer: orig}
}
