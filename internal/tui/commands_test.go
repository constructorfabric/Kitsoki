package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/app"
	"kitsoki/internal/bugprivacy"
	"kitsoki/internal/chats"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestHelpCommandLists confirms /help renders all four sections and
// includes a representative command from each. The exact list will
// drift as phases land more commands — these checks are about
// structural presence, not exact wording.
func TestHelpCommandLists(t *testing.T) {
	t.Parallel()
	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)
	body, _, _ := HelpCommand{}.Run(m, nil)
	for _, want := range []string{
		"chat blocks",
		"dedicated views",
		"room switches",
		"system",
		"/help",
		"/stories",
		"/bug [description]",
		"/chat show",
		"/intents",
		"/work [--all|drive|artifact|summary]",
		"/world",
		"/meta",
		"/quit",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/help missing %q in output\n---\n%s", want, body)
		}
	}
}

func TestStoriesSlashOpensSelector(t *testing.T) {
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("session-123"), "../../testdata/apps/cloak/app.yaml", "",
		WithStorySelector([]StoryOption{
			{Path: "/repo/stories/alpha/app.yaml", AppID: "alpha", Title: "Alpha"},
			{Path: "/repo/stories/beta/app.yaml", AppID: "beta", Title: "Beta"},
		}, nil, nil),
	)

	next, cmd := m.RunSlashCommand("/stories")
	if cmd != nil {
		t.Fatal("/stories should open synchronously")
	}
	if next.mode != ModeStorySelector {
		t.Fatalf("mode = %v, want ModeStorySelector", next.mode)
	}
	view := next.View()
	for _, want := range []string{"stories", "Alpha", "Beta"} {
		if !strings.Contains(view, want) {
			t.Fatalf("selector view missing %q\n---\n%s", want, view)
		}
	}
}

func TestStorySelectorSelectionInvokesCallbackAndQuits(t *testing.T) {
	orch := testCloakOrchestrator(t)
	var selected StoryOption
	m := NewRootModel(orch, app.SessionID("session-123"), "../../testdata/apps/cloak/app.yaml", "",
		WithStorySelector([]StoryOption{
			{Path: "/repo/stories/alpha/app.yaml", AppID: "alpha", Title: "Alpha"},
			{Path: "/repo/stories/beta/app.yaml", AppID: "beta", Title: "Beta"},
		}, nil, func(story StoryOption) { selected = story }),
	)

	next, _ := m.RunSlashCommand("/stories")
	model, _ := tea.Model(next).Update(tea.KeyMsg{Type: tea.KeyDown})
	model, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should emit a story selection command")
	}
	model, cmd = model.Update(runBatch(t, cmd))
	if selected.Path != "/repo/stories/beta/app.yaml" {
		t.Fatalf("selected path = %q, want beta", selected.Path)
	}
	if cmd == nil {
		t.Fatal("selection should return tea.Quit")
	}
	rm, ok := model.(RootModel)
	if !ok {
		t.Fatalf("model = %T, want RootModel", model)
	}
	if !rm.quitting {
		t.Fatal("model should be marked quitting after story selection")
	}
}

func TestStorySelectorCurrentStoryDoesNotRestart(t *testing.T) {
	orch := testCloakOrchestrator(t)
	current := "../../testdata/apps/cloak/app.yaml"
	called := false
	m := NewRootModel(orch, app.SessionID("session-123"), current, "",
		WithStorySelector([]StoryOption{
			{Path: current, AppID: "cloak-of-darkness", Title: "Cloak"},
		}, nil, func(StoryOption) { called = true }),
	)

	next, _ := m.RunSlashCommand("/stories")
	model, cmd := tea.Model(next).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should emit a story selection command")
	}
	model, _ = model.Update(runBatch(t, cmd))
	if called {
		t.Fatal("current-story selection should not invoke restart callback")
	}
	rm, ok := model.(RootModel)
	if !ok {
		t.Fatalf("model = %T, want RootModel", model)
	}
	if rm.quitting {
		t.Fatal("current-story selection should leave the model running")
	}
	if !strings.Contains(rm.transcript.LastBody(), "already running") {
		t.Fatalf("transcript missing already-running notice:\n%s", rm.transcript.LastBody())
	}
}

func TestBugCommandFilesReportAndArtifacts(t *testing.T) {
	root := t.TempDir()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("session-123"), "../../testdata/apps/cloak/app.yaml", "", WithBugRoot(root), WithBugTicketRepo(""))
	m.currentState = "foyer"
	m.traceFilePath = filepath.Join(root, "trace.jsonl")
	m.transcript.AppendTurn("look", "You are in /Users/example/project with token=sk-test-secret.")

	body, next, cmd := BugCommand{}.Run(m, []string{"button", "does", "nothing"})
	if cmd != nil {
		t.Fatal("bug command should be synchronous")
	}
	if !strings.Contains(body, "filed .artifacts/issues/bugs/") {
		t.Fatalf("unexpected command body: %s", body)
	}
	if next.transcript.EntryCount() != m.transcript.EntryCount() {
		t.Fatal("bug command should not mutate transcript until dispatcher appends the returned block")
	}

	bugsDir := filepath.Join(root, ".artifacts", "issues", "bugs")
	entries, err := os.ReadDir(bugsDir)
	if err != nil {
		t.Fatalf("read bugs dir: %v", err)
	}
	var mdEntries []os.DirEntry
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			mdEntries = append(mdEntries, entry)
		}
	}
	if len(mdEntries) != 1 {
		t.Fatalf("bug files = %d, want 1", len(mdEntries))
	}
	id := strings.TrimSuffix(mdEntries[0].Name(), ".md")
	mdPath := filepath.Join(bugsDir, mdEntries[0].Name())
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read bug md: %v", err)
	}
	for _, want := range []string{
		`target: "story"`,
		`app_id: "cloak-of-darkness"`,
		`state_path: "foyer"`,
		`story_app_id: "cloak-of-darkness"`,
		`story_app_version: "0.1.0"`,
		`story_checksum_sha256: "sha256:`,
		"button does nothing",
		"./" + id + ".artifacts/transcript.md",
		"./" + id + ".artifacts/context.json",
	} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("bug markdown missing %q\n---\n%s", want, md)
		}
	}

	artifactsDir := filepath.Join(bugsDir, id+".artifacts")
	transcript, err := os.ReadFile(filepath.Join(artifactsDir, "transcript.md"))
	if err != nil {
		t.Fatalf("read transcript artifact: %v", err)
	}
	if strings.Contains(string(transcript), "sk-test-secret") {
		t.Fatalf("transcript artifact was not scrubbed:\n%s", transcript)
	}
	if !strings.Contains(string(transcript), "[REDACTED]") {
		t.Fatalf("transcript artifact should contain redaction marker:\n%s", transcript)
	}

	contextData, err := os.ReadFile(filepath.Join(artifactsDir, "context.json"))
	if err != nil {
		t.Fatalf("read context artifact: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(contextData, &meta); err != nil {
		t.Fatalf("context artifact json: %v", err)
	}
	if meta["app_id"] != "cloak-of-darkness" || meta["state_path"] != "foyer" || meta["session_id"] != "session-123" || meta["surface"] != "tui" {
		t.Fatalf("unexpected context metadata: %#v", meta)
	}
	runtime, ok := meta["runtime"].(map[string]any)
	if !ok {
		t.Fatalf("context metadata missing runtime: %#v", meta)
	}
	story, ok := runtime["story"].(map[string]any)
	if !ok || story["app_id"] != "cloak-of-darkness" || !strings.HasPrefix(story["checksum_sha256"].(string), "sha256:") {
		t.Fatalf("unexpected runtime story metadata: %#v", runtime["story"])
	}
}

func TestBugCommandAddsDepersonalizedTraceArtifact(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	diagnosticText := "email alice@example.com and read " + home + "/secret.txt"
	turnStart, err := json.Marshal(map[string]any{"input": diagnosticText})
	if err != nil {
		t.Fatalf("marshal turn payload: %v", err)
	}
	worldUpdate, err := json.Marshal(map[string]any{
		"set": map[string]any{"diagnostic": diagnosticText},
	})
	if err != nil {
		t.Fatalf("marshal trace payload: %v", err)
	}
	hist := store.History{
		{
			Turn:      1,
			Seq:       0,
			Ts:        time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC),
			Kind:      store.TurnStarted,
			StatePath: "foyer",
			Payload:   turnStart,
		},
		{
			Turn:      1,
			Seq:       1,
			Ts:        time.Date(2026, 7, 8, 10, 0, 1, 0, time.UTC),
			Kind:      store.EffectApplied,
			StatePath: "foyer",
			Payload:   worldUpdate,
		},
	}

	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("session-123"), "../../testdata/apps/cloak/app.yaml", "",
		WithBugRoot(root),
		WithBugTicketRepo(""),
		WithTraceHistory(func() (store.History, error) { return hist, nil }),
	)
	m.currentState = "foyer"

	body, _, cmd := BugCommand{}.Run(m, []string{"trace", "has", "private", "text"})
	if cmd != nil {
		t.Fatal("bug command should be synchronous")
	}
	if !strings.Contains(body, "filed .artifacts/issues/bugs/") {
		t.Fatalf("unexpected command body: %s", body)
	}

	bugsDir := filepath.Join(root, ".artifacts", "issues", "bugs")
	entries, err := os.ReadDir(bugsDir)
	if err != nil {
		t.Fatalf("read bugs dir: %v", err)
	}
	var mdEntries []os.DirEntry
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			mdEntries = append(mdEntries, entry)
		}
	}
	if len(mdEntries) != 1 {
		t.Fatalf("bug files = %d, want 1", len(mdEntries))
	}
	id := strings.TrimSuffix(mdEntries[0].Name(), ".md")
	md, err := os.ReadFile(filepath.Join(bugsDir, mdEntries[0].Name()))
	if err != nil {
		t.Fatalf("read bug md: %v", err)
	}
	if !strings.Contains(string(md), "./"+id+".artifacts/trace.redacted.jsonl") {
		t.Fatalf("bug markdown missing depersonalized trace link:\n%s", md)
	}

	traceData, err := os.ReadFile(filepath.Join(bugsDir, id+".artifacts", "trace.redacted.jsonl"))
	if err != nil {
		t.Fatalf("read depersonalized trace: %v", err)
	}
	trace := string(traceData)
	for _, leaked := range []string{"alice@example.com", home + "/secret.txt"} {
		if strings.Contains(trace, leaked) {
			t.Fatalf("trace leaked %q:\n%s", leaked, trace)
		}
	}
	for _, want := range []string{`"msg":"turn.start"`, `"msg":"world.update"`, "[TEXT#"} {
		if !strings.Contains(trace, want) {
			t.Fatalf("trace missing %q:\n%s", want, trace)
		}
	}
}

func TestBugCommandGitHubUploadsArtifacts(t *testing.T) {
	root := t.TempDir()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("session-123"), "../../testdata/apps/cloak/app.yaml", "", WithBugRoot(root), WithBugTicketRepo("o/r"))
	m.currentState = "foyer"
	m.traceFilePath = filepath.Join(root, "trace.jsonl")
	m.transcript.AppendTurn("look", "You are in /Users/example/project with token=sk-test-secret.")

	issueBody, uploads, restoreAPI := stubTUIBugGitHubAPI(t, "88")
	defer restoreAPI()

	body, _, cmd := BugCommand{}.Run(m, []string{"button", "does", "nothing"})
	if cmd != nil {
		t.Fatal("bug command should be synchronous")
	}
	if !strings.Contains(body, "filed https://github.com/o/r/issues/88") {
		t.Fatalf("unexpected command body: %s", body)
	}
	if *uploads != 2 {
		t.Fatalf("expected transcript + context uploads, got %d", *uploads)
	}
	for _, want := range []string{
		"uploaded as GitHub release assets",
		"[TUI transcript (scrubbed)](https://github.com/o/r/releases/download/kitsoki-artifacts/",
		"[TUI session context](https://github.com/o/r/releases/download/kitsoki-artifacts/",
		"button does nothing",
		"story_app_id: cloak-of-darkness",
		"story_checksum_sha256: sha256:",
	} {
		if !strings.Contains(*issueBody, want) {
			t.Fatalf("issue body missing %q: %s", want, *issueBody)
		}
	}
	if strings.Contains(*issueBody, "not uploaded to GitHub") {
		t.Fatalf("upload path must not use local-only disclaimer: %s", *issueBody)
	}
}

func TestBugCommandPrivacyFailureBlocksOriginal(t *testing.T) {
	root := t.TempDir()
	orch := testCloakOrchestrator(t)
	checker := bugprivacy.CheckFunc(func(context.Context, bugprivacy.Report, []bugprivacy.Finding) (bugprivacy.Decision, error) {
		return bugprivacy.Decision{Pass: false, Categories: []string{"email"}, Reason: "contains email"}, nil
	})
	m := NewRootModel(orch, app.SessionID("session-123"), "../../testdata/apps/cloak/app.yaml", "", WithBugRoot(root), WithBugPrivacyChecker(checker))
	m.currentState = "foyer"

	body, _, cmd := BugCommand{}.Run(m, []string{"private", "contact"})
	if cmd != nil {
		t.Fatal("bug command should be synchronous")
	}
	compactBody := strings.Join(strings.Fields(body), " ")
	compactBody = strings.ReplaceAll(compactBody, "follow- up", "follow-up")
	if !strings.Contains(compactBody, "privacy check failed") || !strings.Contains(compactBody, "depersonalized follow-up") {
		t.Fatalf("unexpected command body: %s", body)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".artifacts", "issues", "bugs"))
	if err != nil {
		t.Fatalf("read follow-up dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only follow-up bug, got %d", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(root, ".artifacts", "issues", "bugs", entries[0].Name()))
	if err != nil {
		t.Fatalf("read follow-up: %v", err)
	}
	if strings.Contains(string(data), "private contact") || !strings.Contains(string(data), "email") {
		t.Fatalf("follow-up should be depersonalized: %s", data)
	}
}

func stubTUIBugGitHubAPI(t *testing.T, issueNumber string) (*string, *int, func()) {
	t.Helper()
	t.Setenv("GH_TOKEN", "test-token")
	n, err := strconv.Atoi(issueNumber)
	if err != nil {
		t.Fatalf("bad issue number fixture: %v", err)
	}
	var issueBody string
	var uploads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/releases/tags/kitsoki-artifacts":
			writeTUICommandJSON(t, w, map[string]any{"id": 1, "upload_url": "http://" + r.Host + "/uploads/assets{?name,label}"})
		case r.Method == http.MethodPost && r.URL.Path == "/uploads/assets":
			uploads++
			writeTUICommandJSON(t, w, map[string]any{"id": uploads})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode issue payload: %v", err)
			}
			issueBody, _ = payload["body"].(string)
			writeTUICommandJSON(t, w, map[string]any{
				"number":   n,
				"html_url": "https://github.com/o/r/issues/" + issueNumber,
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	return &issueBody, &uploads, func() {
		restore()
		srv.Close()
	}
}

func writeTUICommandJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("write json: %v", err)
	}
}

func TestBugCommandDispatcherAppendsBlock(t *testing.T) {
	root := t.TempDir()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("session-123"), "../../testdata/apps/cloak/app.yaml", "", WithBugRoot(root), WithBugTicketRepo(""))

	next, cmd := m.RunSlashCommand("/bug surprising state")
	if cmd == nil {
		t.Fatal("bug slash command should return an async command")
	}
	if !strings.Contains(next.transcript.AllContent(), "bug: privacy check starting") {
		t.Fatalf("transcript missing bug start feedback:\n%s", next.transcript.AllContent())
	}
	if strings.Contains(next.transcript.AllContent(), "bug: filed .artifacts/issues/bugs/") {
		t.Fatalf("bug result should not appear before async command completes:\n%s", next.transcript.AllContent())
	}
	updated, _ := next.Update(cmd())
	next, ok := updated.(RootModel)
	if !ok {
		t.Fatalf("updated model = %T, want RootModel", updated)
	}
	if !strings.Contains(next.transcript.AllContent(), "bug: filed .artifacts/issues/bugs/") {
		t.Fatalf("transcript missing bug result:\n%s", next.transcript.AllContent())
	}
}

func testCloakOrchestrator(t *testing.T) *orchestrator.Orchestrator {
	t.Helper()
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	if err != nil {
		t.Fatalf("load cloak app: %v", err)
	}
	mach, err := machine.New(def)
	if err != nil {
		t.Fatalf("build machine: %v", err)
	}
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h, err := harness.NewReplay("../../testdata/apps/cloak/recording.yaml")
	if err != nil {
		t.Fatalf("open harness: %v", err)
	}
	return orchestrator.New(def, mach, s, h)
}

func TestChatScopeDisplayStripsSessionPrefix(t *testing.T) {
	t.Parallel()
	if got := chats.DisplayScopeKey("\x00session=session-1\x00mcp-smoke"); got != "mcp-smoke" {
		t.Fatalf("DisplayScopeKey session scoped = %q, want mcp-smoke", got)
	}
	if got := chats.DisplayScopeKey("plain-scope"); got != "plain-scope" {
		t.Fatalf("DisplayScopeKey plain = %q, want plain-scope", got)
	}
}

// TestActionsAutoToggle exercises /intents auto on|off.
func TestActionsAutoToggle(t *testing.T) {
	t.Parallel()
	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)

	body, next, _ := ActionsCommand{}.Run(m, []string{"auto", "on"})
	if !next.actionsAuto {
		t.Errorf("actionsAuto should be true after `auto on`")
	}
	if !strings.Contains(body, "intents auto on") {
		t.Errorf("expected confirmation line, got %q", body)
	}

	body, next, _ = ActionsCommand{}.Run(next, []string{"auto", "off"})
	if next.actionsAuto {
		t.Errorf("actionsAuto should be false after `auto off`")
	}
	if !strings.Contains(body, "intents auto off") {
		t.Errorf("expected confirmation line, got %q", body)
	}
}

// TestIdeasCommandAppends verifies /ideas appends a bullet line to the
// configured file, preserves prior content, and reports usage when given
// no text. It writes to a temp file (not the repo's ideas.md) via the
// ideasFilePath override, so it cannot run in parallel.
func TestIdeasCommandAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ideas.md")
	if err := os.WriteFile(path, []byte("## Ideas\n- existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := ideasPathOverride
	ideasPathOverride = path
	defer func() { ideasPathOverride = orig }()

	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)

	// Empty args → usage, no write.
	body, _, _ := IdeasCommand{}.Run(m, nil)
	if !strings.Contains(body, "usage") {
		t.Errorf("expected usage line for empty /ideas, got %q", body)
	}

	body, _, _ = IdeasCommand{}.Run(m, []string{"build", "a", "widget"})
	if !strings.Contains(body, "jotted to") {
		t.Errorf("expected confirmation line, got %q", body)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "## Ideas\n- existing\n- build a widget\n"; string(got) != want {
		t.Errorf("file contents = %q, want %q", got, want)
	}
}

// TestAppendIdeaLineMissingNewline confirms a bullet is placed on its own
// line even when the existing file lacks a trailing newline.
func TestAppendIdeaLineMissingNewline(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ideas.md")
	if err := os.WriteFile(path, []byte("- existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := appendIdeaLine(path, "new one"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "- existing\n- new one\n"; string(got) != want {
		t.Errorf("file contents = %q, want %q", got, want)
	}
}

// TestNewInboxNotificationsDetectsAdded verifies the differ used by
// the inbox-arrival transcript line picks up only genuinely new items.
func TestNewInboxNotificationsDetectsAdded(t *testing.T) {
	t.Parallel()
	prior := []jobs.Notification{
		{ID: "a", Title: "alpha"},
		{ID: "b", Title: "bravo"},
	}
	fresh := []jobs.Notification{
		{ID: "a", Title: "alpha"},
		{ID: "b", Title: "bravo"},
		{ID: "c", Title: "charlie"},
		{ID: "d", Title: "delta"},
	}
	got := newInboxNotifications(prior, fresh)
	if len(got) != 2 {
		t.Fatalf("expected 2 new notifications, got %d (%v)", len(got), got)
	}
	if got[0].ID != "c" || got[1].ID != "d" {
		t.Errorf("expected [c,d], got %v", []string{got[0].ID, got[1].ID})
	}
}

func TestInboxNotificationHintIncludesBodyAndOriginURL(t *testing.T) {
	t.Parallel()
	got := inboxNotificationHint(jobs.Notification{
		Body:      "Review requested by alice.\n\nhttps://github.com/acme/repo/pull/42",
		OriginURL: "https://github.com/acme/repo/pull/42",
	})
	if got != "Review requested by alice. - https://github.com/acme/repo/pull/42" {
		t.Fatalf("hint = %q", got)
	}

	got = inboxNotificationHint(jobs.Notification{
		OriginURL: "https://github.com/acme/repo/issues/7",
	})
	if got != "https://github.com/acme/repo/issues/7" {
		t.Fatalf("url-only hint = %q", got)
	}
}

// TestRenderActionsBlockFromMenu sanity-checks that menu items
// populated on the model produce a non-empty actions block.
func TestRenderActionsBlockFromMenu(t *testing.T) {
	t.Parallel()
	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)
	m.menu = newMenuModel(40, 24)
	out := renderActionsBlock(m)
	// Empty menu — should still emit a friendly empty notice.
	if !strings.Contains(out, "no actions") {
		t.Errorf("empty menu should render an empty notice, got:\n%s", out)
	}
}
