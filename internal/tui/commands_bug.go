package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"kitsoki/internal/app"
	"kitsoki/internal/bugfile"
	"kitsoki/internal/bugprivacy"
	"kitsoki/internal/bugreport"
	"kitsoki/internal/host"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/reportmeta"
	"kitsoki/internal/tui/blocks"
)

// BugCommand implements `/bug [description]` for the TUI.
//
// With no ticket repo configured it writes a local artifact ticket under
// .artifacts/issues/bugs/. With a ticket repo configured it files through
// host.GitHubFileBug with UploadArtifacts=true, so the TUI transcript and
// context evidence follow the same uploaded-evidence path as web bug reports.
type BugCommand struct{}

func (BugCommand) Name() string { return "/bug" }

type bugCommandDoneMsg struct {
	body string
}

func bugCommandCmd(m RootModel, args []string) tea.Cmd {
	args = append([]string(nil), args...)
	return func() tea.Msg {
		body, _, _ := BugCommand{}.Run(m, args)
		return bugCommandDoneMsg{body: body}
	}
}

func (BugCommand) Run(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	desc := strings.TrimSpace(strings.Join(args, " "))
	scrubbedDesc := bugreport.ScrubText(desc)
	title := tuiBugTitle(scrubbedDesc)
	body := tuiBugBody(m, scrubbedDesc)

	root, err := m.resolveBugRoot()
	if err != nil {
		return m.bugBlock(fmt.Sprintf("could not resolve target root: %v", err)), m, nil
	}
	localRoot, localDisplayPrefix := tuiLocalBugFilingRoot(root)
	privacyRoot := root
	privacyDisplayPrefix := ""
	if strings.TrimSpace(m.bugTicketRepo) == "" {
		privacyRoot = localRoot
		privacyDisplayPrefix = localDisplayPrefix
	}

	checker := m.bugPrivacyCheckerForCommand()
	traceJSON := m.depersonalizedBugTraceJSON()
	artifacts := m.bugArtifacts(reportmeta.Snapshot{}, traceJSON)
	safeReport, privacy, perr := bugprivacy.Check(context.Background(), checker, bugprivacy.Report{
		Surface:       "tui",
		Target:        "kitsoki",
		Title:         title,
		Body:          body,
		ReproSteps:    nil,
		Component:     "tui",
		TraceRef:      bugreport.ScrubText(m.traceFilePath),
		ArtifactNames: bugreport.ArtifactNames(artifacts),
	}, bugreport.ScrubOptions(), privacyRoot, os.Getenv("USER"))
	if perr != nil {
		return m.bugBlock(fmt.Sprintf("privacy check failed: %v", perr)), m, nil
	}
	privacy = prefixTUIPrivacyFollowUp(privacy, privacyDisplayPrefix)
	if privacy.Blocked() {
		return m.bugBlock(privacy.Message + bugreport.PrivacyFollowUpSuffix(privacy)), m, nil
	}
	title = safeReport.Title
	body = safeReport.Body

	if strings.TrimSpace(m.bugTicketRepo) != "" {
		msg := m.fileGitHubBug(root, title, body, traceJSON)
		return m.bugBlock(msg + " (" + bugreport.PrivacyCommandStatus(privacy) + ")"), m, nil
	}
	runtime := reportmeta.Capture(root, m.appDefForBug())

	id, relPath, absPath, err := bugfile.Create(bugfile.CreateRequest{
		Target:    "story",
		Title:     title,
		Body:      body,
		AppID:     m.appIDForBug(),
		StatePath: string(m.currentState),
		Severity:  "med",
		TraceRef:  bugreport.ScrubText(m.traceFilePath),
		TargetDir: localRoot,
		FiledBy:   os.Getenv("USER"),
		Runtime:   runtime,
		Warnf:     func(string, ...any) {},
	})
	if err != nil {
		return m.bugBlock(fmt.Sprintf("could not file bug: %v", err)), m, nil
	}

	artifactsDir := strings.TrimSuffix(absPath, ".md") + ".artifacts"
	artifacts = m.bugArtifacts(runtime, traceJSON)
	displayPath := tuiDisplayBugPath(localDisplayPrefix, relPath)
	if err := bugreport.WriteArtifacts(artifactsDir, artifacts); err != nil {
		return m.bugBlock(fmt.Sprintf("filed %s, but could not write artifacts: %v", displayPath, err)), m, nil
	}
	if err := appendTUIArtifactsSection(absPath, id, bugreport.HasArtifact(artifacts, "trace.redacted.jsonl")); err != nil {
		return m.bugBlock(fmt.Sprintf("filed %s, but could not append artifact links: %v", displayPath, err)), m, nil
	}

	return m.bugBlock(fmt.Sprintf("filed %s (%s)", filepath.ToSlash(displayPath), bugreport.PrivacyCommandStatus(privacy))), m, nil
}

func (m RootModel) bugPrivacyCheckerForCommand() bugprivacy.Checker {
	if m.bugPrivacyCheckerResolver == nil {
		return m.bugPrivacyChecker
	}
	selection := orchestrator.ProfileSelection{}
	if m.orch != nil {
		selection = m.orch.Selection()
	}
	if checker := m.bugPrivacyCheckerResolver(selection); checker != nil {
		return checker
	}
	return m.bugPrivacyChecker
}

func (m RootModel) fileGitHubBug(root, title, body string, traceJSON []byte) string {
	runtime := reportmeta.Capture(root, m.appDefForBug())
	prefix := "tui-bug-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	artifactsDir := filepath.Join(root, ".artifacts", "bug-reports", prefix)
	artifacts := m.bugArtifacts(runtime, traceJSON)
	if err := bugreport.WriteArtifacts(artifactsDir, artifacts); err != nil {
		return fmt.Sprintf("could not write artifacts: %v", err)
	}

	displayRoot := filepath.ToSlash(filepath.Join(".artifacts", "bug-reports", prefix))
	evidence := bugreport.EvidenceFiles(artifactsDir, displayRoot, artifacts)
	res, err := host.GitHubFileBug(context.Background(), host.GitHubBugFiling{
		Repo:            strings.TrimSpace(m.bugTicketRepo),
		Title:           title,
		Body:            body,
		Severity:        "med",
		Component:       "tui",
		Target:          "kitsoki",
		TraceRef:        bugreport.ScrubText(m.traceFilePath),
		KitsokiRev:      bugreport.GitShortRev(root),
		FiledBy:         os.Getenv("USER"),
		Evidence:        evidence,
		Runtime:         runtime,
		UploadArtifacts: true,
	})
	if err != nil {
		return fmt.Sprintf("could not file GitHub bug: %v", err)
	}
	return fmt.Sprintf("filed %s", res.URL)
}

func tuiBugTitle(desc string) string {
	if desc == "" {
		return "tui: bug report " + time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	title := strings.TrimSpace(strings.Split(desc, "\n")[0])
	if len(title) > 80 {
		title = strings.TrimSpace(title[:80])
	}
	return "tui: " + title
}

func tuiBugBody(m RootModel, desc string) string {
	if desc == "" {
		desc = "Filed from the TUI with no operator description."
	}
	var sb strings.Builder
	sb.WriteString(desc)
	sb.WriteString("\n\n## TUI context\n\n")
	fmt.Fprintf(&sb, "- App: `%s`\n", m.appIDForBug())
	fmt.Fprintf(&sb, "- State: `%s`\n", m.currentState)
	fmt.Fprintf(&sb, "- Session: `%s`\n", m.sid)
	if m.appPath != "" {
		fmt.Fprintf(&sb, "- App path: `%s`\n", filepath.ToSlash(m.appPath))
	}
	if m.traceFilePath != "" {
		fmt.Fprintf(&sb, "- Trace: `%s`\n", filepath.ToSlash(m.traceFilePath))
	}
	sb.WriteString("\nSee the attached TUI transcript and context evidence captured at filing time.\n")
	return bugreport.ScrubText(sb.String())
}

func (m RootModel) appIDForBug() string {
	if m.orch == nil || m.orch.AppDef() == nil {
		return ""
	}
	return m.orch.AppDef().App.ID
}

func (m RootModel) appDefForBug() *app.AppDef {
	if m.orch == nil {
		return nil
	}
	return m.orch.AppDef()
}

func (m RootModel) resolveBugRoot() (string, error) {
	if strings.TrimSpace(m.bugRoot) != "" {
		return m.bugRoot, nil
	}
	if m.appPath != "" {
		start := m.appPath
		if info, err := os.Stat(start); err == nil && !info.IsDir() {
			start = filepath.Dir(start)
		}
		if root := nearestGitRoot(start); root != "" {
			return root, nil
		}
		return start, nil
	}
	return os.Getwd()
}

func nearestGitRoot(start string) string {
	dir := filepath.Clean(start)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func (m RootModel) bugArtifacts(runtime reportmeta.Snapshot, traceJSON []byte) []bugreport.Artifact {
	meta := map[string]any{
		"app_id":     m.appIDForBug(),
		"app_path":   filepath.ToSlash(m.appPath),
		"state_path": string(m.currentState),
		"session_id": string(m.sid),
		"trace_ref":  filepath.ToSlash(m.traceFilePath),
		"filed_at":   time.Now().UTC().Format(time.RFC3339),
		"surface":    "tui",
	}
	if !runtime.Empty() {
		meta["runtime"] = runtime
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		data = []byte(`{"surface":"tui"}` + "\n")
	} else {
		data = append(data, '\n')
	}
	return []bugreport.Artifact{
		{Name: "transcript.md", Data: []byte(bugreport.ScrubText(m.transcript.AllContent())), Label: "TUI transcript (scrubbed)"},
		{Name: "context.json", Data: []byte(bugreport.ScrubText(string(data))), Label: "TUI session context"},
		{Name: "trace.redacted.jsonl", Data: traceJSON, Label: "Depersonalized session trace (redacted)"},
	}
}

func (m RootModel) depersonalizedBugTraceJSON() []byte {
	sourceID := string(m.sid)
	if m.traceFilePath != "" {
		if !m.traceFileExternal && m.traceRing != nil {
			if events, err := bugreport.TraceEventsFromBytes(m.traceRing.Snapshot(), sourceID); err == nil && len(events) > 0 {
				return bugreport.DepersonalizedTraceJSONL(events, bugreport.ScrubOptions())
			}
		}
		if events, err := bugreport.ReadTraceEvents(m.traceFilePath, sourceID); err == nil && len(events) > 0 {
			return bugreport.DepersonalizedTraceJSONL(events, bugreport.ScrubOptions())
		}
	}
	if m.traceHistory != nil {
		if hist, err := m.traceHistory(); err == nil && len(hist) > 0 {
			events := bugreport.TraceEventsFromHistory(hist, sourceID)
			if len(events) > 0 {
				return bugreport.DepersonalizedTraceJSONL(events, bugreport.ScrubOptions())
			}
		}
	}
	return nil
}

func appendTUIArtifactsSection(absPath, id string, hasTrace bool) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n## Artifacts\n\n- `./%s.artifacts/transcript.md` - scrubbed TUI transcript at filing time\n- `./%s.artifacts/context.json` - TUI session metadata\n", id, id)
	if hasTrace {
		fmt.Fprintf(&sb, "- `./%s.artifacts/trace.redacted.jsonl` - depersonalized session trace\n", id)
	}
	f, err := os.OpenFile(absPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(sb.String())
	return err
}

func tuiLocalBugFilingRoot(root string) (absRoot, displayPrefix string) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	if filepath.Base(filepath.Clean(root)) == ".artifacts" {
		return root, ""
	}
	return filepath.Join(root, ".artifacts"), ".artifacts"
}

func tuiDisplayBugPath(prefix, rel string) string {
	if strings.TrimSpace(prefix) == "" {
		return rel
	}
	return filepath.Join(prefix, rel)
}

func prefixTUIPrivacyFollowUp(privacy bugprivacy.Result, prefix string) bugprivacy.Result {
	if strings.TrimSpace(prefix) == "" || strings.TrimSpace(privacy.FollowUpPath) == "" {
		return privacy
	}
	privacy.FollowUpPath = tuiDisplayBugPath(prefix, privacy.FollowUpPath)
	return privacy
}

func (m RootModel) bugBlock(line string) string {
	width := m.transcript.width
	if width < 40 {
		width = 40
	}
	return blocks.New(m.transcript.width, m.currentTheme()).SlashOutput(ansi.Wordwrap("bug: "+line, width, " "))
}
