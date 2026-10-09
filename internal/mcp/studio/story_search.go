package studio

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// story_search.go — discover and grep a story's files over MCP.
//
// story.read reads ONE file by exact path; there was no way to learn what files
// a story has, nor to grep across them (find a room ref, an intent name, a
// host.* call, a world key). That forced `ls`/`find`/`grep` over stories/ — the
// exact Read/Grep the MCP-driver agent is told never to use. story.list and
// story.search close that: both are read-only, workspace-scoped, and never make
// an LLM call.

// defaultStorySearchMax caps story.search hits so a broad pattern can't return a
// multi-megabyte payload. A caller raises it with {max}.
const defaultStorySearchMax = 200

// storyListMaxFiles caps story.list so an accidentally-huge tree (e.g. a story
// dir with a vendored artifacts subdir) can't overflow the payload.
const storyListMaxFiles = 2000

// registerStorySearchTools wires story.list and story.search. Both are pure
// reads, so they stay available on a read-only server.
func (srv *Server) registerStorySearchTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "story.list",
		Description: "List a story's files — the discovery counterpart to story.read (which needs an exact path). {dir? (story dir or app.yaml; defaults to the bound workspace), glob? (filter on the workspace-relative path, e.g. \"rooms/*.yaml\" or \"flows/*\")}. Returns {ok, dir, files[]} where files are workspace-relative paths, sorted. Read-only.",
	}, srv.handleStoryList)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "story.search",
		Description: "Grep across a story's files — find a room ref, intent name, host.* call, world key, prompt phrase — without shelling out. {dir? (defaults to the bound workspace), pattern (required; a literal substring unless regex:true), glob? (restrict to matching workspace-relative paths), regex? (treat pattern as RE2), ignore_case?, max? (cap hits; default 200)}. Returns {ok, hits[{file, line, text}], truncated?}. Read-only, no LLM.",
	}, srv.handleStorySearch)
}

// ── story.list ────────────────────────────────────────────────────────────────

// StoryListArgs is the input to story.list.
type StoryListArgs struct {
	Dir  string `json:"dir,omitempty"`
	Glob string `json:"glob,omitempty"`
}

// StoryListOK is the story.list result.
type StoryListOK struct {
	OK        bool     `json:"ok"`
	Dir       string   `json:"dir"`
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated,omitempty"`
}

// handleStoryList walks the story dir and returns the workspace-relative paths
// of its files, optionally filtered by a glob on the relative path.
func (srv *Server) handleStoryList(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args StoryListArgs,
) (*mcpsdk.CallToolResult, any, error) {
	storyDir, _, rerr := srv.resolveWorkspace(args.Dir)
	if rerr != nil {
		return rerr, nil, nil
	}
	files, truncated, err := walkStoryFiles(storyDir, args.Glob, storyListMaxFiles)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("story.list: %v", err)), nil, nil
	}
	return nil, StoryListOK{OK: true, Dir: storyDir, Files: files, Truncated: truncated}, nil
}

// ── story.search ──────────────────────────────────────────────────────────────

// StorySearchArgs is the input to story.search.
type StorySearchArgs struct {
	Dir        string `json:"dir,omitempty"`
	Pattern    string `json:"pattern"`
	Glob       string `json:"glob,omitempty"`
	Regex      bool   `json:"regex,omitempty"`
	IgnoreCase bool   `json:"ignore_case,omitempty"`
	Max        int    `json:"max,omitempty"`
}

// StorySearchOK is the story.search result.
type StorySearchOK struct {
	OK        bool             `json:"ok"`
	Hits      []StorySearchHit `json:"hits"`
	Truncated bool             `json:"truncated,omitempty"`
}

// StorySearchHit is one matching line: its workspace-relative file, 1-based line
// number, and the (trimmed) line text.
type StorySearchHit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// handleStorySearch greps the story's files for pattern. The pattern is a
// literal substring unless regex is set; ignore_case lowercases both sides
// (literal) or compiles with (?i) (regex).
func (srv *Server) handleStorySearch(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args StorySearchArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Pattern == "" {
		return buildToolError(ErrBadRequest, "story.search: pattern is required"), nil, nil
	}
	storyDir, _, rerr := srv.resolveWorkspace(args.Dir)
	if rerr != nil {
		return rerr, nil, nil
	}

	var re *regexp.Regexp
	if args.Regex {
		expr := args.Pattern
		if args.IgnoreCase {
			expr = "(?i)" + expr
		}
		compiled, err := regexp.Compile(expr)
		if err != nil {
			return buildToolError(ErrBadRequest, fmt.Sprintf("story.search: bad regex: %v", err)), nil, nil
		}
		re = compiled
	}
	needle := args.Pattern
	if args.IgnoreCase && !args.Regex {
		needle = strings.ToLower(needle)
	}

	max := args.Max
	if max <= 0 {
		max = defaultStorySearchMax
	}

	files, _, err := walkStoryFiles(storyDir, args.Glob, storyListMaxFiles)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("story.search: %v", err)), nil, nil
	}

	out := StorySearchOK{OK: true, Hits: []StorySearchHit{}}
	for _, rel := range files {
		data, rerr := os.ReadFile(filepath.Join(storyDir, rel))
		if rerr != nil {
			continue // a vanished/binary file is not a search failure
		}
		for i, line := range strings.Split(string(data), "\n") {
			matched := false
			if re != nil {
				matched = re.MatchString(line)
			} else if args.IgnoreCase {
				matched = strings.Contains(strings.ToLower(line), needle)
			} else {
				matched = strings.Contains(line, needle)
			}
			if !matched {
				continue
			}
			if len(out.Hits) >= max {
				out.Truncated = true
				return nil, out, nil
			}
			out.Hits = append(out.Hits, StorySearchHit{File: rel, Line: i + 1, Text: strings.TrimSpace(line)})
		}
	}
	return nil, out, nil
}

// ── shared file walk ──────────────────────────────────────────────────────────

// walkStoryFiles returns the workspace-relative paths of regular files under
// storyDir, sorted, optionally filtered by a glob on the relative path. The walk
// skips dot-directories (.git, .artifacts) since they are never story source.
// truncated is true when the cap was hit.
func walkStoryFiles(storyDir, glob string, maxFiles int) (files []string, truncated bool, err error) {
	absRoot, err := filepath.Abs(storyDir)
	if err != nil {
		return nil, false, fmt.Errorf("resolve dir: %w", err)
	}
	if info, statErr := os.Stat(absRoot); statErr != nil || !info.IsDir() {
		return nil, false, fmt.Errorf("dir %q is not an accessible directory", storyDir)
	}
	walkErr := filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if d.IsDir() {
			if p != absRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(absRoot, p)
		if relErr != nil {
			return nil
		}
		if glob != "" {
			if ok, _ := filepath.Match(glob, rel); !ok {
				// Also allow matching just the basename (e.g. glob "*.yaml").
				if ok2, _ := filepath.Match(glob, filepath.Base(rel)); !ok2 {
					return nil
				}
			}
		}
		if len(files) >= maxFiles {
			truncated = true
			return filepath.SkipAll
		}
		files = append(files, rel)
		return nil
	})
	if walkErr != nil {
		return nil, false, walkErr
	}
	sort.Strings(files)
	return files, truncated, nil
}
