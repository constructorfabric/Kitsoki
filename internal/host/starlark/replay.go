package starlark

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// HTTPCassette is a deterministic, body-carrying record of the HTTP exchanges a
// Starlark script makes. It is the replay counterpart of RecordingClient: a
// flow fixture supplies one of these and the testrunner builds a ReplayClient
// from it so the REAL script runs with its network served from disk.
//
// The format is intentionally distinct from the host_cassette used for oracle
// replay: a host_cassette episode replaces a whole handler with a canned
// Result, whereas here we want the handler to run for real and only its HTTP to
// be canned. The YAML an author writes:
//
//	kind: http_cassette
//	exchanges:
//	  - match:
//	      method: GET            # optional; matched case-insensitively
//	      url: "https://api.example.com/v1/widgets/42"   # exact, OR url_pattern below
//	      url_pattern: "/widgets/[0-9]+$"                # optional Go regexp over the URL
//	    response:
//	      status: 200
//	      headers: { Content-Type: application/json }
//	      body: '{"id":42,"name":"sprocket"}'
//
// Matching: an exchange matches when every present match field matches. method
// is compared case-insensitively; url is an exact string compare; url_pattern
// is a Go regexp tested against the request URL (use one OR the other). The
// first not-yet-consumed matching exchange wins; each exchange is consumed once
// unless replay: any is set, mirroring the host_cassette convention.
type HTTPCassette struct {
	Kind      string        `yaml:"kind"`
	Exchanges []HTTPEpisode `yaml:"exchanges"`
}

// HTTPEpisode is one recorded request/response in an HTTPCassette.
type HTTPEpisode struct {
	Match    HTTPMatch  `yaml:"match"`
	Response HTTPCanned `yaml:"response"`
	// Replay set to "any" lets this episode satisfy more than one request
	// (e.g. a polled endpoint). Default (empty) consumes it after one match.
	Replay string `yaml:"replay,omitempty"`

	consumed bool
}

// HTTPMatch is the request-side selector for an episode.
type HTTPMatch struct {
	Method     string `yaml:"method,omitempty"`
	URL        string `yaml:"url,omitempty"`
	URLPattern string `yaml:"url_pattern,omitempty"`
}

// HTTPCanned is the canned response an episode replays.
type HTTPCanned struct {
	Status  int               `yaml:"status"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Body    string            `yaml:"body,omitempty"`
}

// ReplayClient is an HTTPClient that serves requests from an HTTPCassette. It is
// what makes Starlark flow fixtures deterministic and free: no socket is ever
// opened. A request that matches no episode returns a clear miss error naming
// the available episodes, so a drifted script or cassette fails loudly.
type ReplayClient struct {
	mu        sync.Mutex
	episodes  []*HTTPEpisode
	exchanges []HTTPExchange
}

// NewReplayClient builds a ReplayClient over the cassette's episodes. The
// returned client is safe for the single-threaded script run; matching is
// stateful (episodes are consumed) and guarded by a mutex for safety.
func NewReplayClient(cas *HTTPCassette) *ReplayClient {
	rc := &ReplayClient{}
	if cas != nil {
		rc.episodes = make([]*HTTPEpisode, len(cas.Exchanges))
		for i := range cas.Exchanges {
			rc.episodes[i] = &cas.Exchanges[i]
		}
	}
	return rc
}

// Do matches the request against the cassette and returns the canned response.
// It records a body-free summary so the trace surface is identical to the
// production RecordingClient path.
func (rc *ReplayClient) Do(_ context.Context, method, url string, _ map[string]string, _ []byte) (*HTTPResponse, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	for _, ep := range rc.episodes {
		if ep.consumed && ep.Replay != "any" {
			continue
		}
		ok, err := episodeMatches(ep, method, url)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		ep.consumed = true
		rc.exchanges = append(rc.exchanges, HTTPExchange{Method: method, URL: url, Status: ep.Response.Status})
		headers := ep.Response.Headers
		if headers == nil {
			headers = map[string]string{}
		}
		return &HTTPResponse{
			Status:  ep.Response.Status,
			Headers: headers,
			Body:    []byte(ep.Response.Body),
		}, nil
	}

	return nil, fmt.Errorf("starlark http replay: no episode matched %s %s; available: %s",
		method, url, strings.Join(rc.episodeKeys(), ", "))
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

// episodeKeys renders each episode's selector for a miss error message.
func (rc *ReplayClient) episodeKeys() []string {
	keys := make([]string, len(rc.episodes))
	for i, ep := range rc.episodes {
		sel := ep.Match.URL
		if sel == "" {
			sel = ep.Match.URLPattern
		}
		method := ep.Match.Method
		if method == "" {
			method = "*"
		}
		keys[i] = fmt.Sprintf("%s %s", method, sel)
	}
	return keys
}

// episodeMatches reports whether the episode's match selector matches the
// request. method is case-insensitive; url is exact; url_pattern is a Go regexp.
func episodeMatches(ep *HTTPEpisode, method, url string) (bool, error) {
	if ep.Match.Method != "" && !strings.EqualFold(ep.Match.Method, method) {
		return false, nil
	}
	if ep.Match.URL != "" && ep.Match.URL != url {
		return false, nil
	}
	if ep.Match.URLPattern != "" {
		re, err := regexp.Compile(ep.Match.URLPattern)
		if err != nil {
			return false, fmt.Errorf("starlark http replay: bad url_pattern %q: %w", ep.Match.URLPattern, err)
		}
		if !re.MatchString(url) {
			return false, nil
		}
	}
	// An episode with no selector at all matches anything — useful for a
	// catch-all stub, but normally an author sets at least url or url_pattern.
	return true, nil
}
