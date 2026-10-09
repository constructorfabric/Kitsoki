package starlark

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPClient is the sandbox's ONLY I/O boundary. Every ctx.http.* call from a
// script funnels through Do. Keeping the surface this narrow means the whole
// sandbox can be made deterministic (for replay) or audited (for production)
// by swapping a single implementation — there is no other way for a script to
// touch the outside world.
//
// headers is the request header set (nil is fine). body is the raw request
// body bytes (nil/empty for GET). The returned *HTTPResponse is never nil when
// err is nil. An err is reserved for transport failure (DNS, connection, replay
// miss); a non-2xx status is NOT an error — it is surfaced via HTTPResponse.Status
// so the script can branch on it.
type HTTPClient interface {
	Do(ctx context.Context, method, url string, headers map[string]string, body []byte) (*HTTPResponse, error)
}

// AuthHTTPClient is an optional extension implemented by transports that can
// resolve symbolic auth policy names into concrete request credentials.
//
// Starlark scripts pass only names such as "jira" or "bitbucket_pat" through
// ctx.http.get/post(auth=...). The transport resolves those names against env
// or other host-side secret stores and mutates the request immediately before
// dispatch. The resolved secret values are never materialized as Starlark
// values.
type AuthHTTPClient interface {
	DoAuth(ctx context.Context, method, url string, headers map[string]string, body []byte, auth []string) (*HTTPResponse, error)
}

// HTTPResponse is the wire result handed back to a script. It is the Go-side
// shape behind the Starlark response object (status / headers / text() / json()).
// Body is the full response bytes; it stays in-process and, in production, is
// recorded only to a cassette — never to the trace.
type HTTPResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

// HTTPExchange is the SUMMARY of one request/response, suitable for the trace.
// It deliberately omits bodies: per the design, the HostReturned trace event
// carries only {method, url, status} so traces stay small and free of secrets,
// while full bodies live in cassettes. Run surfaces a slice of these under the
// reserved output key (see ExchangesOutputKey).
type HTTPExchange struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Status int    `json:"status"`
}

// httpKey is the unexported context key for an injected HTTPClient.
type httpKey struct{}

// WithHTTP injects an HTTPClient into ctx. The host.starlark.run adapter calls
// this in production with a recording client; the testrunner calls it with a
// replay client so a flow fixture exercises the REAL script with HTTP served
// from a cassette. This is the single seam that makes Starlark HTTP testable
// without an orchestrator change.
func WithHTTP(ctx context.Context, c HTTPClient) context.Context {
	return context.WithValue(ctx, httpKey{}, c)
}

// HTTPFromContext resolves the injected HTTPClient. When none was injected it
// returns a deniedClient so a script that performs I/O outside a configured
// host fails with a clear error rather than silently reaching the network with
// some default transport.
func HTTPFromContext(ctx context.Context) HTTPClient {
	if c, ok := ctx.Value(httpKey{}).(HTTPClient); ok && c != nil {
		return c
	}
	return deniedClient{}
}

// HasHTTPClient reports whether a client was explicitly injected into ctx via
// WithHTTP. The host.starlark.run adapter uses it to decide whether to install a
// production recording client: when the testrunner has already injected a replay
// client (or a caller has deliberately injected any client, including one that
// denies all I/O), the adapter must leave it in place. The safe default deny is
// applied by HTTPFromContext when nothing was injected, not by storing a
// deniedClient here — so any value present means an intentional choice to honor.
func HasHTTPClient(ctx context.Context) bool {
	return ctx.Value(httpKey{}) != nil
}

// deniedClient is the safe default: every request is refused. It guarantees the
// sandbox never reaches the network unless a client was deliberately injected.
type deniedClient struct{}

func (deniedClient) Do(_ context.Context, method, url string, _ map[string]string, _ []byte) (*HTTPResponse, error) {
	return nil, fmt.Errorf("starlark: no HTTP client configured for this run (attempted %s %s); inject one with WithHTTP", method, url)
}

// RecordingClient is the production HTTPClient. It performs the request with a
// real *http.Client and appends a summary of each exchange to Exchanges so the
// adapter can surface it on the trace. The full response body is returned to
// the caller (and thus available to the script) but the summary it records is
// body-free by design.
//
// A RecordingClient is single-run scoped: the adapter constructs one per
// host.starlark.run invocation so Exchanges reflects exactly that script's I/O.
type RecordingClient struct {
	// Client is the underlying transport. When nil a default client with a
	// modest timeout is used.
	Client *http.Client
	// Exchanges accumulates one summary per Do call, in call order.
	Exchanges []HTTPExchange
}

// NewRecordingClient returns a RecordingClient with a default 30s-timeout
// *http.Client. Callers that need a custom transport (proxy, TLS config) set
// .Client directly afterwards.
func NewRecordingClient() *RecordingClient {
	return &RecordingClient{Client: &http.Client{Timeout: 30 * time.Second}}
}

// Do performs the request and records its summary. It is the production path:
// the only place the sandbox actually opens a socket.
func (r *RecordingClient) Do(ctx context.Context, method, url string, headers map[string]string, body []byte) (*HTTPResponse, error) {
	cl := r.Client
	if cl == nil {
		cl = &http.Client{Timeout: 30 * time.Second}
	}
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("starlark http: build request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("starlark http: %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("starlark http: read body of %s %s: %w", method, url, err)
	}
	respHeaders := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}
	// Record only the body-free summary — full bodies belong in cassettes.
	r.Exchanges = append(r.Exchanges, HTTPExchange{Method: method, URL: url, Status: resp.StatusCode})
	return &HTTPResponse{
		Status:  resp.StatusCode,
		Headers: respHeaders,
		Body:    respBody,
	}, nil
}
