package starlark

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"go.starlark.net/starlark"
)

// httpProxy is the ctx.http value. It exposes get and post, each routing through
// the injected HTTPClient (the sandbox's only I/O boundary). It captures the Go
// context so the underlying client sees cancellation/deadlines.
type httpProxy struct {
	ictx   context.Context
	client HTTPClient
}

func newHTTPProxy(ictx context.Context) *httpProxy {
	return &httpProxy{ictx: ictx, client: HTTPFromContext(ictx)}
}

func (h *httpProxy) String() string        { return "ctx.http" }
func (h *httpProxy) Type() string          { return "ctx.http" }
func (h *httpProxy) Freeze()               {}
func (h *httpProxy) Truth() starlark.Bool  { return starlark.True }
func (h *httpProxy) Hash() (uint32, error) { return 0, fmt.Errorf("ctx.http is unhashable") }

func (h *httpProxy) AttrNames() []string { return []string{"get", "post"} }

func (h *httpProxy) Attr(name string) (starlark.Value, error) {
	switch name {
	case "get":
		return starlark.NewBuiltin("ctx.http.get", h.get), nil
	case "post":
		return starlark.NewBuiltin("ctx.http.post", h.post), nil
	}
	return nil, nil
}

// get implements ctx.http.get(url, headers={}, auth=None).
func (h *httpProxy) get(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		url     string
		headers                = &starlark.Dict{}
		auth    starlark.Value = starlark.None
	)
	if err := starlark.UnpackArgs("ctx.http.get", args, kwargs, "url", &url, "headers?", &headers, "auth?", &auth); err != nil {
		return nil, err
	}
	hdrs, err := dictToStringMap(headers)
	if err != nil {
		return nil, err
	}
	authNames, err := authNamesFromValue("ctx.http.get", auth)
	if err != nil {
		return nil, err
	}
	return h.do("GET", url, hdrs, nil, authNames)
}

// post implements ctx.http.post(url, body=..., headers={}, auth=None). body may
// be a dict (JSON-encoded, with Content-Type defaulted to application/json) or
// a string (sent verbatim).
func (h *httpProxy) post(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		url     string
		body    starlark.Value = starlark.None
		headers                = &starlark.Dict{}
		auth    starlark.Value = starlark.None
	)
	if err := starlark.UnpackArgs("ctx.http.post", args, kwargs, "url", &url, "body?", &body, "headers?", &headers, "auth?", &auth); err != nil {
		return nil, err
	}
	hdrs, err := dictToStringMap(headers)
	if err != nil {
		return nil, err
	}
	var bodyBytes []byte
	switch b := body.(type) {
	case starlark.NoneType:
		// no body
	case starlark.String:
		bodyBytes = []byte(string(b))
	case *starlark.Dict:
		goVal, gErr := starlarkToGo(b)
		if gErr != nil {
			return nil, fmt.Errorf("ctx.http.post: body: %w", gErr)
		}
		j, jErr := json.Marshal(goVal)
		if jErr != nil {
			return nil, fmt.Errorf("ctx.http.post: marshal body: %w", jErr)
		}
		bodyBytes = j
		if _, ok := hdrs["Content-Type"]; !ok {
			if hdrs == nil {
				hdrs = map[string]string{}
			}
			hdrs["Content-Type"] = "application/json"
		}
	default:
		return nil, fmt.Errorf("ctx.http.post: body must be a string or dict, got %s", body.Type())
	}
	authNames, err := authNamesFromValue("ctx.http.post", auth)
	if err != nil {
		return nil, err
	}
	return h.do("POST", url, hdrs, bodyBytes, authNames)
}

// do performs the request via the injected client and wraps the result in an
// httpResponse Starlark value. A transport/replay error becomes a Starlark
// error so the script's traceback (and ultimately the effect's on_error: arc)
// reflects it.
func (h *httpProxy) do(method, url string, headers map[string]string, body []byte, auth []string) (starlark.Value, error) {
	var (
		resp *HTTPResponse
		err  error
	)
	if len(auth) > 0 {
		authClient, ok := h.client.(AuthHTTPClient)
		if !ok {
			return nil, fmt.Errorf("ctx.http.%s: auth=%v requested but no auth-aware HTTP client is configured", strings.ToLower(method), auth)
		}
		resp, err = authClient.DoAuth(h.ictx, method, url, headers, body, auth)
	} else {
		resp, err = h.client.Do(h.ictx, method, url, headers, body)
	}
	if err != nil {
		return nil, err
	}
	return &httpResponse{resp: resp}, nil
}

func authNamesFromValue(call string, v starlark.Value) ([]string, error) {
	switch x := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.String:
		name := strings.TrimSpace(string(x))
		if name == "" {
			return nil, nil
		}
		return []string{name}, nil
	case *starlark.List:
		out := make([]string, 0, x.Len())
		for i := 0; i < x.Len(); i++ {
			name, ok := starlark.AsString(x.Index(i))
			if !ok || strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("%s: auth entries must be non-empty strings", call)
			}
			out = append(out, strings.TrimSpace(name))
		}
		return out, nil
	case starlark.Tuple:
		out := make([]string, 0, x.Len())
		for i := 0; i < x.Len(); i++ {
			name, ok := starlark.AsString(x.Index(i))
			if !ok || strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("%s: auth entries must be non-empty strings", call)
			}
			out = append(out, strings.TrimSpace(name))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s: auth must be a string, list of strings, or None, got %s", call, v.Type())
	}
}

// dictToStringMap converts a Starlark headers dict to a Go string map. Both
// keys and values must be strings.
func dictToStringMap(d *starlark.Dict) (map[string]string, error) {
	if d == nil || d.Len() == 0 {
		return nil, nil
	}
	out := make(map[string]string, d.Len())
	for _, item := range d.Items() {
		k, ok := starlark.AsString(item[0])
		if !ok {
			return nil, fmt.Errorf("header key %v is not a string", item[0])
		}
		v, ok := starlark.AsString(item[1])
		if !ok {
			return nil, fmt.Errorf("header %q value is not a string", k)
		}
		out[k] = v
	}
	return out, nil
}

// ─── ctx.http response object ───────────────────────────────────────────────

// httpResponse is the Starlark value returned by ctx.http.get/post. It exposes
// .status, .headers, .text() and .json() — the minimal surface a script needs
// to branch on a response without any way to reach the raw transport.
type httpResponse struct {
	resp *HTTPResponse
}

func (r *httpResponse) String() string { return fmt.Sprintf("<http response %d>", r.resp.Status) }
func (r *httpResponse) Type() string   { return "http.response" }
func (r *httpResponse) Freeze()        {}
func (r *httpResponse) Truth() starlark.Bool {
	return starlark.Bool(r.resp.Status >= 200 && r.resp.Status < 300)
}
func (r *httpResponse) Hash() (uint32, error) { return 0, fmt.Errorf("http.response is unhashable") }

func (r *httpResponse) AttrNames() []string { return []string{"status", "headers", "text", "json"} }

func (r *httpResponse) Attr(name string) (starlark.Value, error) {
	switch name {
	case "status":
		return starlark.MakeInt(r.resp.Status), nil
	case "headers":
		// Insert in sorted key order so a script that iterates resp.headers sees
		// a deterministic order across runs (Go map iteration is randomised).
		d := starlark.NewDict(len(r.resp.Headers))
		keys := make([]string, 0, len(r.resp.Headers))
		for k := range r.resp.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := d.SetKey(starlark.String(k), starlark.String(r.resp.Headers[k])); err != nil {
				return nil, err
			}
		}
		return d, nil
	case "text":
		return starlark.NewBuiltin("http.response.text", r.text), nil
	case "json":
		return starlark.NewBuiltin("http.response.json", r.json), nil
	}
	return nil, nil
}

// text returns the response body as a string.
func (r *httpResponse) text(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	return starlark.String(string(r.resp.Body)), nil
}

// json parses the response body and returns the corresponding Starlark value.
// A parse failure is a Starlark error so the script's traceback names the bad
// payload rather than silently yielding None.
func (r *httpResponse) json(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	var v any
	if err := json.Unmarshal(r.resp.Body, &v); err != nil {
		return nil, fmt.Errorf("http.response.json: %w", err)
	}
	return goToStarlark(v)
}
