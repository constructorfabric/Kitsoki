package ticketprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	goyaml "github.com/goccy/go-yaml"

	starlarkhost "kitsoki/internal/host/starlark"
)

const SidecarKind = "ticket_provider/v1"

var ticketOps = map[string]bool{
	"search":            true,
	"get":               true,
	"comment":           true,
	"comment_edit":      true,
	"comment_reactions": true,
	"transition":        true,
	"list_mine":         true,
	"create":            true,
}

// Spec is the provider sidecar stored beside a .star file.
type Spec struct {
	Kind string              `yaml:"kind" json:"kind"`
	HTTP HTTPSpec            `yaml:"http,omitempty" json:"http,omitempty"`
	Auth map[string]AuthRule `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// HTTPSpec configures the sandbox's ctx.http capability for a provider.
// A ticket_provider/v1 sidecar enables HTTP by default; set enabled:false for
// a pure/local provider.
type HTTPSpec struct {
	Enabled          *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Methods          []string `yaml:"methods,omitempty" json:"methods,omitempty"`
	Hosts            []string `yaml:"hosts,omitempty" json:"hosts,omitempty"`
	CassetteRequired bool     `yaml:"cassette_required,omitempty" json:"cassette_required,omitempty"`
}

// AuthRule maps one symbolic Starlark auth name to one host-side env var and
// one request header mutation. The token value never crosses into Starlark.
type AuthRule struct {
	Env            string `yaml:"env" json:"env"`
	Header         string `yaml:"header,omitempty" json:"header,omitempty"`
	Prefix         string `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	MissingCode    string `yaml:"missing_code,omitempty" json:"missing_code,omitempty"`
	MissingMessage string `yaml:"missing_message,omitempty" json:"missing_message,omitempty"`
}

// EnvLookup resolves a host-side secret source. Implementations should check
// per-call overrides, configured secret stores, then process env as needed.
type EnvLookup func(ctx context.Context, name string) string

// ProviderError is the structured, provider-neutral error envelope returned by
// a Starlark provider or synthesized by the Go auth/runner boundary.
type ProviderError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Hint    string         `json:"hint,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Code != "" && e.Message != "":
		return e.Code + ": " + e.Message
	case e.Code != "":
		return e.Code
	default:
		return e.Message
	}
}

// Result is the normalized provider call result.
type Result struct {
	Data      map[string]any
	Error     *ProviderError
	Exchanges []starlarkhost.HTTPExchange
}

// StarlarkProvider invokes a ticket_provider/v1 Starlark module.
type StarlarkProvider struct {
	Script string
	Source []byte
	Spec   *Spec
	HTTP   starlarkhost.HTTPClient
	Env    EnvLookup
	World  map[string]any
}

// IsProviderScript reports whether scriptPath has a ticket_provider/v1 sidecar.
func IsProviderScript(scriptPath string) bool {
	spec, err := LoadSpec(scriptPath)
	return err == nil && spec.Kind == SidecarKind
}

// LoadSpec loads scriptPath + ".yaml" and verifies the provider kind.
func LoadSpec(scriptPath string) (*Spec, error) {
	raw, err := os.ReadFile(scriptPath + ".yaml")
	if err != nil {
		return nil, err
	}
	var spec Spec
	if err := goyaml.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("ticket provider sidecar: parse %s: %w", filepath.Base(scriptPath)+".yaml", err)
	}
	if strings.TrimSpace(spec.Kind) != SidecarKind {
		return nil, fmt.Errorf("ticket provider sidecar: kind must be %q", SidecarKind)
	}
	if spec.Auth == nil {
		spec.Auth = map[string]AuthRule{}
	}
	return &spec, nil
}

// Invoke calls the Starlark function matching op and normalizes its output.
func (p *StarlarkProvider) Invoke(ctx context.Context, op string, args map[string]any) (Result, error) {
	op = strings.TrimSpace(op)
	if !ticketOps[op] {
		return Result{Error: &ProviderError{
			Code:    "unsupported_ticket_op",
			Message: fmt.Sprintf("ticket provider does not support operation %q", op),
		}}, nil
	}
	if strings.TrimSpace(p.Script) == "" && len(p.Source) == 0 {
		return Result{Error: &ProviderError{Code: "provider_script_required", Message: "ticket provider script is required"}}, nil
	}

	spec := p.Spec
	if spec == nil {
		var err error
		spec, err = LoadSpec(p.Script)
		if err != nil {
			return Result{}, fmt.Errorf("ticket provider: load sidecar: %w", err)
		}
	}

	src := p.Source
	if src == nil {
		raw, err := os.ReadFile(p.Script)
		if err != nil {
			return Result{}, fmt.Errorf("ticket provider: read script %q: %w", p.Script, err)
		}
		src = raw
	}

	inputs := copyMap(args)
	if inputs == nil {
		inputs = map[string]any{}
	}
	inputs["op"] = op

	httpClient := p.HTTP
	if httpClient == nil && starlarkhost.HasHTTPClient(ctx) {
		httpClient = starlarkhost.HTTPFromContext(ctx)
	}
	if httpClient == nil && spec.HTTP.CassetteRequired && providerNeedsHTTP(spec) {
		return Result{Error: &ProviderError{
			Code:    "http_cassette_required",
			Message: "ticket provider requires an injected Starlark HTTP client",
		}}, nil
	}
	if httpClient == nil && providerNeedsHTTP(spec) {
		httpClient = starlarkhost.NewRecordingClient()
	}
	authClient := &authHTTPClient{
		base:  httpClient,
		rules: spec.Auth,
		env:   p.envLookup(),
	}
	runCtx := ctx
	if httpClient != nil {
		runCtx = starlarkhost.WithHTTP(runCtx, authClient)
	}

	runRes, err := starlarkhost.RunFunction(runCtx, starlarkhost.Params{
		Script:       p.Script,
		Source:       src,
		Inputs:       inputs,
		World:        p.World,
		Capabilities: spec.capabilities(),
	}, op)
	if err != nil {
		if authErr := authClient.err(); authErr != nil {
			return Result{Error: authErr}, nil
		}
		if msg, ok := starlarkhost.AsDomainError(err); ok {
			return Result{Error: &ProviderError{Code: "provider_script_error", Message: msg}}, nil
		}
		return Result{}, fmt.Errorf("ticket provider: %w", err)
	}

	out := normalizeOutput(runRes.Outputs)
	out.Exchanges = runRes.Exchanges
	return out, nil
}

func (p *StarlarkProvider) envLookup() EnvLookup {
	if p.Env != nil {
		return p.Env
	}
	return func(_ context.Context, name string) string { return os.Getenv(name) }
}

func providerNeedsHTTP(spec *Spec) bool {
	return spec != nil && spec.capabilities().NeedsHTTP()
}

func (s *Spec) capabilities() starlarkhost.CapabilitySpec {
	cap := starlarkhost.DefaultCapabilities()
	if s == nil {
		cap.HTTP = starlarkhost.HTTPCapability{Enabled: false}
		return cap
	}
	enabled := true
	if s.HTTP.Enabled != nil {
		enabled = *s.HTTP.Enabled
	}
	cap.HTTP = starlarkhost.HTTPCapability{
		Enabled:          enabled,
		Methods:          append([]string(nil), s.HTTP.Methods...),
		Hosts:            append([]string(nil), s.HTTP.Hosts...),
		CassetteRequired: s.HTTP.CassetteRequired,
	}
	return cap
}

type authHTTPClient struct {
	base  starlarkhost.HTTPClient
	rules map[string]AuthRule
	env   EnvLookup

	mu      sync.Mutex
	lastErr *ProviderError
}

func (c *authHTTPClient) Do(ctx context.Context, method, url string, headers map[string]string, body []byte) (*starlarkhost.HTTPResponse, error) {
	if c.base == nil {
		return nil, fmt.Errorf("ticket provider http: no HTTP client configured")
	}
	return c.base.Do(ctx, method, url, headers, body)
}

func (c *authHTTPClient) DoAuth(ctx context.Context, method, url string, headers map[string]string, body []byte, auth []string) (*starlarkhost.HTTPResponse, error) {
	if c.base == nil {
		err := &ProviderError{Code: "http_client_required", Message: "ticket provider HTTP auth requires an HTTP client"}
		c.setErr(err)
		return nil, err
	}
	next := cloneHeaders(headers)
	for _, name := range auth {
		rule, ok := c.rules[strings.TrimSpace(name)]
		if !ok {
			err := &ProviderError{
				Code:    "auth_policy_not_found",
				Message: fmt.Sprintf("ticket provider auth policy %q is not configured", name),
			}
			c.setErr(err)
			return nil, err
		}
		envName := strings.TrimSpace(rule.Env)
		if envName == "" {
			err := &ProviderError{
				Code:    defaultString(rule.MissingCode, "auth_env_required"),
				Message: fmt.Sprintf("ticket provider auth policy %q is missing its env field", name),
			}
			c.setErr(err)
			return nil, err
		}
		secret := ""
		if c.env != nil {
			secret = strings.TrimSpace(c.env(ctx, envName))
		}
		if secret == "" {
			err := &ProviderError{
				Code:    defaultString(rule.MissingCode, "missing_"+strings.ToLower(envName)),
				Message: defaultString(rule.MissingMessage, fmt.Sprintf("required ticket provider credential %s is not configured", envName)),
			}
			c.setErr(err)
			return nil, err
		}
		header := defaultString(rule.Header, "Authorization")
		next[header] = rule.Prefix + secret
	}
	return c.base.Do(ctx, method, url, next, body)
}

func (c *authHTTPClient) Exchanges() []starlarkhost.HTTPExchange {
	switch x := c.base.(type) {
	case *starlarkhost.RecordingClient:
		return append([]starlarkhost.HTTPExchange(nil), x.Exchanges...)
	case *starlarkhost.ReplayClient:
		return x.Exchanges()
	case *starlarkhost.RecordReplayClient:
		return x.Exchanges()
	case interface {
		Exchanges() []starlarkhost.HTTPExchange
	}:
		return x.Exchanges()
	default:
		return nil
	}
}

func (c *authHTTPClient) err() *ProviderError {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastErr == nil {
		return nil
	}
	cp := *c.lastErr
	if c.lastErr.Details != nil {
		cp.Details = copyMap(c.lastErr.Details)
	}
	return &cp
}

func (c *authHTTPClient) setErr(err *ProviderError) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastErr = err
}

func normalizeOutput(out map[string]any) Result {
	if out == nil {
		out = map[string]any{}
	}
	if ok, hasOK := out["ok"].(bool); hasOK && !ok {
		if pe := parseProviderError(out["error"]); pe != nil {
			return Result{Data: outputData(out), Error: pe}
		}
		return Result{Data: outputData(out), Error: &ProviderError{Code: "provider_error", Message: "ticket provider returned ok=false"}}
	}
	if pe := parseProviderError(out["error"]); pe != nil {
		return Result{Data: outputData(out), Error: pe}
	}
	if data := outputData(out); data != nil {
		return Result{Data: data}
	}
	return Result{Data: out}
}

func parseProviderError(raw any) *ProviderError {
	switch v := raw.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return &ProviderError{Code: "provider_error", Message: strings.TrimSpace(v)}
	case map[string]any:
		code := stringField(v, "code")
		message := stringField(v, "message")
		if code == "" && message == "" {
			return nil
		}
		err := &ProviderError{Code: code, Message: message}
		if hint := stringField(v, "hint"); hint != "" {
			err.Hint = hint
		}
		if details, ok := v["details"].(map[string]any); ok {
			err.Details = copyMap(details)
		}
		return err
	default:
		b, _ := json.Marshal(v)
		msg := strings.TrimSpace(string(b))
		if msg == "" || msg == "null" {
			return nil
		}
		return &ProviderError{Code: "provider_error", Message: msg}
	}
}

func outputData(out map[string]any) map[string]any {
	raw, ok := out["data"]
	if !ok {
		return nil
	}
	if data, ok := raw.(map[string]any); ok {
		return copyMap(data)
	}
	return map[string]any{"data": raw}
}

func cloneHeaders(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func stringField(m map[string]any, key string) string {
	raw, ok := m[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}
