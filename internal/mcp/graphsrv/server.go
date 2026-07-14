package graphsrv

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/clock"
	objectgraph "kitsoki/internal/graph"
	"kitsoki/internal/host"
)

// Config configures the standalone "kitsoki mcp-graph" server.
type Config struct {
	// CatalogFlags are the raw --catalog values, each "[alias=]path", in
	// flag order. May be empty (triggers the pog/catalog.yaml cwd probe).
	CatalogFlags []string
	// ScopeFlags are the raw --scope values, each "[alias=]path" naming a
	// scope-spec YAML file bound to a catalog alias (default: the default
	// catalog). A bound scope deterministically restricts every graph.*
	// call on that alias to the spec's member subset — baked in here, at
	// construction, never adjustable by a tool argument. See scope.go.
	ScopeFlags []string
	// Mode gates write-tool registration: read|propose|steward. P2 has no
	// write tools, so this only needs to be wired correctly for P4.
	Mode string
	// Actor is stored for later use (P4 write-op stamps). No P2 read-tool
	// behavior depends on it.
	Actor string
	// FeedbackSink records the operator's sink preference. P2 only ever
	// writes locally regardless of this value (catalog/github are P6).
	FeedbackSink string
	// JournalPath is the receipts JSONL location for write-tool calls
	// (P4). Accepted now for forward compat; nothing writes to it yet.
	JournalPath string
	// ClockFixed is the --clock-fixed flag value (RFC3339), or empty.
	ClockFixed string

	// WriteVia is the --write-via flag: auto|direct|capsule. Empty means
	// auto — each bound catalog's route comes from its own repo's
	// .kitsoki/project-profile.yaml `graph: {write_via: ...}` block,
	// defaulting to direct when absent. See writevia.go.
	WriteVia string
	// WorkspaceRunner is the injectable dev-workspace.sh process seam used
	// by capsule write routing. Nil uses the real script executor; tests
	// inject a deterministic fake.
	WorkspaceRunner WorkspaceRunner

	// IssueFiler is the injectable GitHub issue-filing seam used when
	// --feedback-sink github (plan §3.6 "Submit stage", P6). Nil disables
	// the github sink: feedback.report degrades to local-only with a
	// routing_errors entry rather than failing.
	IssueFiler IssueFiler

	// Registry, if set, is used instead of constructing a fresh
	// host.Registry via host.NewRegistry()+host.RegisterBuiltins(). Tests
	// use this to inject a registry pointed at fixture catalogs without
	// touching global process state.
	Registry *host.Registry
	// Claims is the process-wide claim registry used by graph.claim and
	// graph.release. Nil creates the conservative default registry.
	Claims *ClaimRegistry
	// Liveness decides whether a claim holder is still live. Nil treats every
	// holder as live, which fails closed against an accidental claim steal.
	Liveness Liveness
}

// Deps bundles the resolved, request-independent state every graphsrv tool
// handler closes over. RegisterGraphTools/RegisterFeedbackTools take a
// *Deps rather than a *Config so P6's studio mount can build one directly
// (no cobra flags involved) and reuse the exact same registration
// functions.
type Deps struct {
	Registry *host.Registry
	Catalogs *CatalogSet
	// Scopes maps a bound catalog alias to its baked scope spec (scope.go).
	// Nil/absent alias = that catalog is unscoped.
	Scopes       map[string]*objectgraph.ScopeSpec
	Mode         string
	Actor        string
	FeedbackSink string
	JournalPath  string
	Clock        clock.Clock
	// Recorder is the last-10-call ring buffer every registered tool
	// (via the `recorded` wrapper) appends to. feedback.report attaches
	// a redacted snapshot of it as evidence.
	Recorder *Recorder
	// IssueFiler is the GitHub issue-filing seam for --feedback-sink
	// github (P6). Nil means the github sink is unconfigured.
	IssueFiler IssueFiler
	// Router decides, per bound catalog, whether writes land directly in
	// the working tree or route through the managed capsule workflow
	// (writevia.go). Nil behaves as direct-everywhere.
	Router *WriteRouter
	Claims *ClaimRegistry
}

// Server is the standalone stdio MCP server exposing mcp-graph's read
// family and feedback channel.
type Server struct {
	mcpSrv *mcpsdk.Server
	deps   *Deps
}

// NewServer constructs the mcp-graph server: resolves catalog bindings,
// builds (or reuses) a host.Registry with the builtin host.graph.* ops
// registered, and registers every tool via RegisterGraphTools /
// RegisterFeedbackTools.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Mode == "" {
		cfg.Mode = DefaultMode
	}
	if err := ValidateMode(cfg.Mode); err != nil {
		return nil, err
	}
	if cfg.FeedbackSink == "" {
		cfg.FeedbackSink = FeedbackSinkLocal
	}
	if err := ValidateFeedbackSink(cfg.FeedbackSink); err != nil {
		return nil, err
	}
	if cfg.WriteVia == "" {
		cfg.WriteVia = DefaultWriteVia
	}
	if err := ValidateWriteVia(cfg.WriteVia); err != nil {
		return nil, err
	}

	clk, err := ResolveClock(cfg.ClockFixed)
	if err != nil {
		return nil, err
	}

	catalogs, err := ParseCatalogFlags(cfg.CatalogFlags)
	if err != nil {
		return nil, err
	}

	scopes, err := ParseScopeFlags(cfg.ScopeFlags, catalogs)
	if err != nil {
		return nil, err
	}

	registry := cfg.Registry
	if registry == nil {
		registry = host.NewRegistry()
		host.RegisterBuiltins(registry)
	}

	deps := &Deps{
		Registry:     registry,
		Catalogs:     catalogs,
		Scopes:       scopes,
		Mode:         cfg.Mode,
		Actor:        cfg.Actor,
		FeedbackSink: cfg.FeedbackSink,
		JournalPath:  cfg.JournalPath,
		Clock:        clk,
		Recorder:     NewRecorder(),
		IssueFiler:   cfg.IssueFiler,
		Router:       NewWriteRouter(cfg.WriteVia, cfg.WorkspaceRunner),
		Claims:       cfg.Claims,
	}
	if deps.Claims == nil {
		deps.Claims = NewClaimRegistry(cfg.Liveness, clk)
	}

	s := &Server{deps: deps}
	s.mcpSrv = mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "kitsoki-graph-mcp",
		Version: "0.1.0",
	}, nil)

	RegisterGraphTools(s.mcpSrv, deps, cfg.Mode)
	RegisterFeedbackTools(s.mcpSrv, deps)

	return s, nil
}

// Run starts the server on stdio and blocks until the peer disconnects or
// ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.mcpSrv.Run(ctx, &mcpsdk.StdioTransport{})
}

// Connect exposes the underlying SDK server for in-process tests
// (mcpsdk.NewInMemoryTransports pattern, per internal/mcp/validator_test.go).
func (s *Server) Connect(ctx context.Context, t mcpsdk.Transport, opts *mcpsdk.ServerSessionOptions) (*mcpsdk.ServerSession, error) {
	return s.mcpSrv.Connect(ctx, t, opts)
}

// toolJSON renders v (an *ErrorPayload or a tool-specific *OK struct) as
// both the CallToolResult's StructuredContent and its serialized text
// fallback, matching internal/mcp/codeact.go's codeactToolJSON pattern. isError
// is normally driven by the payload's own `ok` field by handler callers
// (see toolResult below); it's a separate parameter here only so infra
// (marshal) failures can force isError=true.
func toolJSON(v any, isError bool) *mcpsdk.CallToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(fmt.Sprintf(`{"ok":false,"code":"VALIDATION","error":"marshal tool result: %s","if_stuck":%q}`, err, defaultIfStuck))
		isError = true
	}
	return &mcpsdk.CallToolResult{
		IsError:           isError,
		StructuredContent: v,
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(b)},
		},
	}
}

// errorResult renders an *ErrorPayload as an isError:true CallToolResult.
func errorResult(e *ErrorPayload) *mcpsdk.CallToolResult {
	return toolJSON(e, true)
}

// okResult renders a successful tool payload (isError:false).
func okResult(v any) *mcpsdk.CallToolResult {
	return toolJSON(v, false)
}
