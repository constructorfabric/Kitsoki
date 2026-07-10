package starlark

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

type coverageKey struct{}

// CoverageRecorder collects statement and branch hits from instrumented
// host.starlark.run scripts. It is safe for repeated flow runs in one process.
type CoverageRecorder struct {
	mu      sync.Mutex
	scripts map[string]*ScriptCoverage
}

// ScriptCoverage is the aggregate coverage record for one Starlark script.
type ScriptCoverage struct {
	Script     string                  `json:"script"`
	Statements []StatementCoverageSite `json:"statements,omitempty"`
	Branches   []BranchCoverageSite    `json:"branches,omitempty"`
	FlowFiles  []string                `json:"flow_files,omitempty"`
}

// StatementCoverageSite is one executable statement site.
type StatementCoverageSite struct {
	ID        string   `json:"id"`
	Line      int      `json:"line"`
	Column    int      `json:"column"`
	Kind      string   `json:"kind"`
	Function  string   `json:"function,omitempty"`
	Covered   bool     `json:"covered"`
	Hits      int      `json:"hits,omitempty"`
	FlowFiles []string `json:"flow_files,omitempty"`
}

// BranchCoverageSite records both outcomes of one conditional expression used
// by an if/elif statement.
type BranchCoverageSite struct {
	ID         string   `json:"id"`
	Line       int      `json:"line"`
	Column     int      `json:"column"`
	Kind       string   `json:"kind"`
	Function   string   `json:"function,omitempty"`
	TrueHits   int      `json:"true_hits,omitempty"`
	FalseHits  int      `json:"false_hits,omitempty"`
	TrueFiles  []string `json:"true_files,omitempty"`
	FalseFiles []string `json:"false_files,omitempty"`
}

type coverageContext struct {
	recorder *CoverageRecorder
	flowFile string
}

// NewCoverageRecorder creates an empty Starlark coverage recorder.
func NewCoverageRecorder() *CoverageRecorder {
	return &CoverageRecorder{scripts: map[string]*ScriptCoverage{}}
}

// RegisterScript adds the static statement/branch ledger for a script without
// executing it. Later instrumented runs merge hits into the same sites.
func (r *CoverageRecorder) RegisterScript(script string, src []byte) error {
	if r == nil {
		return nil
	}
	_, sites, err := instrumentCoverageSource(script, src)
	if err != nil {
		return err
	}
	r.mergeSites(script, sites)
	return nil
}

// WithCoverage installs a coverage recorder for a single script invocation.
func WithCoverage(ctx context.Context, recorder *CoverageRecorder, flowFile string) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, coverageKey{}, coverageContext{recorder: recorder, flowFile: flowFile})
}

func coverageFromContext(ctx context.Context) coverageContext {
	cc, _ := ctx.Value(coverageKey{}).(coverageContext)
	return cc
}

func (r *CoverageRecorder) instrument(script string, src []byte) ([]byte, starlark.StringDict) {
	if r == nil {
		return src, nil
	}
	instrumented, sites, err := instrumentCoverageSource(script, src)
	if err != nil {
		r.recordProblem(script, err)
		return src, nil
	}
	r.mergeSites(script, sites)
	return instrumented, starlark.StringDict{
		"__kitsoki_cov":        starlark.NewBuiltin("__kitsoki_cov", r.statementBuiltin),
		"__kitsoki_cov_branch": starlark.NewBuiltin("__kitsoki_cov_branch", r.branchBuiltin),
	}
}

func (r *CoverageRecorder) recordProblem(script string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sc := r.ensureScriptLocked(script)
	sc.Statements = append(sc.Statements, StatementCoverageSite{
		ID:     fmt.Sprintf("%s:instrumentation-error", script),
		Kind:   "instrumentation_error:" + err.Error(),
		Line:   0,
		Column: 0,
	})
}

func (r *CoverageRecorder) mergeSites(script string, sites *ScriptCoverage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sc := r.ensureScriptLocked(script)
	stmtSeen := map[string]bool{}
	for _, s := range sc.Statements {
		stmtSeen[s.ID] = true
	}
	for _, s := range sites.Statements {
		if !stmtSeen[s.ID] {
			sc.Statements = append(sc.Statements, s)
			stmtSeen[s.ID] = true
		}
	}
	branchSeen := map[string]bool{}
	for _, b := range sc.Branches {
		branchSeen[b.ID] = true
	}
	for _, b := range sites.Branches {
		if !branchSeen[b.ID] {
			sc.Branches = append(sc.Branches, b)
			branchSeen[b.ID] = true
		}
	}
}

func (r *CoverageRecorder) statementBuiltin(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var id string
	if err := starlark.UnpackArgs("__kitsoki_cov", args, kwargs, "id", &id); err != nil {
		return nil, err
	}
	flowFile, _ := thread.Local("kitsoki_flow_file").(string)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sc := range r.scripts {
		for i := range sc.Statements {
			if sc.Statements[i].ID == id {
				sc.Statements[i].Hits++
				sc.Statements[i].Covered = true
				sc.Statements[i].FlowFiles = appendUniqueString(sc.Statements[i].FlowFiles, flowFile)
				sc.FlowFiles = appendUniqueString(sc.FlowFiles, flowFile)
				return starlark.None, nil
			}
		}
	}
	return starlark.None, nil
}

func (r *CoverageRecorder) branchBuiltin(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var id string
	var value starlark.Value
	if err := starlark.UnpackArgs("__kitsoki_cov_branch", args, kwargs, "id", &id, "value", &value); err != nil {
		return nil, err
	}
	flowFile, _ := thread.Local("kitsoki_flow_file").(string)
	truth := bool(value.Truth())
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sc := range r.scripts {
		for i := range sc.Branches {
			if sc.Branches[i].ID == id {
				if truth {
					sc.Branches[i].TrueHits++
					sc.Branches[i].TrueFiles = appendUniqueString(sc.Branches[i].TrueFiles, flowFile)
				} else {
					sc.Branches[i].FalseHits++
					sc.Branches[i].FalseFiles = appendUniqueString(sc.Branches[i].FalseFiles, flowFile)
				}
				sc.FlowFiles = appendUniqueString(sc.FlowFiles, flowFile)
				return value, nil
			}
		}
	}
	return value, nil
}

func (r *CoverageRecorder) ensureScriptLocked(script string) *ScriptCoverage {
	if r.scripts == nil {
		r.scripts = map[string]*ScriptCoverage{}
	}
	sc := r.scripts[script]
	if sc == nil {
		sc = &ScriptCoverage{Script: script}
		r.scripts[script] = sc
	}
	return sc
}

// Snapshot returns a stable copy of all recorded coverage.
func (r *CoverageRecorder) Snapshot() []ScriptCoverage {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ScriptCoverage, 0, len(r.scripts))
	for _, sc := range r.scripts {
		cp := ScriptCoverage{
			Script:     sc.Script,
			Statements: append([]StatementCoverageSite(nil), sc.Statements...),
			Branches:   append([]BranchCoverageSite(nil), sc.Branches...),
			FlowFiles:  append([]string(nil), sc.FlowFiles...),
		}
		sort.Slice(cp.Statements, func(i, j int) bool { return cp.Statements[i].ID < cp.Statements[j].ID })
		sort.Slice(cp.Branches, func(i, j int) bool { return cp.Branches[i].ID < cp.Branches[j].ID })
		sort.Strings(cp.FlowFiles)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Script < out[j].Script })
	return out
}

type coverageEdit struct {
	start int
	end   int
	text  string
}

func instrumentCoverageSource(script string, src []byte) ([]byte, *ScriptCoverage, error) {
	file, err := (&syntax.FileOptions{}).Parse(script, src, 0)
	if err != nil {
		return nil, nil, err
	}
	lineOffsets := sourceLineOffsets(src)
	sites := &ScriptCoverage{Script: script}
	var edits []coverageEdit
	seenStmt := map[int]bool{}
	seenBranch := map[int]bool{}
	var stack []string
	var walk func([]syntax.Stmt, map[*syntax.IfStmt]bool)
	walk = func(stmts []syntax.Stmt, elif map[*syntax.IfStmt]bool) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *syntax.LoadStmt:
				continue
			case *syntax.DefStmt:
				name := ""
				if s.Name != nil {
					name = s.Name.Name
				}
				stack = append(stack, name)
				walk(s.Body, nil)
				stack = stack[:len(stack)-1]
				continue
			}
			start, _ := stmt.Span()
			line := int(start.Line)
			col := int(start.Col)
			if line > 0 && !seenStmt[line] && !isElif(stmt, elif) {
				seenStmt[line] = true
				id := coverageID(script, "stmt", line, col)
				sites.Statements = append(sites.Statements, StatementCoverageSite{
					ID:       id,
					Line:     line,
					Column:   col,
					Kind:     stmtKind(stmt),
					Function: currentFunction(stack),
				})
				off, ok := offsetForPosition(src, lineOffsets, start)
				if ok {
					edits = append(edits, coverageEdit{start: lineStartOffset(lineOffsets, line), text: strings.Repeat(" ", col-1) + fmt.Sprintf("__kitsoki_cov(%q)\n", id)})
					_ = off
				}
			}
			switch s := stmt.(type) {
			case *syntax.IfStmt:
				condStart, condEnd := s.Cond.Span()
				condOff, ok1 := offsetForPosition(src, lineOffsets, condStart)
				condEndOff, ok2 := offsetForPosition(src, lineOffsets, condEnd)
				if ok1 && ok2 && !seenBranch[condOff] {
					seenBranch[condOff] = true
					id := coverageID(script, "branch", int(condStart.Line), int(condStart.Col))
					sites.Branches = append(sites.Branches, BranchCoverageSite{
						ID:       id,
						Line:     int(condStart.Line),
						Column:   int(condStart.Col),
						Kind:     "if",
						Function: currentFunction(stack),
					})
					cond := string(src[condOff:condEndOff])
					edits = append(edits, coverageEdit{start: condOff, end: condEndOff, text: fmt.Sprintf("__kitsoki_cov_branch(%q, %s)", id, cond)})
				}
				walk(s.True, nil)
				elifs := map[*syntax.IfStmt]bool{}
				if len(s.False) == 1 {
					if nested, ok := s.False[0].(*syntax.IfStmt); ok && s.ElsePos.Line == nested.If.Line {
						elifs[nested] = true
					}
				}
				walk(s.False, elifs)
			case *syntax.ForStmt:
				walk(s.Body, nil)
			}
		}
	}
	walk(file.Stmts, nil)
	out := applyCoverageEdits(src, edits)
	return out, sites, nil
}

func coverageID(script, kind string, line, col int) string {
	return fmt.Sprintf("%s:%s:%d:%d", script, kind, line, col)
}

func currentFunction(stack []string) string {
	if len(stack) == 0 {
		return ""
	}
	return stack[len(stack)-1]
}

func isElif(stmt syntax.Stmt, elif map[*syntax.IfStmt]bool) bool {
	if len(elif) == 0 {
		return false
	}
	if s, ok := stmt.(*syntax.IfStmt); ok {
		return elif[s]
	}
	return false
}

func stmtKind(stmt syntax.Stmt) string {
	switch stmt.(type) {
	case *syntax.AssignStmt:
		return "assign"
	case *syntax.BranchStmt:
		return "branch"
	case *syntax.ExprStmt:
		return "expr"
	case *syntax.ForStmt:
		return "for"
	case *syntax.IfStmt:
		return "if"
	case *syntax.ReturnStmt:
		return "return"
	default:
		return fmt.Sprintf("%T", stmt)
	}
}

func sourceLineOffsets(src []byte) []int {
	offsets := []int{0}
	for i, b := range src {
		if b == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

func lineStartOffset(offsets []int, line int) int {
	if line <= 0 || line > len(offsets) {
		return 0
	}
	return offsets[line-1]
}

func offsetForPosition(src []byte, offsets []int, pos syntax.Position) (int, bool) {
	line := int(pos.Line)
	col := int(pos.Col)
	if line <= 0 || col <= 0 || line > len(offsets) {
		return 0, false
	}
	off := offsets[line-1]
	target := col - 1
	for target > 0 && off < len(src) {
		_, size := utf8.DecodeRune(src[off:])
		if size == 0 {
			return 0, false
		}
		off += size
		target--
	}
	return off, true
}

func applyCoverageEdits(src []byte, edits []coverageEdit) []byte {
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start == edits[j].start {
			return edits[i].end > edits[j].end
		}
		return edits[i].start > edits[j].start
	})
	out := append([]byte(nil), src...)
	for _, e := range edits {
		if e.end == 0 {
			e.end = e.start
		}
		if e.start < 0 || e.start > len(out) || e.end < e.start || e.end > len(out) {
			continue
		}
		next := make([]byte, 0, len(out)+len(e.text)-(e.end-e.start))
		next = append(next, out[:e.start]...)
		next = append(next, e.text...)
		next = append(next, out[e.end:]...)
		out = next
	}
	return out
}

func appendUniqueString(in []string, value string) []string {
	if value == "" {
		return in
	}
	for _, existing := range in {
		if existing == value {
			return in
		}
	}
	return append(in, value)
}
