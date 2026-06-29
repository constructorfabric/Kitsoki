package expr

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	exprpkg "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm"
)

// Env is the evaluation scope available to every expression — the set of
// roots a guard, effect value, or view template may read. Its fields mirror
// the scope documented in the authoring guide
// (docs/stories/authoring.md §5.7) and the state-machine reference
// (docs/stories/state-machine.md):
//   - Slots   map[string]any  — the current intent's slot values
//   - World   map[string]any  — the current world snapshot
//   - Event   map[string]any  — the triggering event (if any)
//   - Run     RunCtx          — run-level metadata (id, turn, timestamps)
//   - Args    map[string]any  — handler-local arguments (host invocations, prompt files)
//   - Menu    map[string]any  — computed menu (primary + blocked intents)
//     for the current state, populated by view-render call sites so authors
//     can render the "what can I do right now" surface inline in view: prose.
//     Shape: {"primary": [{intent, display, reason, destination_hint, primary}],
//     "blocked": [...]}.
//
// View-render call sites should call PopulateMenuHelpers after assembling
// Slots/World/Menu so the helper functions (available, blocked,
// blocked_reason, intent_status) are bound to a closure over Menu. The
// helpers are exposed as function-typed fields on Env so expr-lang resolves
// bare names like `available(...)` to method-style calls on the env value.
type Env struct {
	Slots  map[string]any `expr:"slots"`
	World  map[string]any `expr:"world"`
	Event  map[string]any `expr:"event"`
	Run    RunCtx         `expr:"run"`
	Args   map[string]any `expr:"args"`
	Menu   map[string]any `expr:"menu"`
	Result map[string]any `expr:"result"`

	// Helper functions for view templates. Bound at view-render time by
	// PopulateMenuHelpers. When Menu is unset (effect evaluation, guard
	// evaluation), these remain nil — they're not callable from non-view
	// contexts, which is fine because authors only reference them from
	// view: prose.
	Available     func(name string) bool   `expr:"available"`
	Blocked       func(name string) bool   `expr:"blocked"`
	BlockedReason func(name string) string `expr:"blocked_reason"`
	IntentStatus  func(name string) string `expr:"intent_status"`

	// Item is the current element inside a {{ range expr }} block. The
	// template engine sets this on a per-iteration copy of the env; field
	// access inside the body uses `.display`, which the engine rewrites
	// to `item.display`. Unused outside range bodies (nil → expr-lang
	// resolves accesses against nil per AllowUndefinedVariables).
	Item any `expr:"item"`

	// State carries the current state's metadata (path, description) for
	// pongo2 view templates. Authors reference it as `{{ state.path }}`,
	// `{{ state.description }}` inside views and inside the per-app
	// `views/base.pongo` shared layout. Deliberately NOT added to the
	// expr-lang whitelist (allowedRoots) — bare `state` references in
	// `when:` guards stay rejected. The field is consumed only by
	// internal/render's pongo bridge (ToContext). View-render call sites
	// populate it; effect / guard evaluation paths leave it nil.
	State map[string]any `expr:"state"`
}

// PopulateMenuHelpers binds the helper functions (available, blocked,
// blocked_reason, intent_status) to the env's current Menu. View-render
// callers invoke this after assembling Menu so the template helpers can
// answer "is this intent currently primary/blocked?" without each call
// re-walking the menu shape.
func PopulateMenuHelpers(env *Env) {
	primarySet, blockedReasons := indexMenuForHelpers(env.Menu)
	env.Available = func(name string) bool {
		_, ok := primarySet[name]
		return ok
	}
	env.Blocked = func(name string) bool {
		_, ok := blockedReasons[name]
		return ok
	}
	env.BlockedReason = func(name string) string {
		return blockedReasons[name]
	}
	env.IntentStatus = func(name string) string {
		if _, ok := primarySet[name]; ok {
			return "available"
		}
		if _, ok := blockedReasons[name]; ok {
			return "blocked"
		}
		return "unknown"
	}
}

// indexMenuForHelpers walks the env.Menu shape (map[string]any with primary
// and blocked lists of map entries) and returns two lookup tables: the set
// of intent names in primary, and intent → reason for blocked entries.
func indexMenuForHelpers(menu map[string]any) (map[string]struct{}, map[string]string) {
	primary := make(map[string]struct{})
	blocked := make(map[string]string)
	if menu == nil {
		return primary, blocked
	}
	if p, ok := menu["primary"].([]any); ok {
		for _, e := range p {
			if m, ok := e.(map[string]any); ok {
				if n, ok := m["intent"].(string); ok && n != "" {
					primary[n] = struct{}{}
				}
			}
		}
	}
	if b, ok := menu["blocked"].([]any); ok {
		for _, e := range b {
			if m, ok := e.(map[string]any); ok {
				name, _ := m["intent"].(string)
				reason, _ := m["reason"].(string)
				if name != "" {
					blocked[name] = reason
				}
			}
		}
	}
	return primary, blocked
}

// RunCtx holds the run-level metadata visible in expressions as the `run`
// root, so authors can branch on session identity or turn number without the
// orchestrator threading those values through every slot. The zero value
// (empty ID, turn 0) is meaningful: it is what guards see before the first
// turn is recorded.
type RunCtx struct {
	// ID is the session identifier.
	ID string `expr:"id"`
	// Turn is the current turn number.
	Turn int64 `expr:"turn"`
}

// Program is a compiled, whitelist-checked expression ready for repeated
// evaluation. The underlying expr-lang program is held opaquely so callers
// never import expr-lang/expr directly — the only legal way to obtain one
// is through [Compile] or [CompileBool], which guarantees the AST passed the
// whitelist. The zero value is not usable; a *Program is immutable after
// compilation and therefore safe for concurrent evaluation from many
// goroutines.
type Program struct {
	source  string
	program *vm.Program
}

// Source returns the original expression string the Program was compiled
// from. It exists so error messages and traces can quote the author's text
// rather than a reconstructed form, keeping diagnostics matchable against
// the YAML.
func (p *Program) Source() string { return p.source }

// allowedBuiltins is the set of expr-lang builtin function names that pass
// the AST whitelist. Everything not in this set is rejected at compile time.
var allowedBuiltins = map[string]bool{
	"len":       true,
	"trim":      true,
	"upper":     true,
	"lower":     true,
	"split":     true,
	"join":      true,
	"hasPrefix": true,
	"hasSuffix": true,
	"now":       true,
	"int":       true,
	"float":     true,
	"string":    true,
	"abs":       true,
	"round":     true,
	"type":      true,
	"get":       true,
	"keys":      true,
	"min":       true,
	"max":       true,
}

// allowedRoots is the set of top-level identifier names that may appear in
// expressions. Member access chains must start with one of these.
var allowedRoots = map[string]bool{
	"slots":     true,
	"world":     true,
	"event":     true,
	"run":       true,
	"args":      true, // handler-local args (host invocations, prompt files)
	"menu":      true, // computed menu (primary/blocked intents) for view templates
	"item":      true, // current element inside a {{ range expr }} block
	"proposal":  true, // $proposal compound-state slot
	"inbox":     true, // $inbox compound-state slot
	"workspace": true, // $workspace compound-state slot
	"result":    true, // host-call Result.Data, exposed to templated `bind:` values
	// Boolean literals, which appear as identifiers in some expr-lang versions.
	"true":  true,
	"false": true,
	"nil":   true,
}

// allowedFunctions is the set of user-defined function names that may appear
// in templates. These are surfaced via function-typed fields on Env (see
// PopulateMenuHelpers) so a call like `available("name")` resolves at
// evaluation time to the closure bound on the env. The whitelist visitor
// admits these CallNodes (other user-defined function calls remain
// rejected).
var allowedFunctions = map[string]bool{
	"available":      true, // available(name) → bool: name is in menu.primary
	"blocked":        true, // blocked(name)   → bool: name is in menu.blocked
	"blocked_reason": true, // blocked_reason(name) → string: reason or ""
	"intent_status":  true, // intent_status(name) → "available"|"blocked"|"unknown"
}

// whitelistVisitor accumulates violations found during the AST walk.
// It is applied as an expr.Patch during compilation.
type whitelistVisitor struct {
	violations []string
}

func (v *whitelistVisitor) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.CallNode:
		// User-defined function calls — only those explicitly listed in
		// allowedFunctions pass the whitelist. The view-template helpers
		// (available, blocked, blocked_reason, intent_status) are surfaced
		// as function-typed fields on Env; expr-lang resolves bare names
		// like `available("foo")` to method-style calls on the env value,
		// which appear here as CallNodes with an IdentifierNode callee.
		// Stdlib builtins (len, trim, …) appear as *ast.BuiltinNode and
		// are checked separately.
		if id, ok := n.Callee.(*ast.IdentifierNode); ok && allowedFunctions[id.Value] {
			break
		}
		v.violations = append(v.violations,
			fmt.Sprintf("forbidden user-defined function call %q (only built-in functions are allowed)", n.Callee))

	case *ast.BuiltinNode:
		if !allowedBuiltins[n.Name] {
			v.violations = append(v.violations,
				fmt.Sprintf("forbidden built-in function %q", n.Name))
		}

	case *ast.PredicateNode:
		// Lambdas/predicates (e.g. filter(x, x > 0)) — forbidden.
		v.violations = append(v.violations, "lambda/predicate expressions are not allowed")

	case *ast.VariableDeclaratorNode:
		// let x = ... — variable declarations are forbidden.
		v.violations = append(v.violations, "variable declarations (let) are not allowed")

	case *ast.MapNode:
		// Map literals {key: val} — too open-ended, forbidden.
		v.violations = append(v.violations, "map literal expressions are not allowed")
	}
}

// memberRootVisitor checks that all MemberNode chains start at an allowed root.
type memberRootVisitor struct {
	violations []string
}

func (v *memberRootVisitor) Visit(node *ast.Node) {
	if mem, ok := (*node).(*ast.MemberNode); ok {
		root := identifierRootOf(mem.Node)
		if root != "" && !allowedRoots[root] {
			v.violations = append(v.violations,
				fmt.Sprintf("member access on forbidden root %q (allowed: slots, world, event, run, args)", root))
		}
	}
}

// identifierRootOf returns the bottom-most identifier in a member-access chain.
func identifierRootOf(node ast.Node) string {
	switch n := node.(type) {
	case *ast.IdentifierNode:
		return n.Value
	case *ast.MemberNode:
		return identifierRootOf(n.Node)
	}
	return ""
}

// lenNilSafeVisitor rewrites every `len(X)` call into `len(X ?? [])` so a nil
// argument (a world key that was never set, or member access through a nil
// map) counts as length 0 rather than crashing the evaluation. This matches Go
// semantics (`len(nil)` on a nil map/slice is 0) and the obvious authoring
// intent: a guard like `len(world.foo.questions) > 0` should read "no
// questions yet" when `foo` is absent, not abort the turn. The coalesce is a
// no-op for any non-nil argument, so it can only turn a crash into 0 — it
// never changes the value of an expression that already evaluated.
//
// The injected nodes (BinaryNode `??`, empty ArrayNode) are allowed node types,
// so they pass the whitelist unchanged.
type lenNilSafeVisitor struct{}

func (lenNilSafeVisitor) Visit(node *ast.Node) {
	n, ok := (*node).(*ast.BuiltinNode)
	if !ok || n.Name != "len" || len(n.Arguments) != 1 {
		return
	}
	if bn, already := n.Arguments[0].(*ast.BinaryNode); already && bn.Operator == "??" {
		return // idempotent: don't double-wrap
	}
	n.Arguments[0] = &ast.BinaryNode{
		Operator: "??",
		Left:     n.Arguments[0],
		Right:    &ast.ArrayNode{},
	}
}

// compileWithOpts compiles using the shared visitor set and optional extra options.
func compileWithOpts(source string, extra ...exprpkg.Option) (*Program, error) {
	wl := &whitelistVisitor{}
	mr := &memberRootVisitor{}

	opts := []exprpkg.Option{
		exprpkg.Env(Env{}),
		exprpkg.AllowUndefinedVariables(),
		exprpkg.Patch(wl),
		exprpkg.Patch(mr),
		exprpkg.Patch(lenNilSafeVisitor{}),
	}
	opts = append(opts, extra...)

	prog, err := exprpkg.Compile(source, opts...)
	if err != nil {
		return nil, fmt.Errorf("expr compile %q: %w", source, err)
	}

	var violations []string
	violations = append(violations, wl.violations...)
	violations = append(violations, mr.violations...)
	if len(violations) > 0 {
		return nil, fmt.Errorf("expr whitelist violation in %q: %s", source, strings.Join(violations, "; "))
	}

	return &Program{source: source, program: prog}, nil
}

// Compile compiles source into a Program that may return any type, for
// effect value expressions and template interpolations where the result is
// rendered or bound rather than branched on. It returns an error when
// expr-lang cannot parse the source or when the AST trips the whitelist
// (forbidden builtin, lambda/predicate, map literal, let, or member access
// on a non-allowed root); the error names the offending construct so the
// author can fix the YAML.
func Compile(source string) (*Program, error) {
	return compileWithOpts(source, exprpkg.AsAny())
}

// CompileBool compiles source into a Program constrained to return a bool,
// for guard expressions (`when:` clauses) where a non-bool result is an
// authoring mistake worth catching at load time rather than mid-turn. Its
// error conditions are those of [Compile] plus a type error when the
// expression cannot yield a bool.
func CompileBool(source string) (*Program, error) {
	return compileWithOpts(source, exprpkg.AsBool())
}

// EvalBool evaluates a guard program against env and returns its bool result.
// It returns an error if the underlying VM faults or if the program yields a
// non-bool value, so a guard that silently drifts to a non-bool surfaces as
// a turn error rather than a misread branch. EvalBool only reads p and env,
// so a single *Program may be evaluated concurrently with different envs.
func EvalBool(p *Program, env Env) (bool, error) {
	out, err := vm.Run(p.program, env)
	if err != nil {
		return false, fmt.Errorf("expr eval %q: %w", p.source, err)
	}
	b, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("expr eval %q: expected bool, got %T", p.source, out)
	}
	return b, nil
}

// EvalAny evaluates a program against env and returns the raw value without
// type coercion, leaving rendering or type assertions to the caller. Like
// [EvalBool] it only reads its arguments, so one *Program is safe to evaluate
// concurrently; it errors only when the underlying VM faults.
func EvalAny(p *Program, env Env) (any, error) {
	out, err := vm.Run(p.program, env)
	if err != nil {
		return nil, fmt.Errorf("expr eval %q: %w", p.source, err)
	}
	return out, nil
}

// ─── Render / template engine ─────────────────────────────────────────────────

// Render interpolates an expr-lang template against env and returns the
// rendered string. It supports {{ expr }}, {{ if }}/{{ else }}/{{ end }}, and
// {{ range }} blocks; literal text between blocks passes through unchanged. A
// template with no {{ is returned verbatim, so the common no-interpolation
// case skips the parser entirely. Errors are returned for malformed block
// structure (unclosed/mismatched delimiters) or for any embedded expression
// that fails to compile or evaluate. Render compiles each embedded expression
// once and caches it process-wide, so repeated rendering of the same template
// amortises compilation; the caches are concurrency-safe.
func Render(tmpl string, env Env) (string, error) {
	if !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}
	tree, err := parseTemplate(tmpl)
	if err != nil {
		return "", err
	}
	return execTmplTree(tree, env)
}

// ValidateTemplate compile-checks a multi-interpolation template string
// WITHOUT evaluating it, so a load-time validator can reject a malformed
// template the moment an app is parsed rather than mid-turn when [Render]
// first runs it. It mirrors Render's parse path exactly — same tokeniser,
// same {{ }} / {{ if }} / {{ range }} grammar — so it neither false-positives
// on a valid template nor false-negatives on a broken one. A template with no
// {{ is a pure literal and passes trivially. For every embedded expression,
// guard condition, and range list it walks, ValidateTemplate runs the same
// whitelist-checked [Compile] the renderer would use, surfacing parse errors
// (e.g. a stray pongo `|filter:` operator) and whitelist violations as a
// returned error. It does not touch the per-expression eval caches.
func ValidateTemplate(tmpl string) error {
	if !strings.Contains(tmpl, "{{") {
		return nil
	}
	tree, err := parseTemplate(tmpl)
	if err != nil {
		return err
	}
	return validateNodes(tree.nodes)
}

// validateNodes compile-checks every expression embedded in a parsed template
// tree, recursing through if/range bodies. Guard conditions compile as bool
// programs (matching the renderer's evalBoolCached path); interpolations and
// range lists compile as any-typed programs (matching evalExprCached). The
// range-body dot-rewrite is applied so `.field` references inside a
// `{{ range }}` validate against the `item` root just as they evaluate.
func validateNodes(nodes []*tmplNode) error {
	for _, n := range nodes {
		switch n.kind {
		case nodeText:
			// Literal text — nothing to compile.
		case nodeExpr:
			if _, err := Compile(n.exprSrc); err != nil {
				return err
			}
		case nodeIf:
			if _, err := CompileBool(n.cond); err != nil {
				return err
			}
			if err := validateNodes(n.thenBody); err != nil {
				return err
			}
			if err := validateNodes(n.elseBody); err != nil {
				return err
			}
		case nodeRange:
			if _, err := Compile(n.cond); err != nil {
				return err
			}
			if err := validateRangeNodes(n.thenBody); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateRangeNodes mirrors validateNodes for the body of a {{ range }} block,
// applying the same leading-dot rewrite the renderer uses (see
// rewriteDotsForRange) so `.field` accesses compile against the `item` root.
func validateRangeNodes(nodes []*tmplNode) error {
	for _, n := range nodes {
		switch n.kind {
		case nodeText:
		case nodeExpr:
			if _, err := Compile(rewriteDotsForRange(n.exprSrc)); err != nil {
				return err
			}
		case nodeIf:
			if _, err := CompileBool(rewriteDotsForRange(n.cond)); err != nil {
				return err
			}
			if err := validateRangeNodes(n.thenBody); err != nil {
				return err
			}
			if err := validateRangeNodes(n.elseBody); err != nil {
				return err
			}
		case nodeRange:
			if _, err := Compile(rewriteDotsForRange(n.cond)); err != nil {
				return err
			}
			if err := validateRangeNodes(n.thenBody); err != nil {
				return err
			}
		}
	}
	return nil
}

// ─── Template AST ─────────────────────────────────────────────────────────────

type nodeKind int

const (
	nodeText nodeKind = iota
	nodeExpr
	nodeIf
	nodeRange
)

type tmplNode struct {
	kind     nodeKind
	text     string      // nodeText: literal text
	exprSrc  string      // nodeExpr: expression source to interpolate
	cond     string      // nodeIf: guard expression / nodeRange: list expression
	thenBody []*tmplNode // nodeIf: then branch / nodeRange: loop body
	elseBody []*tmplNode // nodeIf: else branch
}

type tmplTree struct {
	nodes []*tmplNode
}

// parseTemplate tokenises and parses the template into a tmplTree.
// Grammar (simplified):
//
//	template = { text | '{{' expr '}}' | if-block }
//	if-block = '{{' 'if' expr '}}' template [ '{{' 'else' '}}' template ] '{{' 'end' '}}'
func parseTemplate(src string) (*tmplTree, error) {
	tokens, err := tokenise(src)
	if err != nil {
		return nil, err
	}
	nodes, _, err := parseNodes(tokens, 0, false)
	if err != nil {
		return nil, err
	}
	return &tmplTree{nodes: nodes}, nil
}

type token struct {
	isBlock bool   // true = {{ ... }}, false = literal text
	val     string // raw content (trimmed for blocks)
}

// tokenise splits src into alternating literal-text and {{ }} block tokens.
func tokenise(src string) ([]token, error) {
	var tokens []token
	for len(src) > 0 {
		start := strings.Index(src, "{{")
		if start < 0 {
			tokens = append(tokens, token{val: src})
			break
		}
		if start > 0 {
			tokens = append(tokens, token{val: src[:start]})
		}
		src = src[start+2:] // skip "{{"
		end := strings.Index(src, "}}")
		if end < 0 {
			return nil, fmt.Errorf("template: unclosed {{ block")
		}
		block := strings.TrimSpace(src[:end])
		tokens = append(tokens, token{isBlock: true, val: block})
		src = src[end+2:] // skip "}}"
	}
	return tokens, nil
}

// parseNodes parses a sequence of tmplNodes from the token list starting at
// index i. Returns the nodes and the index of the next unconsumed token.
// If inBlock is true, parsing stops at 'else' or 'end' blocks.
func parseNodes(tokens []token, i int, inBlock bool) ([]*tmplNode, int, error) {
	var nodes []*tmplNode
	for i < len(tokens) {
		t := tokens[i]
		if !t.isBlock {
			nodes = append(nodes, &tmplNode{kind: nodeText, text: t.val})
			i++
			continue
		}
		// Block token.
		keyword, rest := splitKeyword(t.val)
		switch keyword {
		case "end":
			if inBlock {
				return nodes, i, nil // caller will consume 'end'
			}
			return nil, 0, fmt.Errorf("template: unexpected 'end'")

		case "else":
			if inBlock {
				return nodes, i, nil // caller will consume 'else'
			}
			return nil, 0, fmt.Errorf("template: unexpected 'else'")

		case "if":
			cond := strings.TrimSpace(rest)
			i++ // consume 'if' token
			thenBody, j, err := parseNodes(tokens, i, true)
			if err != nil {
				return nil, 0, err
			}
			i = j
			var elseBody []*tmplNode
			if i < len(tokens) && tokens[i].isBlock {
				kw, _ := splitKeyword(tokens[i].val)
				if kw == "else" {
					i++ // consume 'else'
					elseBody, j, err = parseNodes(tokens, i, true)
					if err != nil {
						return nil, 0, err
					}
					i = j
				}
			}
			// Expect 'end'.
			if i >= len(tokens) || !tokens[i].isBlock {
				return nil, 0, fmt.Errorf("template: missing 'end' for if block")
			}
			kw, _ := splitKeyword(tokens[i].val)
			if kw != "end" {
				return nil, 0, fmt.Errorf("template: expected 'end', got %q", tokens[i].val)
			}
			i++ // consume 'end'
			nodes = append(nodes, &tmplNode{
				kind:     nodeIf,
				cond:     cond,
				thenBody: thenBody,
				elseBody: elseBody,
			})

		case "range":
			listExpr := strings.TrimSpace(rest)
			i++ // consume 'range' token
			body, j, err := parseNodes(tokens, i, true)
			if err != nil {
				return nil, 0, err
			}
			i = j
			// Expect 'end' (no else for range).
			if i >= len(tokens) || !tokens[i].isBlock {
				return nil, 0, fmt.Errorf("template: missing 'end' for range block")
			}
			kw, _ := splitKeyword(tokens[i].val)
			if kw != "end" {
				return nil, 0, fmt.Errorf("template: expected 'end' for range, got %q", tokens[i].val)
			}
			i++ // consume 'end'
			nodes = append(nodes, &tmplNode{
				kind:     nodeRange,
				cond:     listExpr,
				thenBody: body,
			})

		default:
			// Plain expression interpolation.
			nodes = append(nodes, &tmplNode{kind: nodeExpr, exprSrc: t.val})
			i++
		}
	}
	return nodes, i, nil
}

// splitKeyword splits "if expr", "else", "end", etc. into (keyword, rest).
func splitKeyword(s string) (string, string) {
	idx := strings.IndexByte(s, ' ')
	if idx < 0 {
		return s, ""
	}
	return s[:idx], s[idx+1:]
}

// execTmplTree renders a parsed template tree against an Env.
func execTmplTree(tree *tmplTree, env Env) (string, error) {
	var sb strings.Builder
	if err := execNodes(tree.nodes, env, &sb); err != nil {
		return "", err
	}
	return sb.String(), nil
}

func execNodes(nodes []*tmplNode, env Env, out *strings.Builder) error {
	for _, n := range nodes {
		switch n.kind {
		case nodeText:
			out.WriteString(n.text)

		case nodeExpr:
			// Compile (with cache) and evaluate.
			v, err := evalExprCached(n.exprSrc, env)
			if err != nil {
				return fmt.Errorf("template expr %q: %w", n.exprSrc, err)
			}
			out.WriteString(anyToString(v))

		case nodeIf:
			truthy, err := evalBoolCached(n.cond, env)
			if err != nil {
				return fmt.Errorf("template if-cond %q: %w", n.cond, err)
			}
			if truthy {
				if err := execNodes(n.thenBody, env, out); err != nil {
					return err
				}
			} else {
				if err := execNodes(n.elseBody, env, out); err != nil {
					return err
				}
			}

		case nodeRange:
			v, err := evalExprCached(n.cond, env)
			if err != nil {
				return fmt.Errorf("template range-expr %q: %w", n.cond, err)
			}
			items, err := coerceToList(v)
			if err != nil {
				return fmt.Errorf("template range %q: %w", n.cond, err)
			}
			for _, item := range items {
				// Per-iteration env: copy and bind Item. The body's bare-dot
				// references (`.display`, `.foo`) are rewritten to
				// `item.display` at exec time by the dot-rewriter (see
				// rewriteDotsForRange).
				iterEnv := env
				iterEnv.Item = item
				if err := execRangeBody(n.thenBody, iterEnv, out); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// execRangeBody renders the body of a {{ range }} block, rewriting dot-
// prefixed identifiers (`.display`) inside each nested expression to bind
// against the `item` env field. Nested if/range nodes recurse through the
// same rewriter so multi-level templates work.
func execRangeBody(nodes []*tmplNode, env Env, out *strings.Builder) error {
	for _, n := range nodes {
		switch n.kind {
		case nodeText:
			out.WriteString(n.text)
		case nodeExpr:
			rewritten := rewriteDotsForRange(n.exprSrc)
			v, err := evalExprCached(rewritten, env)
			if err != nil {
				return fmt.Errorf("template expr %q (in range): %w", n.exprSrc, err)
			}
			out.WriteString(anyToString(v))
		case nodeIf:
			rewritten := rewriteDotsForRange(n.cond)
			truthy, err := evalBoolCached(rewritten, env)
			if err != nil {
				return fmt.Errorf("template if-cond %q (in range): %w", n.cond, err)
			}
			if truthy {
				if err := execRangeBody(n.thenBody, env, out); err != nil {
					return err
				}
			} else {
				if err := execRangeBody(n.elseBody, env, out); err != nil {
					return err
				}
			}
		case nodeRange:
			// Nested range: evaluate (with outer dot-rewrite for the list
			// expression) and recurse with the new item binding. We do not
			// support `.` referring to the outer item from within a nested
			// body — keep it simple (it's not required by current view
			// templates).
			rewritten := rewriteDotsForRange(n.cond)
			v, err := evalExprCached(rewritten, env)
			if err != nil {
				return fmt.Errorf("template range-expr %q (nested): %w", n.cond, err)
			}
			items, err := coerceToList(v)
			if err != nil {
				return fmt.Errorf("template range %q (nested): %w", n.cond, err)
			}
			for _, item := range items {
				iterEnv := env
				iterEnv.Item = item
				if err := execRangeBody(n.thenBody, iterEnv, out); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// rewriteDotsForRange rewrites dot-prefixed identifiers inside an expression
// so they reference the env's Item binding. Specifically:
//
//	.foo       → item.foo
//	.foo.bar   → item.foo.bar
//	a.foo      → a.foo            (unchanged — only leading-dot tokens rewrite)
//
// Implementation: walk the expression looking for occurrences of `.<letter>`
// preceded by a token boundary (start of string, whitespace, or operator
// character). Replace the leading `.` with `item.`.
func rewriteDotsForRange(src string) string {
	if !strings.Contains(src, ".") {
		return src
	}
	var sb strings.Builder
	sb.Grow(len(src) + 8)
	prevBoundary := true
	for i := 0; i < len(src); i++ {
		c := src[i]
		// Detect leading-dot: `.` preceded by a token boundary and followed
		// by an identifier-start (letter or underscore).
		if c == '.' && prevBoundary && i+1 < len(src) && isIdentStart(src[i+1]) {
			sb.WriteString("item.")
			prevBoundary = false
			continue
		}
		sb.WriteByte(c)
		// Update boundary tracking: identifier and digit chars are NOT
		// boundaries (so `a.foo` stays as `a.foo`); everything else is.
		prevBoundary = !isIdentCont(c)
	}
	return sb.String()
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// coerceToList converts a value into a slice for iteration. Supports:
//
//   - []any              (the canonical menu shape)
//   - []map[string]any   (defensive — JSON unmarshalling may produce this)
//   - []string           (occasionally useful for `{{ range world.tags }}`)
//
// Other types return an empty slice with no error (range over nil/empty
// is a no-op, matching Go text/template behaviour).
func coerceToList(v any) ([]any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []any:
		return x, nil
	case []map[string]any:
		out := make([]any, len(x))
		for i, m := range x {
			out[i] = m
		}
		return out, nil
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("range expression yields %T, expected list", v)
	}
}

// RenderValue evaluates a template string that contains exactly one {{ expr }}
// block covering the whole value and returns the typed result (bool, int64, etc.)
// rather than a string. This is used for effect values where the template produces
// a typed world value (e.g. `"{{ world.disturbance > 2 }}"` → bool true/false).
//
// If the template is NOT a single pure-expression form (i.e. has surrounding text
// or multiple blocks), it falls back to Render and returns a string.
func RenderValue(tmpl string, env Env) (any, error) {
	stripped := strings.TrimSpace(tmpl)
	// Check for a single {{ expr }} block wrapping the ENTIRE value. The
	// HasPrefix/HasSuffix pair is necessary but not sufficient: a
	// multi-interpolation template like "{{ a }}Q{{ b }}: {{ c }}" also
	// begins with "{{" and ends with "}}", yet it is a string template,
	// not one expression. Requiring the inner text to contain no further
	// delimiters keeps that case on the Render path — compiling its
	// middle ("a }}Q{{ b }}: {{ c") as a single expr would fail.
	if strings.HasPrefix(stripped, "{{") && strings.HasSuffix(stripped, "}}") {
		inner := stripped[2 : len(stripped)-2]
		if !strings.Contains(inner, "{{") && !strings.Contains(inner, "}}") {
			inner = strings.TrimSpace(inner)
			// Only treat as a typed value if it doesn't start with a block keyword.
			kw, _ := splitKeyword(inner)
			if kw != "if" && kw != "else" && kw != "end" {
				return evalExprCached(inner, env)
			}
		}
	}
	// Fallback to string render.
	return Render(tmpl, env)
}

// ─── Per-expression caches ────────────────────────────────────────────────────

var (
	anyProgCache  sync.Map // map[string]*Program (AsAny)
	boolProgCache sync.Map // map[string]*Program (AsBool)
)

func evalExprCached(src string, env Env) (any, error) {
	if v, ok := anyProgCache.Load(src); ok {
		return EvalAny(v.(*Program), env)
	}
	p, err := Compile(src)
	if err != nil {
		return nil, err
	}
	anyProgCache.Store(src, p)
	return EvalAny(p, env)
}

func evalBoolCached(src string, env Env) (bool, error) {
	// Handle "not expr" by compiling "!(expr)" since expr-lang supports `!`.
	exprSrc := src
	if strings.HasPrefix(src, "not ") {
		exprSrc = "!(" + strings.TrimPrefix(src, "not ") + ")"
	}

	if v, ok := boolProgCache.Load(exprSrc); ok {
		return EvalBool(v.(*Program), env)
	}
	p, err := CompileBool(exprSrc)
	if err != nil {
		// If bool compilation fails, try AsAny and cast.
		p2, err2 := Compile(exprSrc)
		if err2 != nil {
			return false, err
		}
		boolProgCache.Store(exprSrc, p2)
		v, err2 := EvalAny(p2, env)
		if err2 != nil {
			return false, err2
		}
		return isTruthy(v), nil
	}
	boolProgCache.Store(exprSrc, p)
	return EvalBool(p, env)
}

// isTruthy mirrors Javascript-like truthiness used in the template conditionals.
func isTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return true
}

// anyToString converts a value to its string representation for interpolation.
//
// Strings, numbers, and bools render via fmt's %v (the expected default).
// Maps and slices render as JSON — Go's `fmt.Sprintf("%v", map[k]v{})` produces
// `map[k:v]` which is unparseable by anything downstream that expects standard
// data interchange (e.g. a Python CLI consuming `--input <slot>={{ world.X }}`).
// JSON-encoding makes templates that pass structured world slots into host.run
// args, prompts, or Jira/Bitbucket comments produce machine-readable output by
// default.  On marshal failure (cyclic graph, unsupported type), fall back to
// %v so a corrupt slot doesn't crash template rendering.
func anyToString(v any) string {
	if v == nil {
		return ""
	}
	switch v.(type) {
	case map[string]any, []any, map[any]any, []map[string]any:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	}
	return fmt.Sprintf("%v", v)
}
