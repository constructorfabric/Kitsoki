// Package expr wraps github.com/expr-lang/expr with an AST whitelist that
// enforces the restricted expression language defined in §3.5 and §6.
//
// # Template rendering
//
// The view and guard_hint fields in the YAML use expr-lang template syntax
// with {{ }} delimiters — NOT Go text/template. Constructs supported:
//
//	{{ expr }}           — interpolate an expr-lang expression (any type, toString'd)
//	{{ if expr }}        — conditional block start (expr must be truthy)
//	{{ else }}           — else branch
//	{{ end }}            — close if/else block
//
// The ternary operator (e.g. {{ world.wearing_cloak ? 'dark' : 'lit' }}) is
// handled by expr-lang natively. Block constructs (if/else/end) are handled
// by our custom Render function.
//
// Design rationale: We chose a small custom parser over Go text/template because:
//  1. The YAML already uses expr-lang expression syntax (ternary, ==, member access)
//     inside {{ }}, which is NOT valid Go template syntax.
//  2. text/template requires .world, .slots (with leading dot); the YAML omits the dot.
//  3. A custom parser is ~100 lines and covers 100% of what the Cloak app needs.
//
// # AST whitelist
//
// Allowed built-in functions (a conservative subset of expr-lang's builtins):
//
//	len, trim, upper, lower, split, join, hasPrefix, hasSuffix, now,
//	int, float, string, abs, round, type, get, keys, min, max
//
// Allowed identifier roots: slots, world, event, run.
// Forbidden: lambda/predicate nodes, map literals, variable declarations (let),
// raw user-defined function calls (CallNode — builtins are BuiltinNode).
package expr

import (
	"fmt"
	"strings"
	"sync"

	exprpkg "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm"
)

// Env is the evaluation scope available to every expression (§3.5):
//   - Slots   map[string]any  — the current intent's slot values
//   - World   map[string]any  — the current world snapshot
//   - Event   map[string]any  — the triggering event (if any)
//   - Run     RunCtx          — run-level metadata (id, turn, timestamps)
type Env struct {
	Slots map[string]any `expr:"slots"`
	World map[string]any `expr:"world"`
	Event map[string]any `expr:"event"`
	Run   RunCtx         `expr:"run"`
}

// RunCtx holds the run-level metadata visible in expressions.
type RunCtx struct {
	// ID is the session identifier.
	ID string `expr:"id"`
	// Turn is the current turn number.
	Turn int64 `expr:"turn"`
}

// Program is a compiled expression, ready for repeated evaluation.
// The underlying expr-lang program is stored opaquely so callers don't
// import expr-lang/expr directly.
type Program struct {
	source  string
	program *vm.Program
}

// Source returns the original expression string.
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
	"slots": true,
	"world": true,
	"event": true,
	"run":   true,
	// Boolean literals, which appear as identifiers in some expr-lang versions.
	"true":  true,
	"false": true,
	"nil":   true,
}

// whitelistVisitor accumulates violations found during the AST walk.
// It is applied as an expr.Patch during compilation.
type whitelistVisitor struct {
	violations []string
}

func (v *whitelistVisitor) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.CallNode:
		// Raw user-defined function calls — forbidden.
		// Builtins are represented as *ast.BuiltinNode, not *ast.CallNode.
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
				fmt.Sprintf("member access on forbidden root %q (allowed: slots, world, event, run)", root))
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

// compileWithOpts compiles using the shared visitor set and optional extra options.
func compileWithOpts(source string, extra ...exprpkg.Option) (*Program, error) {
	wl := &whitelistVisitor{}
	mr := &memberRootVisitor{}

	opts := []exprpkg.Option{
		exprpkg.Env(Env{}),
		exprpkg.AllowUndefinedVariables(),
		exprpkg.Patch(wl),
		exprpkg.Patch(mr),
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

// Compile compiles source into a Program that may return any type.
// Use for effect value expressions and template interpolations.
func Compile(source string) (*Program, error) {
	return compileWithOpts(source, exprpkg.AsAny())
}

// CompileBool compiles source into a Program that must return a bool.
// Use for guard expressions (when: clauses).
func CompileBool(source string) (*Program, error) {
	return compileWithOpts(source, exprpkg.AsBool())
}

// EvalBool evaluates a guard program and returns a bool.
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

// EvalAny evaluates a program and returns the raw value.
func EvalAny(p *Program, env Env) (any, error) {
	out, err := vm.Run(p.program, env)
	if err != nil {
		return nil, fmt.Errorf("expr eval %q: %w", p.source, err)
	}
	return out, nil
}

// ─── Render / template engine ─────────────────────────────────────────────────

// renderCache caches parsed template trees keyed by source string.
var renderCache sync.Map // map[string]*tmplTree

// Render interpolates an expr-lang template against an Env.
// Supports {{ expr }}, {{ if expr }}...{{ else }}...{{ end }} blocks.
// Literal text between blocks is passed through unchanged.
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

// ─── Template AST ─────────────────────────────────────────────────────────────

type nodeKind int

const (
	nodeText nodeKind = iota
	nodeExpr
	nodeIf
)

type tmplNode struct {
	kind     nodeKind
	text     string      // nodeText: literal text
	exprSrc  string      // nodeExpr: expression source to interpolate
	cond     string      // nodeIf: guard expression
	thenBody []*tmplNode // nodeIf: then branch
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
		}
	}
	return nil
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
	// Check for single {{ expr }} wrapping the entire value.
	if strings.HasPrefix(stripped, "{{") && strings.HasSuffix(stripped, "}}") {
		inner := stripped[2 : len(stripped)-2]
		inner = strings.TrimSpace(inner)
		// Only treat as a typed value if it doesn't start with a block keyword.
		kw, _ := splitKeyword(inner)
		if kw != "if" && kw != "else" && kw != "end" {
			return evalExprCached(inner, env)
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
func anyToString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
