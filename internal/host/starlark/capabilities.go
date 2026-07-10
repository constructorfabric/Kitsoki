package starlark

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
)

// BuiltinHostVerbVocabulary is the built-in host verb vocabulary a
// host.starlark.run script may be granted through ctx.host.call. Per-effect
// metadata still grants a subset; this list is only the maximum vocabulary the
// sandbox recognizes as intentionally callable from Starlark.
var BuiltinHostVerbVocabulary = []string{
	"host.workspace_manager.get",
	"host.graph.load",
	"host.graph.lint",
	"host.graph.diff",
	"host.graph.project",
	"host.graph.query",
}

// CapabilitySpec is the normalized runtime authority for a Starlark run. Pure
// helpers plus ctx.inputs and ctx.world are available by default; every
// external surface is opt-in.
type CapabilitySpec struct {
	Stdlib map[string]bool
	World  bool

	HTTP  HTTPCapability
	FS    FSCapability
	Probe ProbeCapability
	Host  HostCapability
}

type HTTPCapability struct {
	Enabled          bool
	Methods          []string
	Hosts            []string
	CassetteRequired bool
}

type FSCapability struct {
	ReadPatterns  []string
	WritePatterns []string
	MaxBytes      int
}

type ProbeCapability struct {
	Names []string
}

type HostCapability struct {
	Verbs []string
}

// DefaultCapabilities returns the deterministic default sandbox: pure helpers,
// ctx.inputs, and read-only ctx.world, with no I/O or host integration.
func DefaultCapabilities() CapabilitySpec {
	return CapabilitySpec{
		Stdlib: map[string]bool{
			"json": true,
			"math": true,
			"yaml": true,
		},
		World: true,
	}
}

// ParseCapabilities normalizes the YAML/JSON-ish value used in
// with.capabilities. The top-level shape is a structured mapping so the
// capability boundary stays explicit and statically reviewable.
func ParseCapabilities(raw any) (CapabilitySpec, error) {
	spec := DefaultCapabilities()
	if raw == nil {
		return spec, nil
	}
	switch v := raw.(type) {
	case map[string]any:
		if err := applyCapabilityMap(&spec, v); err != nil {
			return CapabilitySpec{}, err
		}
		return spec, nil
	default:
		return CapabilitySpec{}, fmt.Errorf("capabilities must be a mapping, got %T", raw)
	}
}

func applyCapabilityMap(spec *CapabilitySpec, m map[string]any) error {
	for key, raw := range m {
		if strings.Contains(key, "{{") {
			continue
		}
		switch key {
		case "stdlib":
			std, err := parseStringSet(raw, "stdlib")
			if err != nil {
				return err
			}
			for name := range std {
				switch name {
				case "json", "math", "yaml":
				default:
					return fmt.Errorf("unknown Starlark stdlib module %q", name)
				}
			}
			spec.Stdlib = std
		case "world":
			world, err := parseWorld(raw)
			if err != nil {
				return err
			}
			spec.World = world
		case "http":
			httpCap, err := parseHTTPCapability(raw)
			if err != nil {
				return err
			}
			spec.HTTP = httpCap
		case "fs":
			fsCap, err := parseFSCapability(raw)
			if err != nil {
				return err
			}
			spec.FS = fsCap
		case "probe":
			names, err := parseStringSlice(raw, "probe")
			if err != nil {
				return err
			}
			addProbeNames(&spec.Probe, names...)
		case "vcs":
			names, err := parseVCS(raw)
			if err != nil {
				return err
			}
			addProbeNames(&spec.Probe, names...)
		case "github":
			names, err := parseGitHub(raw)
			if err != nil {
				return err
			}
			addProbeNames(&spec.Probe, names...)
		case "host":
			hostCap, err := parseHostCapability(raw)
			if err != nil {
				return err
			}
			spec.Host = hostCap
		default:
			return fmt.Errorf("unknown Starlark capability key %q", key)
		}
	}
	sortSpec(spec)
	return nil
}

func parseWorld(raw any) (bool, error) {
	switch v := raw.(type) {
	case nil:
		return true, nil
	case bool:
		return v, nil
	case string:
		switch strings.TrimSpace(v) {
		case "", "read", "true":
			return true, nil
		case "none", "false":
			return false, nil
		default:
			return false, fmt.Errorf("world capability must be read|none|true|false, got %q", v)
		}
	default:
		return false, fmt.Errorf("world capability must be read|none|true|false, got %T", raw)
	}
}

func parseHTTPCapability(raw any) (HTTPCapability, error) {
	cap := HTTPCapability{Enabled: true}
	switch v := raw.(type) {
	case nil:
		return cap, nil
	case bool:
		cap.Enabled = v
		return cap, nil
	case []any, []string:
		methods, err := parseStringSlice(raw, "http")
		if err != nil {
			return HTTPCapability{}, err
		}
		cap.Methods = normalizeMethods(methods)
		return cap, nil
	case map[string]any:
		for key, value := range v {
			switch key {
			case "methods":
				methods, err := parseStringSlice(value, "http.methods")
				if err != nil {
					return HTTPCapability{}, err
				}
				cap.Methods = normalizeMethods(methods)
			case "hosts":
				hosts, err := parseStringSlice(value, "http.hosts")
				if err != nil {
					return HTTPCapability{}, err
				}
				cap.Hosts = normalizeStrings(hosts)
			case "cassette_required":
				b, ok := value.(bool)
				if !ok {
					return HTTPCapability{}, fmt.Errorf("http.cassette_required must be a boolean")
				}
				cap.CassetteRequired = b
			default:
				return HTTPCapability{}, fmt.Errorf("unknown http capability key %q", key)
			}
		}
		return cap, nil
	default:
		return HTTPCapability{}, fmt.Errorf("http capability must be bool, list, or mapping, got %T", raw)
	}
}

func parseFSCapability(raw any) (FSCapability, error) {
	var cap FSCapability
	switch v := raw.(type) {
	case nil:
		return cap, nil
	case bool:
		if v {
			cap.ReadPatterns = []string{"**"}
			cap.WritePatterns = []string{"**"}
		}
		return cap, nil
	case map[string]any:
		for key, value := range v {
			switch key {
			case "read":
				patterns, err := parseStringSlice(value, "fs.read")
				if err != nil {
					return FSCapability{}, err
				}
				cap.ReadPatterns = normalizePatterns(patterns)
			case "write":
				patterns, err := parseStringSlice(value, "fs.write")
				if err != nil {
					return FSCapability{}, err
				}
				cap.WritePatterns = normalizePatterns(patterns)
			case "max_bytes":
				n, err := parseInt(value, "fs.max_bytes")
				if err != nil {
					return FSCapability{}, err
				}
				cap.MaxBytes = n
			default:
				return FSCapability{}, fmt.Errorf("unknown fs capability key %q", key)
			}
		}
		return cap, nil
	default:
		return FSCapability{}, fmt.Errorf("fs capability must be bool or mapping, got %T", raw)
	}
}

func parseVCS(raw any) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case bool:
		if v {
			return []string{"git.status", "git.ls_files"}, nil
		}
		return nil, nil
	case string:
		if strings.TrimSpace(v) == "read" {
			return []string{"git.status", "git.ls_files"}, nil
		}
		return nil, fmt.Errorf("vcs capability must be read|true|false or a list, got %q", v)
	case []any, []string:
		names, err := parseStringSlice(raw, "vcs")
		if err != nil {
			return nil, err
		}
		var out []string
		for _, name := range names {
			switch name {
			case "status", "git.status":
				out = append(out, "git.status")
			case "ls_files", "git.ls_files":
				out = append(out, "git.ls_files")
			default:
				return nil, fmt.Errorf("unknown vcs capability %q", name)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("vcs capability must be read|true|false or a list, got %T", raw)
	}
}

func parseGitHub(raw any) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case bool:
		if !v {
			return nil, nil
		}
		return nil, fmt.Errorf("github capability must name a GitHub API surface")
	case map[string]any:
		var out []string
		for key, value := range v {
			switch key {
			case "issues":
				world, err := parseWorld(value)
				if err != nil {
					return nil, fmt.Errorf("github.issues: %w", err)
				}
				if world {
					out = append(out, "gh.issue.list")
				}
			default:
				return nil, fmt.Errorf("unknown github capability key %q", key)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("github capability must be a mapping, got %T", raw)
	}
}

func parseHostCapability(raw any) (HostCapability, error) {
	var cap HostCapability
	switch v := raw.(type) {
	case nil:
		return cap, nil
	case map[string]any:
		for key, value := range v {
			switch key {
			case "verbs":
				verbs, err := parseStringSlice(value, "host.verbs")
				if err != nil {
					return HostCapability{}, err
				}
				for _, verb := range verbs {
					if !stringIn(verb, BuiltinHostVerbVocabulary) {
						return HostCapability{}, fmt.Errorf("unknown ctx.host verb %q", verb)
					}
				}
				cap.Verbs = normalizeStrings(verbs)
			default:
				return HostCapability{}, fmt.Errorf("unknown host capability key %q", key)
			}
		}
		return cap, nil
	default:
		return HostCapability{}, fmt.Errorf("host capability must be a mapping, got %T", raw)
	}
}

func parseStringSet(raw any, name string) (map[string]bool, error) {
	items, err := parseStringSlice(raw, name)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out, nil
}

func parseStringSlice(raw any, name string) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		return []string{strings.TrimSpace(v)}, nil
	case []string:
		return normalizeStrings(v), nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("%s entries must be non-empty strings", name)
			}
			if strings.Contains(s, "{{") {
				continue
			}
			out = append(out, strings.TrimSpace(s))
		}
		return normalizeStrings(out), nil
	default:
		return nil, fmt.Errorf("%s must be a string or list of strings, got %T", name, raw)
	}
}

func parseInt(raw any, name string) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case uint:
		if uint64(v) > uint64(int(^uint(0)>>1)) {
			return 0, fmt.Errorf("%s is too large", name)
		}
		return int(v), nil
	case uint64:
		if v > uint64(int(^uint(0)>>1)) {
			return 0, fmt.Errorf("%s is too large", name)
		}
		return int(v), nil
	case float64:
		if v == float64(int(v)) {
			return int(v), nil
		}
		return 0, fmt.Errorf("%s must be an integer", name)
	default:
		return 0, fmt.Errorf("%s must be an integer, got %T", name, raw)
	}
}

func addProbeNames(cap *ProbeCapability, names ...string) {
	cap.Names = append(cap.Names, names...)
	cap.Names = normalizeStrings(cap.Names)
}

func normalizeMethods(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		out = append(out, strings.ToUpper(strings.TrimSpace(item)))
	}
	return normalizeStrings(out)
}

func normalizePatterns(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		out = append(out, cleanSlashPath(item))
	}
	return normalizeStrings(out)
}

func normalizeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func sortSpec(spec *CapabilitySpec) {
	spec.HTTP.Methods = normalizeMethods(spec.HTTP.Methods)
	spec.HTTP.Hosts = normalizeStrings(spec.HTTP.Hosts)
	spec.FS.ReadPatterns = normalizePatterns(spec.FS.ReadPatterns)
	spec.FS.WritePatterns = normalizePatterns(spec.FS.WritePatterns)
	spec.Probe.Names = normalizeStrings(spec.Probe.Names)
	spec.Host.Verbs = normalizeStrings(spec.Host.Verbs)
}

// NeedsHTTP reports whether ctx.http should be exposed.
func (c CapabilitySpec) NeedsHTTP() bool { return c.HTTP.Enabled }

// RequiresInjectedHTTP reports whether the adapter must receive an HTTP client
// from the caller rather than installing the production recording transport.
func (c CapabilitySpec) RequiresInjectedHTTP() bool {
	return c.NeedsHTTP() && c.HTTP.CassetteRequired
}

// NeedsInspector reports whether any ctx.fs or ctx.probe surface should be
// exposed.
func (c CapabilitySpec) NeedsInspector() bool {
	return len(c.FS.ReadPatterns) > 0 || len(c.FS.WritePatterns) > 0 || len(c.Probe.Names) > 0
}

func (c CapabilitySpec) AllowsFS() bool {
	return len(c.FS.ReadPatterns) > 0 || len(c.FS.WritePatterns) > 0
}

func (c CapabilitySpec) AllowsProbe() bool { return len(c.Probe.Names) > 0 }

func (c CapabilitySpec) AllowsHost() bool { return len(c.Host.Verbs) > 0 }

// CapabilityLabels returns a stable, human-readable list suitable for codeact
// prompts.
func (c CapabilitySpec) CapabilityLabels() []string {
	var out []string
	if c.World {
		out = append(out, "world")
	}
	if c.HTTP.Enabled {
		out = append(out, "http")
	}
	if len(c.FS.ReadPatterns) > 0 {
		out = append(out, "fs.read")
	}
	if len(c.FS.WritePatterns) > 0 {
		out = append(out, "fs.write")
	}
	if len(c.Probe.Names) > 0 {
		if containsAny(c.Probe.Names, "git.status", "git.ls_files") {
			out = append(out, "vcs")
		}
		if containsAny(c.Probe.Names, "gh.issue.list") {
			out = append(out, "github.issues")
		}
	}
	if len(c.Host.Verbs) > 0 {
		out = append(out, "host")
	}
	return normalizeStrings(out)
}

func containsAny(haystack []string, needles ...string) bool {
	for _, n := range needles {
		if stringIn(n, haystack) {
			return true
		}
	}
	return false
}

func stringIn(s string, list []string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// ApplyCapabilityPolicy wraps any injected clients/callers with this run's
// normalized policy. It does not install production implementations; adapters
// decide which concrete HTTP/Inspector/HostCaller to inject.
func ApplyCapabilityPolicy(ctx context.Context, cap CapabilitySpec) context.Context {
	if cap.NeedsHTTP() {
		ctx = WithHTTP(ctx, capabilityHTTPClient{base: HTTPFromContext(ctx), cap: cap.HTTP})
	}
	if cap.NeedsInspector() {
		ctx = WithInspector(ctx, capabilityInspector{base: InspectorFromContext(ctx), cap: cap})
	}
	if cap.AllowsHost() {
		ctx = RestrictHost(ctx, cap.Host.Verbs)
	}
	return ctx
}

type capabilityHTTPClient struct {
	base HTTPClient
	cap  HTTPCapability
}

func (c capabilityHTTPClient) Do(ctx context.Context, method, rawURL string, headers map[string]string, body []byte) (*HTTPResponse, error) {
	if err := c.check(method, rawURL); err != nil {
		return nil, err
	}
	return c.base.Do(ctx, strings.ToUpper(method), rawURL, headers, body)
}

func (c capabilityHTTPClient) DoAuth(ctx context.Context, method, rawURL string, headers map[string]string, body []byte, auth []string) (*HTTPResponse, error) {
	if err := c.check(method, rawURL); err != nil {
		return nil, err
	}
	authClient, ok := c.base.(AuthHTTPClient)
	if !ok {
		return nil, fmt.Errorf("ctx.http.%s: auth=%v requested but no auth-aware HTTP client is configured", strings.ToLower(method), auth)
	}
	return authClient.DoAuth(ctx, strings.ToUpper(method), rawURL, headers, body, auth)
}

func (c capabilityHTTPClient) check(method, rawURL string) error {
	method = strings.ToUpper(method)
	if len(c.cap.Methods) > 0 && !stringIn(method, c.cap.Methods) {
		return fmt.Errorf("ctx.http.%s: method %s is not granted (allowed: %s)", strings.ToLower(method), method, strings.Join(c.cap.Methods, ", "))
	}
	if len(c.cap.Hosts) > 0 {
		u, err := url.Parse(rawURL)
		if err != nil {
			return fmt.Errorf("ctx.http.%s: parse URL %q: %w", strings.ToLower(method), rawURL, err)
		}
		host := strings.ToLower(u.Hostname())
		if !hostAllowed(host, c.cap.Hosts) {
			return fmt.Errorf("ctx.http.%s: host %q is not granted (allowed: %s)", strings.ToLower(method), host, strings.Join(c.cap.Hosts, ", "))
		}
	}
	return nil
}

func hostAllowed(host string, allowed []string) bool {
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "*" || candidate == host {
			return true
		}
		if strings.HasPrefix(candidate, "*.") && strings.HasSuffix(host, strings.TrimPrefix(candidate, "*")) {
			return true
		}
	}
	return false
}

type capabilityInspector struct {
	base Inspector
	cap  CapabilitySpec
}

func (c capabilityInspector) Read(ctx context.Context, p string) ([]byte, error) {
	if !pathAllowed(p, c.cap.FS.ReadPatterns) {
		return nil, fmt.Errorf("ctx.fs.read: path %q is not granted", p)
	}
	data, err := c.base.Read(ctx, p)
	if err != nil {
		return nil, err
	}
	if max := c.cap.FS.MaxBytes; max > 0 && len(data) > max {
		return nil, fmt.Errorf("ctx.fs.read %q: file exceeds %d-byte capability cap", p, max)
	}
	return data, nil
}

func (c capabilityInspector) Exists(ctx context.Context, p string) (bool, error) {
	if !pathAllowed(p, c.cap.FS.ReadPatterns) {
		return false, fmt.Errorf("ctx.fs.exists: path %q is not granted", p)
	}
	return c.base.Exists(ctx, p)
}

func (c capabilityInspector) Glob(ctx context.Context, pattern string) ([]string, error) {
	if !pathAllowed(pattern, c.cap.FS.ReadPatterns) {
		return nil, fmt.Errorf("ctx.fs.glob: pattern %q is not granted", pattern)
	}
	return c.base.Glob(ctx, pattern)
}

func (c capabilityInspector) Write(ctx context.Context, p string, content []byte) (string, error) {
	if !pathAllowed(p, c.cap.FS.WritePatterns) {
		return "", fmt.Errorf("ctx.fs.write: path %q is not granted", p)
	}
	if max := c.cap.FS.MaxBytes; max > 0 && len(content) > max {
		return "", fmt.Errorf("ctx.fs.write %q: content exceeds %d-byte capability cap", p, max)
	}
	return c.base.Write(ctx, p, content)
}

func (c capabilityInspector) Probe(ctx context.Context, name string, args []string) (ProbeResult, error) {
	if !stringIn(name, c.cap.Probe.Names) {
		return ProbeResult{}, fmt.Errorf("ctx.probe: probe %q is not granted", name)
	}
	return c.base.Probe(ctx, name, args)
}

func pathAllowed(target string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	target = cleanSlashPath(target)
	for _, pattern := range patterns {
		if pattern == "**" || pattern == "*" {
			return true
		}
		if pattern == target {
			return true
		}
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			if target == prefix || strings.HasPrefix(target, prefix+"/") {
				return true
			}
		}
		if ok, err := path.Match(pattern, target); err == nil && ok {
			return true
		}
	}
	return false
}

func cleanSlashPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	return path.Clean(strings.ReplaceAll(p, "\\", "/"))
}
