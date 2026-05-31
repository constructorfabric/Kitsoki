// Package expr is the expression and template subsystem for the story
// state machine: it wraps github.com/expr-lang/expr behind an AST whitelist
// and adds a small block-template engine. It sits below the orchestrator —
// guards (`when:`), effect values, and view prose all flow through here, so
// that the only expression dialect an author ever writes is the restricted,
// auditable one this package enforces, never raw expr-lang or Go templates.
//
// Two surfaces share one [Env]:
//
//   - Expressions — a single value or boolean, compiled by [Compile] /
//     [CompileBool] and run by [EvalAny] / [EvalBool]. Guards and bound
//     effect values use these.
//   - Templates — `{{ … }}`-delimited prose with conditional and range
//     blocks, rendered by [Render] / [RenderValue]. View text uses these.
//
// # Algorithm
//
// Compilation runs the author's source through expr-lang's parser, then
// walks the resulting AST with two whitelist visitors before the program is
// accepted:
//
//  1. whitelistVisitor rejects forbidden node shapes — built-in functions
//     outside allowedBuiltins, lambda/predicate nodes, `let` variable
//     declarations, and map literals — and admits only the named helper
//     calls in allowedFunctions.
//  2. memberRootVisitor walks every member-access chain (`a.b.c`) down to
//     its bottom identifier and rejects the expression unless that root is
//     in allowedRoots (slots, world, event, run, args, menu, item, …).
//
// If either visitor accumulates a violation, Compile returns an error naming
// the offending construct; otherwise it hands back an opaque, immutable
// [Program]. Evaluation is a plain expr-lang VM run against an [Env] value.
//
// Template rendering is a separate, ~100-line parser. Render tokenises the
// string into literal-text and `{{ }}` tokens, parses block keywords
// (if/else/range/end) into a small node tree, then executes the tree:
// literal nodes are copied through, expression nodes are compiled (via a
// process-wide cache) and stringified, if-nodes branch on a cached boolean,
// and range-nodes iterate a coerced list, binding each element to the `item`
// root. Inside a range body the bare-dot form `.field` is rewritten to
// `item.field` before evaluation.
//
// # Invariants
//
//   - The only path to a [Program] is [Compile] / [CompileBool]; a Program
//     therefore always represents source that passed the whitelist.
//   - A *Program is immutable after compilation and safe for concurrent
//     evaluation; the expression and template caches are concurrency-safe.
//   - Member access is gated by root, not by leaf — `world.anything` is
//     allowed, `os.Getenv` is not, regardless of depth.
//   - Boolean truthiness in template `{{ if }}` blocks follows the
//     JavaScript-style rule in isTruthy (nil/0/""/false are falsy), not Go's
//     stricter bool-only rule.
//
// # Worked example
//
// A cloak room declares `world.wearing_cloak` and a view line that branches
// on it. Rendering that line:
//
//	tmpl: "The hall is {{ if world.wearing_cloak }}dark{{ else }}lit{{ end }}."
//	env:  Env{World: {"wearing_cloak": false}}
//
//	tokenise → [ "The hall is " | if world.wearing_cloak | "dark" | else | "lit" | end | "." ]
//	parse    → [ text("The hall is ") , if{cond:"world.wearing_cloak", then:[text("dark")], else:[text("lit")]} , text(".") ]
//	exec     → cond compiles to bool, evaluates false → take else branch
//	out:  "The hall is lit."
//
// A guard over the same world variable compiles and evaluates directly:
//
//	src:  "world.wearing_cloak == false"
//	CompileBool → *Program (whitelist: member root `world` allowed, no
//	              forbidden nodes)
//	EvalBool(p, Env{World: {"wearing_cloak": false}}) → true
//
// Runnable forms of both traces live in [ExampleRender] and
// [ExampleCompileBool].
//
// # Lifecycle
//
// Guard and view-template source is compiled once at machine load (and the
// template engine additionally memoises embedded expressions process-wide on
// first render), so per-turn cost is evaluation only. View-render call sites
// assemble an [Env], then call [PopulateMenuHelpers] to bind the menu helper
// closures before rendering view prose; guard and effect evaluation paths
// leave those helpers, and the Menu/State/Item roots, nil.
//
// # Non-goals
//
//   - Full expr-lang compatibility. Most builtins, arbitrary user functions,
//     lambdas/predicates, and map literals are rejected — the conservative
//     whitelist keeps the expression surface small enough to audit and
//     predict, which matters more here than expressive power.
//   - A Turing-complete template language. The engine has interpolation,
//     if/else, and range and nothing else (no assignment, no recursion, no
//     custom functions); templates are declarative view text, not programs.
//   - Go text/template syntax. The YAML already uses expr-lang expression
//     syntax inside `{{ }}` (ternary, `==`, dotless member access), which is
//     not valid Go template syntax, so a small purpose-built parser is used
//     instead of adapting one.
//   - Sandboxed evaluation of untrusted input. The whitelist constrains what
//     story authors may write; it is not a security boundary against
//     adversarial end-user strings, which never reach the compiler.
//
// # Reference
//
// The author-facing guide to expressions and template interpolation is
// docs/stories/authoring.md §5.7; the guard/world/scope model lives in
// docs/stories/state-machine.md.
package expr
