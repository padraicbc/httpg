package httpg

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/padraicbc/httpg/internal/bodyutil"
	"github.com/padraicbc/httpg/internal/check"
)

// Output writes each HTTP exchange to w, including retries and redirects.
// It includes credentials and complete bodies, buffering them in memory.
// Pass nil to disable. Shared writers require caller synchronization.
// An output failure stops retries and is returned by execution; the request
// may already have reached the server. Close any response returned with an error.
func (r *Rq) Output(w io.Writer) *Rq {
	if w != nil && check.NilLike(w) {
		return r.fail(fmt.Errorf("httpg: nil output writer"))
	}
	r.output = w
	return r
}

type outputTransport struct {
	base    http.RoundTripper
	writer  io.Writer
	attempt int
	err     error
}

func (t *outputTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	handedOff := false
	defer func() {
		if !handedOff && req.Body != nil {
			bodyutil.Close(req.Body)
		}
	}()
	if t.err != nil {
		return nil, t.err
	}
	// Clone headers and obtain a separate body: dumping must not consume the
	// request body passed to the underlying transport.
	clone := req.Clone(req.Context())
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			err := fmt.Errorf("httpg: output requires a replayable request body")
			t.err = err
			return nil, err
		}
		var err error
		clone.Body, err = bodyutil.Open(req)
		if err != nil {
			t.err = err
			return nil, err
		}
		defer bodyutil.Close(clone.Body)
	}
	data, err := httputil.DumpRequestOut(clone, true)
	if err != nil {
		t.err = fmt.Errorf("httpg: dump request: %w", err)
		return nil, t.err
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "\n>>> REQUEST (attempt %d)\n", t.attempt)
	output.Write(data)
	output.WriteByte('\n')
	if err := t.write(output.Bytes()); err != nil {
		return nil, err
	}

	started := time.Now()
	handedOff = true
	res, callErr := t.base.RoundTrip(req)
	output.Reset()
	fmt.Fprintf(&output, "\n<<< RESPONSE (attempt %d, %s to headers)\n", t.attempt, time.Since(started))
	if res != nil {
		data, err := (&Responser{Response: res}).Dump(true)
		if err != nil {
			t.err = fmt.Errorf("httpg: dump response: %w", err)
			return res, callErr
		}
		output.Write(data)
		output.WriteByte('\n')
	}
	if callErr != nil {
		fmt.Fprintf(&output, "ERROR: %v\n", callErr)
	}
	_ = t.write(output.Bytes())
	return res, callErr
}

func (t *outputTransport) write(data []byte) error {
	n, err := t.writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		t.err = fmt.Errorf("httpg: write exchange output: %w", err)
	}
	return t.err
}
