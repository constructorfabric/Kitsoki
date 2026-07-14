package render

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/flosch/pongo2/v6"

	"kitsoki/internal/expr"
)

// noopLoader is a pongo2.TemplateLoader that resolves nothing. Used when
// an AppRenderer is constructed for an app with no views/ directory: the
// renderer still works for inline Render() calls (the fast path bypasses
// the loader; FromString does not consult it either), and any
// {% extends %} / {% include %} / RenderFile call fails loudly with a
// clear "no such template" error — which is the right outcome.
type noopLoader struct{}

func (noopLoader) Abs(_, name string) string { return name }

func (noopLoader) Get(path string) (io.Reader, error) {
	return nil, fmt.Errorf("no app views/ directory configured (cannot load template %q)", path)
}

// AppRenderer is a pongo2 renderer scoped to a single app's template root.
// It owns a per-app pongo2.TemplateSet so that {% extends %} and
// {% include %} resolve names against <appDir>/views/.
//
// Use NewAppRenderer for dev / interactive mode (templates re-read on every
// render so app-file edits take effect without restart). Use
// NewCachedAppRenderer for tests and production runs where templates are
// stable.
type AppRenderer struct {
	// mu serialises every access to set. pongo2's TemplateSet caches compiled
	// templates in an unsynchronised map (FromString / FromCache both mutate
	// it), so two goroutines rendering through the same renderer race on that
	// map. In kitsoki one session's renderer is reached concurrently by the
	// foreground turn loop AND the background job listener (a job's on_complete
	// arc renders the next view while session.inspect renders the current one),
	// so the race is live, not theoretical — the race detector flags it as a
	// concurrent write inside pongo2.(*TemplateSet).FromString. Holding mu for
	// the whole compile+execute keeps every set access single-threaded; the
	// {% include %}/{% extends %} re-entry into set.FromCache happens on the
	// same goroutine that already holds mu, so it cannot deadlock.
	mu      sync.Mutex
	set     *pongo2.TemplateSet
	rootDir string
	cached  bool

	// prompts is set only when this renderer was built by NewPromptRenderer
	// (its loader is a search-path loader rather than the views
	// LocalFilesystemLoader). It is nil for view renderers. Exposed methods
	// that resolve overlay-first prompt references consult it; on a view
	// renderer they no-op.
	prompts *searchPathLoader
}

// NewAppRenderer builds an uncached renderer rooted at <appDir>/views/.
// The "uncached" behavior is implemented by setting TemplateSet.Debug,
// which forces FromCache to recompile on every call.
//
// If <appDir>/views/ does not exist the renderer is still constructed
// successfully — inline Render calls work, only RenderFile / {% extends %}
// / {% include %} lookups will error when the (absent) loader is asked
// for a name. This tolerance lets the orchestrator build a renderer for
// every app uniformly without paying for a per-app "do views exist?"
// branch at every call site.
func NewAppRenderer(appDir string) (*AppRenderer, error) {
	return newAppRenderer(appDir, false)
}

// NewCachedAppRenderer is the cached variant. Templates loaded via
// RenderFile / {% extends %} / {% include %} are compiled once and reused.
// Use in tests and any non-interactive production path.
func NewCachedAppRenderer(appDir string) (*AppRenderer, error) {
	return newAppRenderer(appDir, true)
}

func newAppRenderer(appDir string, cached bool) (*AppRenderer, error) {
	// An empty appDir means the caller has no on-disk context
	// (LoadBytes() does this; tests use it heavily). Skip the
	// filesystem probe and build a no-op-loader renderer — inline
	// Render() still works; file-loading paths fail loudly.
	if appDir == "" {
		set := pongo2.NewSet("app:anonymous", noopLoader{})
		set.Debug = !cached
		return &AppRenderer{set: set, rootDir: "", cached: cached}, nil
	}
	viewsDir := filepath.Join(appDir, "views")
	info, err := os.Stat(viewsDir)
	switch {
	case err == nil:
		if !info.IsDir() {
			return nil, fmt.Errorf("render: app views path %q is not a directory", viewsDir)
		}
	case os.IsNotExist(err):
		// No views/ directory. Build a renderer with no loader; inline
		// Render calls still succeed (the fast path bypasses the set
		// entirely for non-templated strings, and FromString does not
		// touch the loader). Only {% extends %} / {% include %} / a
		// RenderFile call will hit the loader and fail loudly, which is
		// the right behaviour — those are the only sites that *require*
		// a views/ directory.
		set := pongo2.NewSet("app:"+filepath.Base(appDir), noopLoader{})
		set.Debug = !cached
		return &AppRenderer{set: set, rootDir: viewsDir, cached: cached}, nil
	default:
		return nil, fmt.Errorf("render: app views dir %q: %w", viewsDir, err)
	}

	loader, err := pongo2.NewLocalFileSystemLoader(viewsDir)
	if err != nil {
		return nil, fmt.Errorf("render: loader for %q: %w", viewsDir, err)
	}
	// The set name is purely diagnostic; per-app distinct names make
	// pongo2 error messages locate the failing renderer.
	set := pongo2.NewSet("app:"+filepath.Base(appDir), loader)
	// Debug=true makes FromCache recompile on each call — that's our
	// "uncached" mode for dev hot-reload.
	set.Debug = !cached

	return &AppRenderer{set: set, rootDir: viewsDir, cached: cached}, nil
}

// Render renders an inline template string against env, using this
// renderer's TemplateSet so that {% extends %} / {% include %} references
// resolve through the per-app loader. Pongo's fast path (verbatim return
// for non-templated text) is preserved; the `??` → `|default:` rewrite
// runs at the same seam as in package-level Pongo so story templates
// using `{{ world.x ?? "(none)" }}` parse uniformly under either path.
func (r *AppRenderer) Render(src string, env expr.Env) (out string, err error) {
	if !hasDelims(src) {
		return src, nil
	}
	src = preprocessCoalesce(src)
	r.mu.Lock()
	defer r.mu.Unlock()
	tpl, err := r.set.FromString(src)
	if err != nil {
		return "", wrapTemplateError(src, err)
	}
	// pongo2 filters execute arbitrary Go against author-controlled input and
	// can panic rather than return an error (the stock wordwrap did exactly
	// this). Recover at this seam — as package-level Pongo does — so a filter
	// panic degrades to an ordinary render error instead of unwinding past the
	// caller (the TUI's render-error fallback / the agent dispatch goroutine).
	defer func() {
		if rec := recover(); rec != nil {
			out = ""
			err = wrapTemplateError(src, fmt.Errorf("panic during template execution: %v", rec))
		}
	}()
	out, err = tpl.Execute(ToContext(env))
	if err != nil {
		return "", wrapTemplateError(src, err)
	}
	return out, nil
}

// RenderFile renders the named template (resolved relative to the app's
// views/ directory) against env. Use for standalone .pongo files
// referenced via `template_file:` or as the target of `extends:`.
func (r *AppRenderer) RenderFile(name string, env expr.Env) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tpl, err := r.set.FromCache(name)
	if err != nil {
		return "", fmt.Errorf("render: load template %q: %w", name, err)
	}
	out, err := tpl.Execute(ToContext(env))
	if err != nil {
		return "", fmt.Errorf("render: execute template %q: %w", name, err)
	}
	return out, nil
}

// RootDir returns the absolute path to this renderer's views directory.
// Exposed for diagnostics — tests and error wrappers may want to surface
// it.
func (r *AppRenderer) RootDir() string { return r.rootDir }

// RenderExtended renders an `extends:` / `blocks:` typed view. extends
// names the base template (e.g. "base" → <appDir>/views/base.pongo);
// blocks maps each named block to the pre-rendered text that should
// replace the base template's default block body. env carries the
// runtime expr.Env (world, slots, …) so the base template's own
// expressions (including the un-overridden default blocks) resolve
// correctly.
//
// The implementation builds a one-off wrapping template of the form
//
//	{% extends "<extends>" %}
//	{% block <name> %}{{ _block_<name> }}{% endblock %}
//	…
//
// and binds each `_block_<name>` to the rendered string. Passing block
// content through the pongo2 context (rather than splicing it into the
// template source) is the only safe option — splicing would re-parse
// any incidental `{{` or `{%` inside the block body, which is exactly
// the failure mode the typed-element pipeline is meant to prevent.
//
// extends="" is an authoring error (the View loader rejects it); the
// helper returns a wrapped error if reached anyway. Unknown block names
// (i.e. blocks listed in `blocks:` that the base template doesn't
// declare) are silently dropped, matching pongo2's own behaviour — the
// wrapping template's `{% block %}` entries inherit any base-side
// definition or fall through. Authors get a loud failure only if the
// extends target itself is missing.
func (r *AppRenderer) RenderExtended(extends string, blocks map[string]string, env expr.Env) (string, error) {
	if extends == "" {
		return "", fmt.Errorf("render: RenderExtended: extends is required")
	}
	// Normalise the template name: callers may write "base" or
	// "base.pongo"; pongo2 treats them as literal filenames so the
	// extension matters. If no extension is present, default to .pongo
	// to match the convention used in <appDir>/views/.
	tplName := extends
	if filepath.Ext(tplName) == "" {
		tplName = tplName + ".pongo"
	}

	// Build the wrapping template body. Sort block names for
	// deterministic output (the {% block %} order in the wrapper has
	// no semantic effect, but a stable order makes failures easier to
	// reproduce in tests).
	names := make([]string, 0, len(blocks))
	for name := range blocks {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	fmt.Fprintf(&sb, "{%% extends %q %%}\n", tplName)
	for _, name := range names {
		// Block names are pongo2 identifiers; the loader's element
		// path already constrains them to safe characters via YAML
		// key syntax. Use the bound variable form so the rendered
		// content flows through verbatim.
		fmt.Fprintf(&sb, "{%% block %s %%}{{ _block_%s|safe }}{%% endblock %%}\n", name, name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	tpl, err := r.set.FromString(sb.String())
	if err != nil {
		return "", fmt.Errorf("render: build wrapping template for extends %q: %w", extends, err)
	}

	ctx := ToContext(env)
	for name, body := range blocks {
		ctx["_block_"+name] = body
	}

	out, err := tpl.Execute(ctx)
	if err != nil {
		return "", fmt.Errorf("render: execute extends %q: %w", extends, err)
	}
	return out, nil
}
