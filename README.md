# httpg

Build a request, inspect what you are sending, read the response, and replay it.
`httpg` wraps Go's `net/http` with chainable request options, reusable sessions,
HTTP dumps, and cURL export.

## Quick start

```go
import "github.com/padraicbc/httpg"

// Inside a function returning error:
s := httpg.New(httpg.SessBaseURL("https://example.com/api/"))
defer s.CloseIdleConnections()

var items []Item
err := s.R("items").
    Bearer(token).
    QueryParam("q", "coffee").
    Output(os.Stdout).
    DoJSON(&items)
if err != nil {
    return err
}
```

`DoJSON` checks for a successful status, decodes JSON, and closes the response.
Use `Do`, `Get`, or `Post` when you need to retain the response or stream its body.

Run a complete local example, using an in-process server and no external API:

```sh
go run ./examples/inspect
```

## Build and send

Choose the method when building the request or at the end of the chain:

```go
req := s.Request(http.MethodPost, endpoint).
    Context(ctx).
    Header("Authorization", "Bearer "+token).
    JSON(map[string]string{"name": "example"}).
    Retry(false)

res, err := req.Do()
// Equivalent: s.R(endpoint).JSON(payload).Retry(false).Post()
```

| Task | Method |
| --- | --- |
| Set bearer or Basic authentication | `Bearer(token)`, `BasicAuth(user, password)` |
| Set one query parameter | `QueryParam(key, value)` |
| Set one header | `Header(key, value)` |
| Set several headers | `Headers(map[string]string{...})` |
| Set query values, including repeated keys | `Query(url.Values{...})` |
| Encode JSON, or send raw JSON bytes | `JSON(value)` |
| Send bytes with a content type | `Body(data, contentType)` |
| Send a form with repeated keys | `Form(url.Values{...})` |
| Stream a large body without buffering | `StreamBody(reader, size, contentType)` |
| Send multipart fields or files | `MultiPartFormData(parts...)` |
| Set cancellation or a deadline | `Context(ctx)` |
| Control redirects for this request | `Redirect(false)` |
| Execute | `Get()`, `Head()`, `Post()`, `Put()`, `Patch()`, `Delete()`, `Options()` |
| Execute, validate, decode, and close | `DoJSON(&result)` |
| Set total attempts explicitly | `Attempts(3)` |
| Print each exchange | `Output(writer)` |
| Execute a custom method | `Method("PROPFIND").Do()` |

`Query` replaces values for the supplied keys and preserves other query keys.
For JSON, `[]byte` is sent as-is; other values are JSON encoded, including strings.

Configuration errors are retained and returned by execution, inspection, or
`req.Err()`. You can build the whole chain and handle the error once.

## Inspect and export

Add `Output(os.Stdout)` anywhere in the request chain to print every HTTP
exchange, including retries, redirects, cookie-jar headers, response bodies, and
time to response headers. Output uses an HTTP text representation; it is not a
byte-for-byte packet capture. `Output(nil)` disables it. Share writers between
concurrent requests only with synchronization.

Output buffers complete bodies and is intended for finite responses. A writer
or dump failure stops retries and is returned by execution. The request may
already have been sent; close any non-nil response returned alongside an error.
`DoJSON` handles closure automatically, including when output fails.


Inspect a request before sending it:

```go
if _, err := req.WriteTo(os.Stdout); err != nil {
    return err
}
command, err := req.Curl()
if err != nil {
    return err
}
fmt.Println(command)
```

Both requests and responses support `Dump(false)` for headers, `Dump(true)` for
headers and body, and `WriteTo(io.Writer)` for full HTTP output. Use a file or
buffer instead of `os.Stdout` to save the output.

```go
if _, err := res.WriteTo(os.Stdout); err != nil {
    return err
}
var result map[string]interface{}
if err := res.JSON(&result); err != nil {
    return err
}
```

`Text()`, `Bytes()`, and `JSON(&dst)` restore the remaining response body after
reading it, so you can inspect it and then decode it. Use `defer res.Close()` after successful execution (or close `res.Body` directly).
`Do` and the verb methods return HTTP error statuses as normal responses. Call
`res.CheckStatus()` to require any 2xx status, or `res.CheckStatus(200, 304)` for
specific accepted codes. Checking status preserves the response body.

`DoJSON` returns a `*httpg.StatusError` for a non-2xx status. Use `errors.As` to
read `StatusCode`, `Method`, `URL`, `Body`, and `Truncated`. The body preview is
limited to 4 KiB and is also included in the error message. JSON decoding errors
retain their underlying cause. HEAD, 204, and 205 responses skip JSON decoding;
`DoJSON(nil)` discards successful response bodies.

Inspection buffers bodies in memory. Use `res.Body` directly for large downloads
or streams. If you have already consumed part of the body, inspection only sees
what remains; a full HTTP dump may fail if it no longer matches `Content-Length`.

Dumps and cURL commands include credentials and payloads. cURL export uses POSIX
shell quoting and describes the configured request before redirects. It omits
session cookie-jar contents, proxy and transport settings. Bodies containing NUL
bytes cannot be exported this way; use in-process replay instead.

`Debug(true)` logs retry information. `Trace(w)` writes connection and DNS events to `w`.
Full request/response output is explicit through the inspection methods.

## Replay and retries

Keep the request builder to send it again with a fresh body reader:

```go
res, err := req.Do()
if err != nil {
    return err
}
// Read or inspect the first response here.
res.Body.Close()

res, err = req.Replay()
if err != nil {
    return err
}
defer res.Body.Close()
```

Replay uses the builder's current configuration, session headers, and cookies.
It repeats the request, including server-side effects; it is not a frozen network
recording. It also reuses the context: replace an expired one with `Context(ctx)`.

Automatic retries allow **three total attempts for GET, HEAD, OPTIONS, TRACE,
PUT, and DELETE**. POST, PATCH, and custom methods get one attempt by default.
`Attempts(3)` explicitly permits up to three attempts for any method;
`Attempts(1)` or `Retry(false)` disables retries. `Retry(true)` opts any method
into retrying with the configured count.

Retries cover selected server errors, rate limiting, network timeout
errors, and connection resets. `Retry-After` accepts seconds or an HTTP date on
429 and retryable server errors. Cancellation and context deadlines stop retries.
Use `WithRetryPolicy(policy)` to customize the decision. The default wait respects
cancellation; custom `Backoff` callbacks manage their own cancellation.

Explicit replay and retry opt-ins can repeat server-side effects. These attempt
limits describe httpg's retry loop; redirects and Go's transport-level retries
are handled separately by `net/http`.

## Sessions

```go
s := httpg.New(
    httpg.SessBaseURL("https://example.com/api/"),
    httpg.SessRequestTimeout(15*time.Second),
    httpg.SessHeaders(map[string]string{"Accept": "application/json"}),
)
```

`SessBaseURL` uses standard URL resolution: with a base ending in `/api/`,
`R("items")` targets `/api/items`, while `R("/items")` targets `/items` at the
origin. Absolute request URLs override the base.

Invalid session configuration is available through `s.Err()` and is returned
when executing requests. Configuration errors do not terminate your process.
Sessions reuse connections and maintain a cookie jar. Request headers override
session defaults. Configure a session before concurrent use; `SetHeaders` can
update shared headers safely. Use a separate request builder per goroutine.
Request builders and response readers are mutable.

## Error checks

Execution and inspection validate the HTTP(S) URL, method, and headers before
reading request bodies. Invalid session defaults are checked too. Negative
retry delays and timeout settings are rejected; documented negative values that
disable keep-alives remain valid.

`DoJSON` validates that its destination is a non-nil pointer before sending the
request. Plain `nil` still means discard the successful response. Invalid
pointers return a wrapped `*json.InvalidUnmarshalError`.

Nil output writers return errors. `Output(nil)` disables output, while an
interface containing a typed nil writer is a configuration error. Failed or
nil body reopens return errors rather than panicking. Underlying read and reopen
errors remain available through `errors.Is` and `errors.As`.

A custom retry policy returning a negative delay stops execution with an error.
If a response is returned alongside that error, it remains readable and must be
closed. `DoJSON` closes it automatically.

## Development

```sh
go test -race ./...
go vet ./...
```

## Migration from httpgo

The module and package name are now `github.com/padraicbc/httpg` and `httpg`.
The overlapping and unused API from httpgo was removed:

| Removed | Use instead |
| --- | --- |
| `NewRq(target)` | `R(target)` |
| `Retries(n)` | `Attempts(n)` |
| `AddAHeader(k, v)` | `Header(k, v)` |
| `URLEncoded(map)`, `URLEncodedBody(v)` | `Form(url.Values{...})`, `Body(data, contentType)` |
| `Params(map)` | `Query(url.Values{...})`, `QueryParam(k, v)` |
| `Session.Get(rq)`, `Post`, `Put`, `Patch`, `Delete`, `Head`, `Options` | `rq.Get()`, `rq.Post()`, ... or `Session.Do(rq)` |
| `Request` and `Response` type aliases | `Rq` and `Responser` |
| `Data`, `RequestOption`, `ErrTimedout` (unused) | nothing |
| exported `Backoff` func; `Trace()` | `Rq.Backoff` (nil by default); `Trace(w)` |

Other intentional behavior changes:

- Default retries changed from ten attempts for all methods to three for
  idempotent methods. Opt in with `Attempts(n)` to repeat other methods.
- `Attempts(n)` also enables/disables retries according to the count.
- Invalid proxy configuration returns an error instead of terminating the process.
- Body setters copy their input immediately; mutating the original slice no
  longer changes a request. Forms and multipart bodies reset content length.
- Session header names are canonicalized, fixing case-dependent duplicates.
- HTTP/2 negotiation is enabled and the default User-Agent is `httpg/<version>` (the module tag, or `devel` for untagged builds; see `httpg.Version`).
- The unused `isConnectionRefused` helper and unimplemented `ExpoBackoff` panic
  stub were removed. Connection resets use `errors.Is` through wrapped errors.
