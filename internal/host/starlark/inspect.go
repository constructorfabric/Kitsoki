package starlark

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Inspector is the sandbox's filesystem + process I/O boundary, the
// sibling of HTTPClient for ctx.fs and ctx.probe. Every ctx.fs.* and ctx.probe
// call from a script funnels through exactly one of its methods. Keeping
// the surface this narrow means the whole inspection capability can be made
// deterministic (for replay) or audited (for production) by swapping a single
// implementation — there is no other way for a script to read a file or run a
// process.
//
// Filesystem methods are explicit and rooted: Read/Exists/Glob inspect, Write
// replaces one repo-relative file, and Probe runs ONLY programs on a global
// allow-list (it is not a shell). A real implementation roots paths at a
// working directory and rejects escape; the deny-all default refuses every call
// so a misconfigured run fails loud rather than silently reaching the disk.
//
// An err is reserved for refusal (no inspector injected), an out-of-bounds path
// (".." escape, oversize read), an unknown probe name, or a transport failure
// (exec start error). A non-zero probe exit is NOT an error — it is surfaced via
// ProbeResult.Exit so the script can branch on it, exactly like a non-2xx HTTP
// status.
type Inspector interface {
	// Read returns the bytes of a repo-relative file. An out-of-bounds path or a
	// file exceeding the size cap is an error.
	Read(ctx context.Context, path string) ([]byte, error)
	// Exists reports whether a repo-relative path exists. An out-of-bounds path is
	// an error; a simply-absent path is (false, nil).
	Exists(ctx context.Context, path string) (bool, error)
	// Glob returns the repo-relative paths matching a glob pattern, sorted. An
	// out-of-bounds pattern is an error.
	Glob(ctx context.Context, pattern string) ([]string, error)
	// Write replaces a repo-relative file with content, creating parent
	// directories as needed. An out-of-bounds path or oversized write is an
	// error. The returned path is normalized repo-relative slash form.
	Write(ctx context.Context, path string, content []byte) (string, error)
	// Probe runs an allow-listed read-only program and returns its exit code and
	// combined output. An unknown name is an error; a non-zero exit is not.
	Probe(ctx context.Context, name string, args []string) (ProbeResult, error)
}

// ProbeResult is the Go-side result of one ctx.probe call. Exit is the program's
// exit code (0 on success, non-zero on a clean failure the script may branch
// on); Out is the combined stdout+stderr. It is the shape behind the Starlark
// probe result dict {exit, out}.
type ProbeResult struct {
	Exit int
	Out  string
}

// InspectExchange is the SUMMARY of one inspection call, suitable for the trace.
// Like HTTPExchange it is deliberately body-free: it carries only {op, target,
// status} so traces stay small and free of file contents, while full bytes stay
// in-process (and, in replay, in cassettes). Run surfaces a slice of these the
// same way it surfaces HTTPExchange (see ExchangesFromContext).
//
// Op is the call kind ("read", "exists", "glob", "write", "probe"); Target is the path,
// pattern, or probe name; Status is a short outcome ("ok", "missing",
// "exit:0", "exit:1", or an error class) so the trace shows what happened
// without leaking the payload.
type InspectExchange struct {
	Op     string `json:"op"`
	Target string `json:"target"`
	Status string `json:"status"`
}

// maxInspectReadBytes caps a single ctx.fs.read. A glue script inspecting a repo
// reads small metadata files (manifests, configs); this turns an accidental read
// of a huge artifact into a clean error rather than ballooning memory.
const maxInspectReadBytes = 1 << 20 // 1 MiB

// maxInspectWriteBytes caps a single ctx.fs.write. Starlark is for small glue
// artifacts, not bulk file generation.
const maxInspectWriteBytes = 1 << 20 // 1 MiB

// inspectorKey is the unexported context key for an injected Inspector.
type inspectorKey struct{}

// WithInspector injects an Inspector into ctx. The host.starlark.run adapter
// calls this in production with a working-dir-rooted inspector; the testrunner
// calls it with a ReplayInspector so a flow fixture exercises the REAL script
// with its fs/probe served from a cassette. This is the single seam that makes
// Starlark inspection testable without an orchestrator change — the exact mirror
// of WithHTTP.
func WithInspector(ctx context.Context, in Inspector) context.Context {
	return context.WithValue(ctx, inspectorKey{}, in)
}

// InspectorFromContext resolves the injected Inspector. When none was injected
// it returns a deniedInspector so a script that touches the filesystem outside a
// configured host fails with a clear error rather than silently reaching the
// disk. Mirrors HTTPFromContext.
func InspectorFromContext(ctx context.Context) Inspector {
	if in, ok := ctx.Value(inspectorKey{}).(Inspector); ok && in != nil {
		return in
	}
	return deniedInspector{}
}

// HasInspector reports whether an inspector was explicitly injected into ctx via
// WithInspector. The host.starlark.run adapter uses it to decide whether to
// install a production inspector: when the testrunner has already injected a
// replay inspector (or a caller deliberately injected any, including one that
// denies all I/O), the adapter must leave it in place. The safe default deny is
// applied by InspectorFromContext when nothing was injected, not by storing a
// deniedInspector here — so any value present means an intentional choice to
// honor. Mirrors HasHTTPClient.
func HasInspector(ctx context.Context) bool {
	return ctx.Value(inspectorKey{}) != nil
}

// deniedInspector is the safe default: every call is refused. It guarantees the
// sandbox never reads a file or runs a process unless an inspector was
// deliberately injected. Mirrors deniedClient.
type deniedInspector struct{}

func (deniedInspector) Read(_ context.Context, path string) ([]byte, error) {
	return nil, fmt.Errorf("starlark: no inspector injected for this run (attempted fs.read %q); inject one with WithInspector", path)
}

func (deniedInspector) Exists(_ context.Context, path string) (bool, error) {
	return false, fmt.Errorf("starlark: no inspector injected for this run (attempted fs.exists %q); inject one with WithInspector", path)
}

func (deniedInspector) Glob(_ context.Context, pattern string) ([]string, error) {
	return nil, fmt.Errorf("starlark: no inspector injected for this run (attempted fs.glob %q); inject one with WithInspector", pattern)
}

func (deniedInspector) Write(_ context.Context, path string, _ []byte) (string, error) {
	return "", fmt.Errorf("starlark: no inspector injected for this run (attempted fs.write %q); inject one with WithInspector", path)
}

func (deniedInspector) Probe(_ context.Context, name string, _ []string) (ProbeResult, error) {
	return ProbeResult{}, fmt.Errorf("starlark: no inspector injected for this run (attempted probe %q); inject one with WithInspector", name)
}

// ─── production inspector ────────────────────────────────────────────────────

// probeSpec is one entry in the global process probe allow-list: a fixed argv
// template whose {0},{1}… placeholders are filled positionally from the
// script-supplied args. The program is exec'd directly (no shell), so there is
// no word-splitting, globbing, or injection surface — an arg can only ever land
// in one argv slot.
type probeSpec struct {
	// argv is the command and its template arguments. argv[0] is the program;
	// "{n}" tokens in later slots are replaced by args[n].
	argv []string
}

// probeAllowList is the GLOBAL read-only probe vocabulary. It is intentionally
// tiny and audited: each entry is a read-only inspection of repo state, no
// program that can mutate the working tree or reach arbitrary hosts. Native
// network probes such as gh.issue.list are handled explicitly in Probe, not by
// this process allow-list. Per-story extension of this list is a documented v1
// follow-up; this global base is the security boundary now.
//
//	git.status   -> git status --porcelain
//	git.ls_files -> git ls-files {0}
var probeAllowList = map[string]probeSpec{
	"git.status":   {argv: []string{"git", "status", "--porcelain"}},
	"git.ls_files": {argv: []string{"git", "ls-files", "{0}"}},
}

// productionInspector is the real Inspector: filesystem reads rooted at a working
// directory (repo-relative, escape-proof) and probes restricted to the global
// allow-list. It is the inspection-side analogue of RecordingClient.
//
// A productionInspector is single-run scoped: the adapter constructs one per
// host.starlark.run invocation rooted at that run's working dir.
type productionInspector struct {
	// root is the absolute working directory all paths are resolved against and
	// confined to.
	root string

	mu          sync.Mutex
	inspections []InspectExchange // body-free summaries for the trace
}

// record appends a body-free summary of one inspection call. It is the
// inspection-side analogue of RecordingClient appending an HTTPExchange.
func (p *productionInspector) record(op, target, status string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inspections = append(p.inspections, InspectExchange{Op: op, Target: target, Status: status})
}

// Inspections returns the body-free summaries recorded so far, so the adapter
// can surface them on the trace exactly as it does for HTTP exchanges.
func (p *productionInspector) Inspections() []InspectExchange {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]InspectExchange, len(p.inspections))
	copy(out, p.inspections)
	return out
}

// NewProductionInspector returns an Inspector rooted at root. root should be the
// run's working directory (typically world.workdir). Relative paths and globs
// from a script are resolved against it and may not escape it.
func NewProductionInspector(root string) Inspector {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return &productionInspector{root: abs}
}

// resolve cleans a repo-relative path and confines it to root, rejecting any
// path that escapes via "..". Absolute paths are rejected except for temp-dir
// paths, which flow fixtures use for durable outputs that should stay out of
// the checkout and review artifact tree. It is the single chokepoint every fs
// method passes through.
func (p *productionInspector) resolve(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		clean := filepath.Clean(rel)
		if isTempPath(clean) {
			return clean, nil
		}
		return "", fmt.Errorf("starlark fs: path %q must be repo-relative or under /tmp", rel)
	}
	full := filepath.Join(p.root, rel)
	// The joined+cleaned path must still be within root; filepath.Join collapses
	// any ".." segments, so a Rel that starts with ".." proves an escape attempt
	// regardless of where the ".." appeared in the input.
	relTo, err := filepath.Rel(p.root, full)
	if err != nil || relTo == ".." || strings.HasPrefix(relTo, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("starlark fs: path %q escapes the working directory", rel)
	}
	return full, nil
}

func isTempPath(path string) bool {
	clean := filepath.Clean(path)
	if clean == "/tmp" || strings.HasPrefix(clean, "/tmp"+string(filepath.Separator)) {
		return true
	}
	tmp := filepath.Clean(os.TempDir())
	return clean == tmp || strings.HasPrefix(clean, tmp+string(filepath.Separator))
}

// Read returns the bytes of a rooted file, capped at maxInspectReadBytes.
func (p *productionInspector) Read(_ context.Context, path string) ([]byte, error) {
	full, err := p.resolve(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, fmt.Errorf("starlark fs.read %q: %w", path, err)
	}
	if info.Size() > maxInspectReadBytes {
		return nil, fmt.Errorf("starlark fs.read %q: file exceeds %d-byte cap", path, maxInspectReadBytes)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("starlark fs.read %q: %w", path, err)
	}
	p.record("read", path, "ok")
	return data, nil
}

// Exists reports whether a rooted path exists.
func (p *productionInspector) Exists(_ context.Context, path string) (bool, error) {
	full, err := p.resolve(path)
	if err != nil {
		return false, err
	}
	if _, statErr := os.Stat(full); statErr != nil {
		if os.IsNotExist(statErr) {
			p.record("exists", path, "missing")
			return false, nil
		}
		return false, fmt.Errorf("starlark fs.exists %q: %w", path, statErr)
	}
	p.record("exists", path, "ok")
	return true, nil
}

// Glob returns the rooted paths matching pattern, made repo-relative and sorted.
func (p *productionInspector) Glob(_ context.Context, pattern string) ([]string, error) {
	full, err := p.resolve(pattern)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(full)
	if err != nil {
		return nil, fmt.Errorf("starlark fs.glob %q: %w", pattern, err)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		rel, rErr := filepath.Rel(p.root, m)
		if rErr != nil {
			rel = m
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	p.record("glob", pattern, fmt.Sprintf("matched:%d", len(out)))
	return out, nil
}

// Write replaces a rooted file, creating parent directories as needed.
func (p *productionInspector) Write(_ context.Context, path string, content []byte) (string, error) {
	if len(content) > maxInspectWriteBytes {
		return "", fmt.Errorf("starlark fs.write %q: content exceeds %d-byte cap", path, maxInspectWriteBytes)
	}
	full, err := p.resolve(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("starlark fs.write %q: mkdir parent: %w", path, err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		return "", fmt.Errorf("starlark fs.write %q: %w", path, err)
	}
	rel, err := filepath.Rel(p.root, full)
	if err != nil {
		rel = path
	} else if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	p.record("write", rel, "ok")
	return rel, nil
}

// Probe runs an allow-listed program with positional args substituted into its
// argv template and exec'd directly (no shell). An unknown name is an error; a
// non-zero exit is returned in ProbeResult.Exit, not as an error.
func (p *productionInspector) Probe(ctx context.Context, name string, args []string) (ProbeResult, error) {
	if name == "gh.issue.list" {
		res, err := probeGitHubIssueList(ctx, args)
		if err != nil {
			return ProbeResult{}, err
		}
		p.record("probe", name, fmt.Sprintf("exit:%d", res.Exit))
		return res, nil
	}
	spec, ok := probeAllowList[name]
	if !ok {
		return ProbeResult{}, fmt.Errorf("starlark probe %q: not on the allow-list", name)
	}
	argv, err := substituteArgv(spec.argv, args)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("starlark probe %q: %w", name, err)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = p.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// A clean non-zero exit is a result the script branches on, not an
			// error — mirror a non-2xx HTTP status.
			p.record("probe", name, fmt.Sprintf("exit:%d", exitErr.ExitCode()))
			return ProbeResult{Exit: exitErr.ExitCode(), Out: string(out)}, nil
		}
		return ProbeResult{}, fmt.Errorf("starlark probe %q: %w", name, err)
	}
	p.record("probe", name, "exit:0")
	return ProbeResult{Exit: 0, Out: string(out)}, nil
}

// substituteArgv fills the {n} placeholders of an argv template from args. A
// placeholder with no corresponding arg is an error so a misuse fails loud
// rather than running a half-formed command.
func substituteArgv(template, args []string) ([]string, error) {
	out := make([]string, len(template))
	for i, tok := range template {
		if strings.HasPrefix(tok, "{") && strings.HasSuffix(tok, "}") {
			var idx int
			if _, err := fmt.Sscanf(tok, "{%d}", &idx); err != nil {
				return nil, fmt.Errorf("malformed argv placeholder %q", tok)
			}
			if idx < 0 || idx >= len(args) {
				return nil, fmt.Errorf("argv placeholder %s has no argument (got %d args)", tok, len(args))
			}
			out[i] = args[idx]
			continue
		}
		out[i] = tok
	}
	return out, nil
}
