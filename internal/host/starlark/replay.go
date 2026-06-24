package starlark

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// HTTPCassette is a deterministic, body-carrying record of the HTTP exchanges a
// Starlark script makes. It is the replay (and, with a record_mode, the record)
// counterpart of the production transport: a flow fixture supplies one of these
// and the testrunner builds a client from it so the REAL script runs with its
// network served from — or recorded to — disk.
//
// The format is intentionally distinct from the host_cassette used for agent
// replay: a host_cassette episode replaces a whole handler with a canned
// Result, whereas here we want the handler to run for real and only its HTTP to
// be canned. The model is deliberately close to Python's VCR.py — record modes,
// configurable request matchers, secret redaction, and per-interaction
// request/response capture — so authors familiar with VCR find it predictable.
//
//	kind: http_cassette
//	record_mode: none            # none | once | new_episodes | all (default none)
//	match_on: [method, url]      # any of: method, url/uri, scheme, host, port, path, query, body, headers
//	ignore_hosts: ["metrics.internal"]
//	ignore_localhost: false
//	filter_headers: ["X-Api-Key"]            # redacted on write (Authorization/Cookie/Set-Cookie always are)
//	filter_query_parameters: ["token"]
//	filter_post_data_parameters: ["password"]
//	allow_playback_repeats: false            # global; per-episode `replay: any` also works
//	exchanges:
//	  - request:                             # the recorded (VCR-style) form
//	      method: GET
//	      url: "https://api.example.com/v1/widgets/42"
//	      headers: { Accept: application/json }
//	      body: ""
//	    response:
//	      status: 200
//	      headers: { Content-Type: application/json }
//	      body: '{"id":42,"name":"sprocket"}'
//
// Legacy/hand-authored episodes use a `match:` selector instead of `request:`
// (with method, url, OR a url_pattern Go regexp); those continue to work
// unchanged and ignore match_on (they always match on their declared selector).
type HTTPCassette struct {
	Kind string `yaml:"kind" json:"kind"`
	// RecordMode selects record/replay behaviour. Empty == "none" (replay only).
	RecordMode string `yaml:"record_mode,omitempty" json:"record_mode,omitempty"`
	// MatchOn lists the request fields that must match for replay. Empty defaults
	// to [method, url]. Applies only to recorded (`request:`) episodes; legacy
	// `match:` episodes match on their own selector regardless of this.
	MatchOn []string `yaml:"match_on,omitempty" json:"match_on,omitempty"`
	// IgnoreHosts lists hostnames whose requests are passed straight through to
	// the real transport and never recorded or replayed (mirrors VCR.py).
	IgnoreHosts []string `yaml:"ignore_hosts,omitempty" json:"ignore_hosts,omitempty"`
	// IgnoreLocalhost, when true, treats localhost/127.0.0.1/[::1] as ignored.
	IgnoreLocalhost bool `yaml:"ignore_localhost,omitempty" json:"ignore_localhost,omitempty"`
	// FilterHeaders names request/response headers to redact before WRITING the
	// cassette. Authorization, Cookie, Set-Cookie and Proxy-Authorization are
	// always redacted in addition to these.
	FilterHeaders []string `yaml:"filter_headers,omitempty" json:"filter_headers,omitempty"`
	// FilterQueryParameters names URL query parameters to redact on write.
	FilterQueryParameters []string `yaml:"filter_query_parameters,omitempty" json:"filter_query_parameters,omitempty"`
	// FilterPostDataParameters names form/JSON body parameters to redact on write.
	FilterPostDataParameters []string `yaml:"filter_post_data_parameters,omitempty" json:"filter_post_data_parameters,omitempty"`
	// AllowPlaybackRepeats lets every episode satisfy more than one request
	// (global form of per-episode `replay: any`). Default consumes once.
	AllowPlaybackRepeats bool `yaml:"allow_playback_repeats,omitempty" json:"allow_playback_repeats,omitempty"`
	// Exchanges are the recorded interactions, matched in order.
	Exchanges []HTTPEpisode `yaml:"exchanges" json:"exchanges"`
}

// effectiveMatchOn returns the configured matchers or the [method, url] default.
func (c *HTTPCassette) effectiveMatchOn() []string {
	if len(c.MatchOn) > 0 {
		return c.MatchOn
	}
	return []string{"method", "url"}
}

// HTTPEpisode is one recorded request/response in an HTTPCassette.
type HTTPEpisode struct {
	// Request is the recorded request (VCR-style). When present, matching uses
	// the cassette's match_on against these fields. When nil, the episode is a
	// legacy hand-authored one matched via Match below.
	Request *HTTPRequestRecord `yaml:"request,omitempty" json:"request,omitempty"`
	// Match is the legacy selector (method / url / url_pattern). Used only when
	// Request is nil.
	Match HTTPMatch `yaml:"match,omitempty" json:"match,omitempty"`
	// Response is the canned response this episode replays.
	Response HTTPCanned `yaml:"response" json:"response"`
	// Replay set to "any" lets this episode satisfy more than one request
	// (e.g. a polled endpoint). Default (empty) consumes it after one match.
	Replay string `yaml:"replay,omitempty" json:"replay,omitempty"`

	consumed bool
}

// HTTPRequestRecord is the recorded request side of an episode.
type HTTPRequestRecord struct {
	Method  string            `yaml:"method,omitempty" json:"method,omitempty"`
	URL     string            `yaml:"url,omitempty" json:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body    string            `yaml:"body,omitempty" json:"body,omitempty"`
}

// HTTPMatch is the legacy request-side selector for an episode.
type HTTPMatch struct {
	Method     string `yaml:"method,omitempty" json:"method,omitempty"`
	URL        string `yaml:"url,omitempty" json:"url,omitempty"`
	URLPattern string `yaml:"url_pattern,omitempty" json:"url_pattern,omitempty"`
}

// HTTPCanned is the canned response an episode replays.
type HTTPCanned struct {
	Status  int               `yaml:"status" json:"status"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body    string            `yaml:"body,omitempty" json:"body,omitempty"`
}

// httpReq bundles the fields of an incoming request for matching.
type httpReq struct {
	method  string
	url     string
	headers map[string]string
	body    []byte
}

// ReplayClient is an HTTPClient that serves requests from an HTTPCassette
// (replay only — no recording). A request that matches no episode returns a
// clear miss error naming the available episodes, so a drifted script or
// cassette fails loudly. For record/replay use NewRecordReplayClient.
type ReplayClient struct {
	mu        sync.Mutex
	cas       *HTTPCassette
	episodes  []*HTTPEpisode
	exchanges []HTTPExchange
}

// NewReplayClient builds a ReplayClient over the cassette's episodes. The
// returned client is safe for the single-threaded script run; matching is
// stateful (episodes are consumed) and guarded by a mutex for safety.
func NewReplayClient(cas *HTTPCassette) *ReplayClient {
	return &ReplayClient{cas: cas, episodes: episodePtrs(cas)}
}

// episodePtrs returns a pointer slice over a cassette's episodes (so consumed
// state is tracked per episode without copying).
func episodePtrs(cas *HTTPCassette) []*HTTPEpisode {
	if cas == nil {
		return nil
	}
	ptrs := make([]*HTTPEpisode, len(cas.Exchanges))
	for i := range cas.Exchanges {
		ptrs[i] = &cas.Exchanges[i]
	}
	return ptrs
}

// Do matches the request against the cassette and returns the canned response.
// It records a body-free summary so the trace surface is identical to the
// production path.
func (rc *ReplayClient) Do(_ context.Context, method, url string, headers map[string]string, body []byte) (*HTTPResponse, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	req := httpReq{method: method, url: url, headers: headers, body: body}
	ep, err := matchEpisode(rc.cas, rc.episodes, req)
	if err != nil {
		return nil, err
	}
	if ep == nil {
		return nil, missError(method, url, rc.episodes)
	}
	rc.exchanges = append(rc.exchanges, HTTPExchange{Method: method, URL: url, Status: ep.Response.Status})
	return cannedResponse(ep.Response), nil
}

// Exchanges returns the body-free summaries recorded so far, so the adapter can
// surface them on the trace exactly as it does for the production client.
func (rc *ReplayClient) Exchanges() []HTTPExchange {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	out := make([]HTTPExchange, len(rc.exchanges))
	copy(out, rc.exchanges)
	return out
}

// cannedResponse converts a stored canned response into an HTTPResponse.
func cannedResponse(c HTTPCanned) *HTTPResponse {
	headers := c.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	return &HTTPResponse{Status: c.Status, Headers: headers, Body: []byte(c.Body)}
}

// matchEpisode returns the first not-yet-consumed episode whose selector matches
// the request, consuming it (unless it is repeatable), or nil on a miss. cas may
// be nil (an empty cassette, always a miss).
func matchEpisode(cas *HTTPCassette, episodes []*HTTPEpisode, req httpReq) (*HTTPEpisode, error) {
	repeatable := cas != nil && cas.AllowPlaybackRepeats
	matchOn := []string{"method", "url"}
	if cas != nil {
		matchOn = cas.effectiveMatchOn()
	}
	for _, ep := range episodes {
		if ep.consumed && ep.Replay != "any" && !repeatable {
			continue
		}
		ok, err := episodeMatches(ep, matchOn, req)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		ep.consumed = true
		return ep, nil
	}
	return nil, nil
}

// episodeMatches reports whether the episode's selector matches the request.
// A recorded (`request:`) episode is matched per the cassette's match_on; a
// legacy (`match:`) episode is matched on its declared method/url/url_pattern.
func episodeMatches(ep *HTTPEpisode, matchOn []string, req httpReq) (bool, error) {
	if ep.Request == nil {
		return legacyMatches(ep, req.method, req.url)
	}
	for _, field := range matchOn {
		ok, err := matchField(field, ep.Request, req)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// matchField compares one match_on field between the recorded request and the
// incoming request.
func matchField(field string, rec *HTTPRequestRecord, req httpReq) (bool, error) {
	switch strings.ToLower(field) {
	case "method":
		return rec.Method == "" || strings.EqualFold(rec.Method, req.method), nil
	case "url", "uri":
		return rec.URL == "" || rec.URL == req.url, nil
	case "scheme", "host", "port", "path", "query":
		return matchURLComponent(field, rec.URL, req.url)
	case "body":
		return rec.Body == string(req.body), nil
	case "headers":
		for k, v := range rec.Headers {
			if req.headers[k] != v {
				return false, nil
			}
		}
		return true, nil
	default:
		return false, fmt.Errorf("starlark http replay: unknown match_on field %q", field)
	}
}

// matchURLComponent compares a single parsed URL component (scheme/host/port/
// path/query) between the recorded and incoming URLs.
func matchURLComponent(field, recURL, reqURL string) (bool, error) {
	if recURL == "" {
		return true, nil
	}
	ru, err := url.Parse(recURL)
	if err != nil {
		return false, fmt.Errorf("starlark http replay: bad recorded url %q: %w", recURL, err)
	}
	qu, err := url.Parse(reqURL)
	if err != nil {
		return false, fmt.Errorf("starlark http replay: bad request url %q: %w", reqURL, err)
	}
	switch field {
	case "scheme":
		return strings.EqualFold(ru.Scheme, qu.Scheme), nil
	case "host":
		return strings.EqualFold(ru.Hostname(), qu.Hostname()), nil
	case "port":
		return ru.Port() == qu.Port(), nil
	case "path":
		return ru.Path == qu.Path, nil
	case "query":
		return canonicalQuery(ru.RawQuery) == canonicalQuery(qu.RawQuery), nil
	}
	return false, nil
}

// canonicalQuery renders a query string with parameters sorted so matching is
// order-insensitive (a=1&b=2 matches b=2&a=1).
func canonicalQuery(raw string) string {
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		vs := vals[k]
		sort.Strings(vs)
		for _, v := range vs {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	return b.String()
}

// legacyMatches implements the original hand-authored selector: method
// (case-insensitive), url (exact), url_pattern (Go regexp).
func legacyMatches(ep *HTTPEpisode, method, reqURL string) (bool, error) {
	if ep.Match.Method != "" && !strings.EqualFold(ep.Match.Method, method) {
		return false, nil
	}
	if ep.Match.URL != "" && ep.Match.URL != reqURL {
		return false, nil
	}
	if ep.Match.URLPattern != "" {
		re, err := regexp.Compile(ep.Match.URLPattern)
		if err != nil {
			return false, fmt.Errorf("starlark http replay: bad url_pattern %q: %w", ep.Match.URLPattern, err)
		}
		if !re.MatchString(reqURL) {
			return false, nil
		}
	}
	return true, nil
}

// missError builds the loud "no episode matched" error naming the selectors.
func missError(method, url string, episodes []*HTTPEpisode) error {
	return fmt.Errorf("starlark http replay: no episode matched %s %s; available: %s",
		method, url, strings.Join(episodeKeys(episodes), ", "))
}

// episodeKeys renders each episode's selector for a miss error message.
func episodeKeys(episodes []*HTTPEpisode) []string {
	keys := make([]string, len(episodes))
	for i, ep := range episodes {
		method, sel := "*", ""
		if ep.Request != nil {
			if ep.Request.Method != "" {
				method = ep.Request.Method
			}
			sel = ep.Request.URL
		} else {
			if ep.Match.Method != "" {
				method = ep.Match.Method
			}
			sel = ep.Match.URL
			if sel == "" {
				sel = ep.Match.URLPattern
			}
		}
		keys[i] = fmt.Sprintf("%s %s", method, sel)
	}
	return keys
}
