// migrate_agent.go — implements `kitsoki migrate-agent <story-dir|app.yaml>`.
//
// First performs the terminology migration from oracle → agent across story
// text assets, then walks each app.yaml, finds every invoke:
// host.agent.ask_with_mcp / host.agent.talk call, classifies each one using
// the §6 decision table, and rewrites the invoke: verb in place. Comments and
// formatting outside the rewritten spans are preserved (byte-surgery approach,
// same pattern as kitsoki-migrate-templates).
//
// Classification rules (§6 of agent-split proposal):
//
//  1. chat_id: set → converse
//  2. host.agent.talk → converse (warn; alias already present)
//  3. has agent with mutation tools (Edit/Write/unrestricted Bash) → task
//  4. has validator.post_cmd AND (agent mutation tools OR validator reads like
//     a worker) → task
//  5. prompt "pick from enum" / "parse phrase" heuristic → extract (schema required)
//  6. schema: set, no mutation tools, validator pure-read → decide
//  7. no schema, no mutation tools → ask
//  8. ambiguous (can't determine) → flagged in .migrate-agent.todo
//
// Rewrite modes:
//
//	(default)        in-place YAML rewrite
//	--dry-run        print unified diff to stdout; do not write
//	--print-classified  list each call's classification before rewriting
//
// Ambiguous cases: written to <file>.migrate-agent.todo (Markdown).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/spf13/cobra"
)

// migrateAgentCmd returns the `kitsoki migrate-agent` command.
func migrateAgentCmd() *cobra.Command {
	var (
		dryRun          bool
		printClassified bool
	)

	cmd := &cobra.Command{
		Use:   "migrate-agent <story-dir|app.yaml>",
		Short: "Migrate old oracle story vocabulary to agent vocabulary",
		Long: `Walk <story-dir|app.yaml> and migrate old oracle terminology to agent
terminology: oracle_plugins → agent_plugins, host.oracle.* → host.agent.*,
oracle.* aliases/events → agent.*, and matching prose/fixture references.

It also rewrites every host.agent.ask_with_mcp and host.agent.talk effect to
the appropriate specific verb: extract, decide, ask, task, or converse.

Classification rules (agent-split proposal §6):
  chat_id: set                          → converse
  host.agent.talk                      → converse
  agent with mutation tools             → task
  validator.post_cmd (worker pattern)   → task
  prompt picks from enum / parses phrase → extract
  schema set, no mutation, read-only    → decide
  no schema, no mutation tools          → ask
  ambiguous                             → flagged in <file>.migrate-agent.todo

Flags:
  --dry-run           print unified diff; do not write
  --print-classified  list each call's classification before rewriting

Ambiguous calls are written to a Markdown .migrate-agent.todo file next to
the input YAML for human review.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			return runMigrateAgent(cmd, path, dryRun, printClassified)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print diff without modifying files")
	cmd.Flags().BoolVar(&printClassified, "print-classified", false, "list each call's classification before rewriting")

	return cmd
}

// agentCallClassification names the six output verbs plus an ambiguous bucket.
type agentCallClassification string

const (
	classExtract   agentCallClassification = "extract"
	classDecide    agentCallClassification = "decide"
	classAsk       agentCallClassification = "ask"
	classTask      agentCallClassification = "task"
	classConverse  agentCallClassification = "converse"
	classAmbiguous agentCallClassification = "ambiguous"
)

// agentCallSite records one host.agent.ask_with_mcp or host.agent.talk
// call found in the YAML tree.
type agentCallSite struct {
	// InvokeByteStart / InvokeByteEnd is the byte range of the current verb
	// string value (e.g. "host.agent.ask_with_mcp") in the source. The
	// rewriter replaces this span with the new verb string.
	InvokeByteStart int
	InvokeByteEnd   int

	// Original invoke: value.
	OriginalVerb string

	// Path in the YAML tree for diagnostics.
	Path string

	// Fields extracted from the with: block (best-effort).
	HasChatID         bool
	HasSchema         bool
	HasMutationTool   bool // agent with Edit/Write/unrestricted Bash
	HasValidatorCmd   bool // validator.post_cmd set
	ValidatorIsWorker bool // heuristic: validator cmd doesn't look purely read-only
	PromptIsEnum      bool // heuristic: prompt asks LLM to pick from enum

	Classification  agentCallClassification
	Ambiguous       bool
	AmbiguousReason string

	// WithBlockByteStart / WithBlockByteEnd is the byte range of the entire
	// "with:\n  ..." block (from the 'w' of "with:" to the start of the next
	// sibling at the same indentation). Only populated when the site is
	// classified as task (where the with: block must be restructured).
	WithBlockByteStart int
	WithBlockByteEnd   int

	// InvokeKeyIndent is the 0-based column of the invoke: KEY (= the number
	// of leading spaces). Used to generate properly-indented replacement text.
	InvokeKeyIndent int

	// Parsed with: fields captured for task-shape restructuring.
	// These are raw strings (templates included), suitable for direct YAML emission.
	WithAgent        string
	WithWorkingDir   string
	WithPrompt       string // prompt: or prompt_path:
	WithPromptIsPath bool   // true when the source key was prompt_path:
	WithSchema       string
	WithArgs         []withArgKV // ordered key=value pairs from args:
	WithMaxRetries   string
}

// withArgKV is one key-value pair from the with.args block.
type withArgKV struct {
	Key   string
	Value string
}

// runMigrateAgent processes one app.yaml file (or all app.yaml files under
// a directory, up to two levels deep).
func runMigrateAgent(cmd *cobra.Command, path string, dryRun, printClassified bool) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("migrate-agent: stat %q: %w", path, err)
	}

	if err := migrateAgentTerminology(cmd, path, fi, dryRun); err != nil {
		return err
	}

	var yamlFiles []string
	if fi.IsDir() {
		// Walk up to two directory levels for app.yaml files.
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return fmt.Errorf("migrate-agent: readdir %q: %w", path, readErr)
		}
		for _, e := range entries {
			candidate := filepath.Join(path, e.Name(), "app.yaml")
			if _, serr := os.Stat(candidate); serr == nil {
				yamlFiles = append(yamlFiles, candidate)
			}
			if e.IsDir() {
				sub := filepath.Join(path, e.Name())
				subs, _ := os.ReadDir(sub)
				for _, se := range subs {
					c2 := filepath.Join(sub, se.Name(), "app.yaml")
					if _, serr := os.Stat(c2); serr == nil {
						yamlFiles = append(yamlFiles, c2)
					}
				}
			}
		}
		if len(yamlFiles) == 0 {
			// Check if an app.yaml lives directly in the directory.
			direct := filepath.Join(path, "app.yaml")
			if _, serr := os.Stat(direct); serr == nil {
				yamlFiles = append(yamlFiles, direct)
			}
		}
	} else {
		yamlFiles = []string{path}
	}

	if len(yamlFiles) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "migrate-agent: no app.yaml files found under %q\n", path)
		return nil
	}

	for _, f := range yamlFiles {
		if err := migrateAgentFile(cmd, f, dryRun, printClassified); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "migrate-agent: %s: %v\n", f, err)
		}
	}
	return nil
}

func migrateAgentTerminology(cmd *cobra.Command, path string, fi os.FileInfo, dryRun bool) error {
	var files []string
	if fi.IsDir() {
		err := filepath.WalkDir(path, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", ".worktrees", ".claude", ".artifacts", ".context", "node_modules", "dist":
					return filepath.SkipDir
				}
				return nil
			}
			if isAgentMigrationTextFile(p) {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("migrate-agent: walk %q: %w", path, err)
		}
	} else if isAgentMigrationTextFile(path) {
		files = []string{path}
	}

	changed := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("migrate-agent: read %q: %w", f, err)
		}
		next := rewriteAgentTerminology(src)
		if string(next) == string(src) {
			continue
		}
		changed++
		if dryRun {
			printUnifiedDiff(cmd, f, src, next)
			continue
		}
		if err := os.WriteFile(f, next, 0644); err != nil {
			return fmt.Errorf("migrate-agent: write %q: %w", f, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "migrate-agent: renamed oracle terminology in %s\n", f)
	}
	if changed > 0 && dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "migrate-agent: %d file(s) would rename oracle terminology\n", changed)
	}
	return nil
}

func isAgentMigrationTextFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".md", ".json", ".jsonl", ".pongo", ".tmpl", ".txt", ".star", ".ts", ".tsx", ".js", ".vue":
		return true
	default:
		return false
	}
}

func rewriteAgentTerminology(src []byte) []byte {
	out := strings.ReplaceAll(string(src), "ORACLE", "AGENT")
	out = strings.ReplaceAll(out, "Oracle", "Agent")
	out = strings.ReplaceAll(out, "oracle", "agent")
	return []byte(out)
}

// migrateAgentFile processes one YAML file.
func migrateAgentFile(cmd *cobra.Command, path string, dryRun, printClassified bool) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}

	sites, err := findAgentCallSites(path, src)
	if err != nil {
		return err
	}
	if len(sites) == 0 {
		return nil
	}

	// Classify each site.
	for i := range sites {
		classifyAgentCallSite(&sites[i])
	}

	if printClassified {
		for _, s := range sites {
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s → %s", path, s.OriginalVerb, string(s.Classification))
			if s.Ambiguous {
				fmt.Fprintf(cmd.OutOrStdout(), " (AMBIGUOUS: %s)", s.AmbiguousReason)
			}
			fmt.Fprintln(cmd.OutOrStdout())
		}
	}

	// Collect ambiguous calls for the TODO file.
	var ambiguousSites []agentCallSite
	for _, s := range sites {
		if s.Ambiguous {
			ambiguousSites = append(ambiguousSites, s)
		}
	}

	// Perform the rewrite (byte surgery, descending order so offsets stay valid).
	newSrc := make([]byte, len(src))
	copy(newSrc, src)

	// Build a flat list of byte-range edits, sorted from last to first.
	// Each edit is (start, end, replacement). For a task site two edits are
	// emitted: one for the with: block restructure and one for the verb string
	// — the with: block edit has a higher byte offset and must come first.
	type byteEdit struct {
		start       int
		end         int
		replacement []byte
	}
	var edits []byteEdit
	for _, s := range sites {
		if s.Ambiguous && s.Classification == classAmbiguous {
			continue
		}
		newVerb := newAgentVerb(s.OriginalVerb, s.Classification)
		if newVerb == "" {
			continue
		}
		// Verb replacement.
		edits = append(edits, byteEdit{s.InvokeByteStart, s.InvokeByteEnd, []byte(newVerb)})
		// with: block restructure — only for task classification.
		if s.Classification == classTask && s.WithBlockByteStart > 0 && s.WithBlockByteEnd > s.WithBlockByteStart {
			newWith := buildTaskWithBlock(s)
			edits = append(edits, byteEdit{s.WithBlockByteStart, s.WithBlockByteEnd, []byte(newWith)})
		}
	}
	// Sort descending by start offset (stable).
	for i := 0; i < len(edits); i++ {
		for j := i + 1; j < len(edits); j++ {
			if edits[j].start > edits[i].start {
				edits[i], edits[j] = edits[j], edits[i]
			}
		}
	}
	// Apply in descending order so earlier offsets stay valid.
	for _, e := range edits {
		newSrc = append(
			append([]byte{}, newSrc[:e.start]...),
			append(e.replacement, newSrc[e.end:]...)...,
		)
	}

	if dryRun {
		printUnifiedDiff(cmd, path, src, newSrc)
	} else if string(newSrc) != string(src) {
		if err := os.WriteFile(path, newSrc, 0644); err != nil {
			return fmt.Errorf("write %q: %w", path, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "migrate-agent: rewrote %s (%d call(s))\n", path, len(sites)-len(ambiguousSites))
	}

	// Write the .migrate-agent.todo file.
	if len(ambiguousSites) > 0 {
		todoPath := path + ".migrate-agent.todo"
		if err := writeMigrateAgentTodo(todoPath, path, ambiguousSites); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "migrate-agent: write todo %q: %v\n", todoPath, err)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "migrate-agent: wrote %s (%d ambiguous call(s) require review)\n", todoPath, len(ambiguousSites))
		}
	}

	return nil
}

// findAgentCallSites parses the YAML and locates every invoke:
// host.agent.ask_with_mcp / host.agent.talk call. It also recognizes the
// pre-rename host.oracle.* spellings so --dry-run can classify a story before
// the terminology rewrite is written to disk.
func findAgentCallSites(name string, src []byte) ([]agentCallSite, error) {
	f, err := parser.ParseBytes(src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("%s: parse YAML: %w", name, err)
	}
	lineIdx := buildAgentLineIndex(src)
	var sites []agentCallSite
	for _, doc := range f.Docs {
		walkAgentAST(doc, "", src, lineIdx, &sites)
	}
	return sites, nil
}

// buildAgentLineIndex builds a byte-offset → line start index.
func buildAgentLineIndex(src []byte) []int {
	idx := []int{0}
	for i, b := range src {
		if b == '\n' {
			idx = append(idx, i+1)
		}
	}
	return idx
}

// lineColToByteOffset converts 1-indexed (line, col) to a byte offset.
func lineColToByteOffset(lineIdx []int, line, col int) int {
	if line < 1 || line > len(lineIdx) {
		return -1
	}
	off := lineIdx[line-1] + col - 1
	if off < 0 || off > 1<<30 {
		return -1
	}
	return off
}

// walkAgentAST visits the AST tree and records every agent call site.
func walkAgentAST(n ast.Node, path string, src []byte, lineIdx []int, sites *[]agentCallSite) {
	walkAgentASTWithParent(n, nil, path, src, lineIdx, sites)
}

// walkAgentASTWithParent is the recursive implementation; parent is the
// enclosing *ast.MappingNode when n is a *ast.MappingValueNode, so the
// site extractor can walk siblings for the with: block.
func walkAgentASTWithParent(n ast.Node, parent *ast.MappingNode, path string, src []byte, lineIdx []int, sites *[]agentCallSite) {
	if n == nil {
		return
	}
	switch v := n.(type) {
	case *ast.DocumentNode:
		if v.Body != nil {
			walkAgentASTWithParent(v.Body, nil, path, src, lineIdx, sites)
		}
	case *ast.MappingNode:
		for _, mv := range v.Values {
			walkAgentASTWithParent(mv, v, path, src, lineIdx, sites)
		}
	case *ast.MappingValueNode:
		key := agentMapKeyString(v.Key)
		childPath := path + "/" + key
		if key == "invoke" {
			if sv, ok := v.Value.(*ast.StringNode); ok {
				verb := sv.Value
				if verb == "host.agent.ask_with_mcp" || verb == "host.agent.talk" ||
					verb == "host.oracle.ask_with_mcp" || verb == "host.oracle.talk" {
					site := extractAgentCallSite(path, verb, v, parent, src, lineIdx)
					if site != nil {
						*sites = append(*sites, *site)
					}
					return
				}
			}
		}
		walkAgentASTWithParent(v.Value, nil, childPath, src, lineIdx, sites)
	case *ast.SequenceNode:
		for i, item := range v.Values {
			walkAgentASTWithParent(item, nil, fmt.Sprintf("%s[%d]", path, i), src, lineIdx, sites)
		}
	}
}

// extractAgentCallSite builds an agentCallSite from the mapping value node
// that contains the invoke: key. parentMapping is the enclosing mapping node;
// it is used to walk the with: block for classification signals.
func extractAgentCallSite(path, verb string, invokeNode *ast.MappingValueNode, parentMapping *ast.MappingNode, src []byte, lineIdx []int) *agentCallSite {
	// Find the byte range of the verb string value.
	// L5: use token.Origin for precise byte range instead of computing offsets
	// from Position.Column manually. Origin contains the raw source text
	// (including surrounding quotes) so we can place the rewrite boundary
	// precisely without depending on Position column-counting semantics.
	sv, ok := invokeNode.Value.(*ast.StringNode)
	if !ok {
		return nil
	}
	valTok := sv.GetToken()
	if valTok == nil {
		return nil
	}

	// Use Position.Line/Column (1-indexed) to derive the 0-based byte offset.
	// Position.Offset is unreliable: goccy/go-yaml scanner initialises
	// s.offset = 1 (scanner.go:1504), making it 1-indexed in the common case,
	// but comments alter the running offset differently, producing values that
	// are neither 0-based nor consistently 1-based. lineColToByteOffset uses
	// only the pre-built newline index and is always correct.
	byteOff := lineColToByteOffset(lineIdx, valTok.Position.Line, valTok.Position.Column)
	if byteOff < 0 {
		return nil
	}
	start := byteOff
	// Advance past a leading quote if present.
	if start < len(src) && (src[start] == '"' || src[start] == '\'') {
		start++
	}
	end := start + len(verb)
	if end > len(src) {
		end = len(src)
	}

	keyTok := invokeNode.Key.GetToken()
	invokeKeyIndent := 0
	if keyTok != nil {
		invokeKeyIndent = keyTok.Position.Column - 1
	}

	site := &agentCallSite{
		InvokeByteStart: start,
		InvokeByteEnd:   end,
		OriginalVerb:    verb,
		Path:            path,
		InvokeKeyIndent: invokeKeyIndent,
	}

	// Walk the sibling with: block using AST traversal (M10).
	if parentMapping != nil {
		populateSiteFromWithBlock(site, parentMapping)
	}

	// Capture the with: block byte range and parsed fields for task restructuring.
	if keyTok != nil {
		invokeLineNo := keyTok.Position.Line - 1 // 0-indexed
		extractWithBlockForTask(src, lineIdx, invokeKeyIndent, invokeLineNo, site)
	}

	return site
}

// populateSiteFromWithBlock finds the with: sibling in parentMapping and
// extracts classification signals via AST traversal (not text heuristics).
// This handles both block-style (- Edit) and flow-style ([Edit, Write]) tool
// lists and avoids false positives from comments or unrelated string values.
func populateSiteFromWithBlock(site *agentCallSite, parentMapping *ast.MappingNode) {
	var withNode ast.Node
	for _, mv := range parentMapping.Values {
		switch agentMapKeyString(mv.Key) {
		case "with":
			withNode = mv.Value
		}
	}

	if withNode == nil {
		return
	}

	// withNode is the mapping under with:
	withMapping, ok := withNode.(*ast.MappingNode)
	if !ok {
		return
	}

	for _, wv := range withMapping.Values {
		k := agentMapKeyString(wv.Key)
		switch k {
		case "chat_id":
			if !isNullNode(wv.Value) {
				site.HasChatID = true
			}
		case "schema":
			if !isNullNode(wv.Value) {
				site.HasSchema = true
			}
		case "tools":
			// M10: Use AST-based tool list traversal. Works for both
			//   block-style:   - Edit
			//   flow-style:    [Edit, Write]
			// AliasNode (*<anchor>) is noted as unresolvable; we emit a TODO.
			tools := extractToolNames(wv.Value)
			for _, t := range tools {
				if isMutationToolName(t) {
					site.HasMutationTool = true
				}
			}
		case "agent":
			// Agent-level tools are in the agents: block, not inline with:.
			// We can't resolve them here without the full app YAML context.
			// Handled as ambiguous below if needed.
		}
		// Walk into nested mappings for validator.post_cmd.
		if k == "validator" {
			if vm, vmOK := wv.Value.(*ast.MappingNode); vmOK {
				for _, vv := range vm.Values {
					if agentMapKeyString(vv.Key) == "post_cmd" && !isNullNode(vv.Value) {
						site.HasValidatorCmd = true
						site.ValidatorIsWorker = isWorkerValidatorNode(vv.Value)
					}
				}
			}
		}
	}

	// Prompt-is-enum heuristic: still text-based but scoped to the prompt: value
	// string only, not the entire with: block.
	for _, wv := range withMapping.Values {
		if agentMapKeyString(wv.Key) == "prompt" || agentMapKeyString(wv.Key) == "prompt_path" {
			if sv, svOK := wv.Value.(*ast.StringNode); svOK {
				site.PromptIsEnum = isEnumPromptHint(sv.Value)
			}
		}
	}
}

// extractWithBlockForTask scans the lines after the invoke: line at the same
// indentation to find the "with:" sibling block. It records its byte range in
// site.WithBlockByteStart/End and parses the known sub-keys (agent, working_dir,
// prompt, prompt_path, schema, args, max_retries) into site.With* fields.
//
// Only the fields needed for the task-shape rewrite are extracted; unknown keys
// are silently ignored (the caller retains the original with: block for those).
func extractWithBlockForTask(src []byte, lineIdx []int, invokeKeyIndent, invokeLineNo int, site *agentCallSite) {
	// Scan forward from the line after invoke: to find "with:" at the same indent.
	withLineNo := -1
	for lineNo := invokeLineNo + 1; lineNo < len(lineIdx); lineNo++ {
		lineStart := lineIdx[lineNo]
		lineEnd := len(src)
		if lineNo+1 < len(lineIdx) {
			lineEnd = lineIdx[lineNo+1]
		}
		trimmed := strings.TrimRight(string(src[lineStart:lineEnd]), "\n\r")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		indent := countLeadingSpaces(trimmed)
		if indent < invokeKeyIndent {
			break // left the parent mapping
		}
		if indent == invokeKeyIndent && strings.HasPrefix(strings.TrimSpace(trimmed), "with:") {
			withLineNo = lineNo
			break
		}
	}
	if withLineNo < 0 {
		return
	}

	// with: block starts at the beginning of its line.
	site.WithBlockByteStart = lineIdx[withLineNo]

	// The with: block ends at the first line that is at the SAME indent as
	// invoke: (= invokeKeyIndent) or shallower — i.e. the next sibling key.
	site.WithBlockByteEnd = len(src) // default: end of file
	for lineNo := withLineNo + 1; lineNo < len(lineIdx); lineNo++ {
		lineStart := lineIdx[lineNo]
		lineEnd := len(src)
		if lineNo+1 < len(lineIdx) {
			lineEnd = lineIdx[lineNo+1]
		}
		trimmed := strings.TrimRight(string(src[lineStart:lineEnd]), "\n\r")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		indent := countLeadingSpaces(trimmed)
		if indent <= invokeKeyIndent {
			site.WithBlockByteEnd = lineStart
			break
		}
	}

	// Parse the with: block lines to extract known sub-keys.
	//
	// Indentation is inferred from the actual file: the first non-blank line
	// after "with:" tells us the child indent (depth1Indent). The args: entries
	// will be one level deeper (depth2Indent, inferred from the first arg line).
	//
	// Using exact indent comparison instead of heuristic <= invokeKeyIndent+N
	// avoids mis-classifying args children as with: children or vice-versa.
	depth1Indent := -1 // indent level of direct children of with:
	depth2Indent := -1 // indent level of children of args:
	inArgs := false

	for lineNo := withLineNo + 1; lineNo < len(lineIdx); lineNo++ {
		lineStart := lineIdx[lineNo]
		lineEnd := len(src)
		if lineNo+1 < len(lineIdx) {
			lineEnd = lineIdx[lineNo+1]
		}
		trimmed := strings.TrimRight(string(src[lineStart:lineEnd]), "\n\r")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		indent := countLeadingSpaces(trimmed)
		if indent <= invokeKeyIndent {
			break // end of with: block
		}

		// Infer depth1Indent from the first content line.
		if depth1Indent < 0 {
			depth1Indent = indent
		}

		content := strings.TrimSpace(trimmed)

		if indent == depth1Indent {
			// Direct child of with: — reset args mode.
			inArgs = false

			if !strings.Contains(content, ":") {
				continue
			}
			kv := strings.SplitN(content, ":", 2)
			k := strings.TrimSpace(kv[0])
			v := ""
			if len(kv) == 2 {
				v = strings.TrimSpace(kv[1])
			}

			switch k {
			case "agent":
				site.WithAgent = v
			case "working_dir":
				site.WithWorkingDir = v
			case "prompt":
				site.WithPrompt = v
				site.WithPromptIsPath = false
			case "prompt_path":
				site.WithPrompt = v
				site.WithPromptIsPath = true
			case "schema":
				site.WithSchema = v
			case "max_retries":
				site.WithMaxRetries = v
			case "args":
				inArgs = true
			}
			continue
		}

		// Deeper than depth1Indent: only care about args children.
		if inArgs && strings.Contains(content, ":") {
			// Infer depth2Indent from the first arg line.
			if depth2Indent < 0 {
				depth2Indent = indent
			}
			// Only parse lines at the args child depth (not deeper nested values).
			if indent == depth2Indent {
				kv := strings.SplitN(content, ":", 2)
				if len(kv) == 2 {
					k := strings.TrimSpace(kv[0])
					v := strings.TrimSpace(kv[1])
					site.WithArgs = append(site.WithArgs, withArgKV{k, v})
				}
			}
		}
	}
}

// countLeadingSpaces returns the number of leading space characters in s.
func countLeadingSpaces(s string) int {
	n := 0
	for _, c := range s {
		if c == ' ' {
			n++
		} else {
			break
		}
	}
	return n
}

// buildTaskWithBlock generates the new with: block for a task site.
// The indentation base is site.InvokeKeyIndent spaces.
//
// Output shape:
//
//	with:
//	  agent: <agent>
//	  working_dir: <working_dir>   # if present
//	  acceptance:
//	    schema: <schema>
//	  context:
//	    prompt: <prompt>           # or prompt_path:
//	    args:                      # if any args
//	      key: value
//	      ...
func buildTaskWithBlock(s agentCallSite) string {
	base := strings.Repeat(" ", s.InvokeKeyIndent)
	d1 := base + "  "     // depth 1 under with:
	d2 := base + "    "   // depth 2 under acceptance: / context:
	d3 := base + "      " // depth 3 under args:

	var sb strings.Builder
	sb.WriteString(base + "with:\n")

	if s.WithAgent != "" {
		sb.WriteString(d1 + "agent: " + s.WithAgent + "\n")
	}
	if s.WithWorkingDir != "" {
		sb.WriteString(d1 + "working_dir: " + s.WithWorkingDir + "\n")
	}

	// acceptance: block (always present for task).
	sb.WriteString(d1 + "acceptance:\n")
	if s.WithSchema != "" {
		sb.WriteString(d2 + "schema: " + s.WithSchema + "\n")
	}

	// context: block (always present; holds prompt and args).
	sb.WriteString(d1 + "context:\n")
	if s.WithPrompt != "" {
		promptKey := "prompt"
		if s.WithPromptIsPath {
			promptKey = "prompt_path"
		}
		sb.WriteString(d2 + promptKey + ": " + s.WithPrompt + "\n")
	}
	if len(s.WithArgs) > 0 {
		sb.WriteString(d2 + "args:\n")
		for _, kv := range s.WithArgs {
			sb.WriteString(d3 + kv.Key + ": " + kv.Value + "\n")
		}
	}

	return sb.String()
}

// extractToolNames walks an AST node that represents the value of a tools:
// field and returns the list of tool name strings. Handles SequenceNode
// (block and flow style) and single StringNode. AliasNodes (anchor refs) are
// not followed; the caller receives a TODO marker for those.
func extractToolNames(n ast.Node) []string {
	if n == nil {
		return nil
	}
	switch v := n.(type) {
	case *ast.SequenceNode:
		var names []string
		for _, item := range v.Values {
			if s, ok := item.(*ast.StringNode); ok {
				names = append(names, s.Value)
			}
			// AliasNodes: anchor refs (*shared) can't be resolved statically
			// without the full doc context. Skip silently; the call site may
			// be classified incorrectly but the TODO file will flag it.
		}
		return names
	case *ast.StringNode:
		return []string{v.Value}
	case *ast.AnchorNode:
		// The anchor's value node contains the actual sequence.
		return extractToolNames(v.Value)
	}
	return nil
}

// isMutationToolName reports whether name is a mutation tool (Edit, Write,
// NotebookEdit). Case-sensitive per the spec.
func isMutationToolName(name string) bool {
	switch name {
	case "Edit", "Write", "NotebookEdit":
		return true
	}
	return false
}

// isNullNode reports whether n represents a YAML null value.
func isNullNode(n ast.Node) bool {
	if n == nil {
		return true
	}
	_, isNull := n.(*ast.NullNode)
	return isNull
}

// isWorkerValidatorNode reports whether the post_cmd value looks like a worker
// (mutating) command by checking the string content.
func isWorkerValidatorNode(n ast.Node) bool {
	if n == nil {
		return false
	}
	sv, ok := n.(*ast.StringNode)
	if !ok {
		return false
	}
	return isWorkerValidatorHint(sv.Value)
}

// isWorkerValidatorHint heuristically determines if a validator looks like a
// worker (makes changes) rather than a pure verifier. We look for keywords
// that suggest mutation: git commit, git push, write, create, apply, patch.
func isWorkerValidatorHint(withBlock string) bool {
	lower := strings.ToLower(withBlock)
	for _, keyword := range []string{
		"git commit", "git push", "git apply",
		"write", "create", "apply patch", "modify",
	} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// isEnumPromptHint heuristically detects "pick from enum" style prompts.
func isEnumPromptHint(withBlock string) bool {
	lower := strings.ToLower(withBlock)
	return strings.Contains(lower, "pick one") ||
		strings.Contains(lower, "choose one") ||
		strings.Contains(lower, "select one") ||
		strings.Contains(lower, "one of the following") ||
		strings.Contains(lower, "one of:") ||
		strings.Contains(lower, "classify") ||
		strings.Contains(lower, "parse this") ||
		strings.Contains(lower, "extract the")
}

// classifyAgentCallSite applies the §6 decision table.
func classifyAgentCallSite(s *agentCallSite) {
	// Rule 1: host.agent.talk → converse (warn; alias already present from Phase 7)
	if s.OriginalVerb == "host.agent.talk" || s.OriginalVerb == "host.oracle.talk" {
		s.Classification = classConverse
		return
	}

	// Rule 2: chat_id: set → converse
	if s.HasChatID {
		s.Classification = classConverse
		return
	}

	// Rule 3: agent has mutation tools → task
	if s.HasMutationTool {
		s.Classification = classTask
		return
	}

	// Rule 4: validator.post_cmd that looks like a worker → task
	if s.ValidatorIsWorker {
		s.Classification = classTask
		return
	}

	// Rule 5: prompt picks from enum / parses phrase → extract
	if s.PromptIsEnum {
		if s.HasSchema {
			s.Classification = classExtract
		} else {
			// No schema — can't use extract; ambiguous
			s.Ambiguous = true
			s.AmbiguousReason = "prompt looks like enum extraction but no schema: is set; add schema: to use extract"
			s.Classification = classAmbiguous
		}
		return
	}

	// Rule 6: schema set, no mutation, validator present → decide with validator
	// Rule 6b: schema set, no mutation, no validator → decide
	if s.HasSchema {
		s.Classification = classDecide
		return
	}

	// Rule 7: no schema, no mutation tools, has validator.post_cmd → ambiguous
	// (read-only validator without schema is odd — probably decide with a schema not yet declared)
	if s.HasValidatorCmd && !s.HasSchema {
		s.Ambiguous = true
		s.AmbiguousReason = "validator.post_cmd present but no schema:; add schema: to classify as decide, or remove validator to classify as ask"
		s.Classification = classAmbiguous
		return
	}

	// Rule 8: no schema, no mutation → ask
	s.Classification = classAsk
}

// newAgentVerb maps an original verb and a classification to the new verb string.
func newAgentVerb(originalVerb string, cls agentCallClassification) string {
	if cls == classAmbiguous {
		return ""
	}
	return "host.agent." + string(cls)
}

// writeMigrateAgentTodo writes a Markdown file listing ambiguous calls.
func writeMigrateAgentTodo(todoPath, yamlPath string, sites []agentCallSite) error {
	var sb strings.Builder
	sb.WriteString("# migrate-agent: calls requiring human review\n\n")
	sb.WriteString("Generated by `kitsoki migrate-agent`. Each entry below is a call that\n")
	sb.WriteString("could not be automatically classified. Review each one and update the\n")
	sb.WriteString("invoke: verb manually.\n\n")
	sb.WriteString("Source: `" + yamlPath + "`\n\n")

	for i, s := range sites {
		sb.WriteString(fmt.Sprintf("## Call %d at path `%s`\n\n", i+1, s.Path))
		sb.WriteString(fmt.Sprintf("- **Original verb:** `%s`\n", s.OriginalVerb))
		sb.WriteString(fmt.Sprintf("- **Reason:** %s\n", s.AmbiguousReason))
		sb.WriteString("- **Options:**\n")
		sb.WriteString("  - Change to `invoke: host.agent.decide` and add a `schema:` field.\n")
		sb.WriteString("  - Change to `invoke: host.agent.ask` if the output is prose-only.\n")
		sb.WriteString("  - Change to `invoke: host.agent.task` if the agent mutates files.\n")
		sb.WriteString("\n")
	}

	return os.WriteFile(todoPath, []byte(sb.String()), 0644)
}

// printUnifiedDiff writes a minimal unified diff of old vs new bytes to cmd.OutOrStdout.
func printUnifiedDiff(cmd *cobra.Command, path string, old, newBytes []byte) {
	oldLines := strings.Split(string(old), "\n")
	newLines := strings.Split(string(newBytes), "\n")
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "--- a/%s\n", path)
	fmt.Fprintf(w, "+++ b/%s\n", path)

	// Simple line-by-line diff (no LCS — adequate for short YAML files with
	// only verb substitutions). Two-pass: find changed lines and emit them
	// with ±3 lines of context.
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}
	inHunk := false
	for i := 0; i < maxLen; i++ {
		oldLine := ""
		newLine := ""
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if !inHunk {
				fmt.Fprintf(w, "@@ -%d +%d @@ (context omitted)\n", i+1, i+1)
				inHunk = true
			}
			if i < len(oldLines) {
				fmt.Fprintf(w, "-%s\n", oldLine)
			}
			if i < len(newLines) {
				fmt.Fprintf(w, "+%s\n", newLine)
			}
		} else {
			inHunk = false
		}
	}
}

// agentMapKeyString converts an AST map key to a string.
func agentMapKeyString(k ast.MapKeyNode) string {
	if k == nil {
		return ""
	}
	if s, ok := k.(*ast.StringNode); ok {
		return s.Value
	}
	return strings.TrimSpace(k.String())
}
