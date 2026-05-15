package metamode

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
)

// withKitsokiEditMode is an option for newTestController that adds a
// `kitsoki.edit` meta mode + kitsoki-engineer agent (skeleton; just
// enough to drive cross-app keying tests).
//
// Replaces the legacy `withSelfMode` helper after `/meta self` was
// removed — the cross-app keying it exercised is now provided by the
// `kitsoki.*` group, with `edit` playing the role the old `self` did
// (default verb, same agent).
func withKitsokiEditMode(c *Controller) {
	if c.AppDef == nil {
		c.AppDef = &app.AppDef{App: app.AppMeta{ID: "test-app"}}
	}
	if c.AppDef.MetaModes == nil {
		c.AppDef.MetaModes = map[string]*app.MetaModeDef{}
	}
	c.AppDef.MetaModes["kitsoki.edit"] = &app.MetaModeDef{
		Group:   "kitsoki",
		Trigger: "edit",
		Default: true,
		Label:   "Edit kitsoki",
		Agent:   "kitsoki-engineer",
		Cwd:     "/tmp/kitsoki-repo-fake",
	}
	// Register the agent in whatever fake registry the test wired.
	c.Agents.Register(agents.Agent{
		Name:         "kitsoki-engineer",
		SystemPrompt: "fake engineer prompt",
		Tools:        []string{"Bash", "Read", "Write", "Edit"},
		DefaultCwd:   "/tmp/kitsoki-repo-fake",
	})
}

// TestMetaAppID checks the helper that picks the appID for chat
// resolution: SelfAppID for kitsoki-target modes, the running app's id
// for anything else.
func TestMetaAppID(t *testing.T) {
	require.Equal(t, SelfAppID, metaAppID("kitsoki.edit", "running-app"),
		"kitsoki.edit keys against SelfAppID regardless of running app")
	require.Equal(t, SelfAppID, metaAppID("kitsoki.ask", "running-app"),
		"kitsoki.ask is cross-app too")
	require.Equal(t, SelfAppID, metaAppID("kitsoki.bug", "running-app"),
		"kitsoki.bug is cross-app too")
	require.Equal(t, "running-app", metaAppID("story.edit", "running-app"),
		"story-target modes key against the running app")
	require.Equal(t, "running-app", metaAppID("story.bug", "running-app"),
		"story.bug is per-app, not cross-app")
}

// TestMetaScopeKey checks the helper that picks the scope_key: empty
// for kitsoki-target modes (one conversation per verb, cross-app), the
// state path for everything else.
func TestMetaScopeKey(t *testing.T) {
	require.Equal(t, "", metaScopeKey("kitsoki.edit", "foyer"),
		"kitsoki.edit uses an empty scope_key — one conversation per user per verb")
	require.Equal(t, "", metaScopeKey("kitsoki.ask", "foyer"),
		"kitsoki.ask is also cross-app empty-scope")
	require.Equal(t, "foyer", metaScopeKey("story.edit", "foyer"),
		"story-target modes key against state path")
}

// TestController_Enter_KitsokiEditKeysCrossApp asserts that entering
// `kitsoki.edit` from a running app `cloak` resolves the chat under
// SelfAppID (not the running app's id) and with an empty scope_key (not
// the state path).
func TestController_Enter_KitsokiEditKeysCrossApp(t *testing.T) {
	c, store, _ := newTestController(t, withKitsokiEditMode)
	c.AppDef.App.ID = "cloak" // simulate running cloak
	snap := makeSnapshot("foyer")

	_, err := c.Enter(context.Background(), snap, "kitsoki.edit")
	require.NoError(t, err)

	require.Equal(t, SelfAppID, store.gotAppID,
		"kitsoki.edit must resolve chat under SelfAppID, not the running app's id")
	require.Equal(t, "meta:kitsoki.edit", store.gotRoom)
	require.Equal(t, "", store.gotScopeKey,
		"kitsoki-target chats use an empty scope_key for cross-app continuity")
}

// TestController_Enter_NonKitsokiModesUnchanged asserts the keying
// behaviour for story-target modes is unaffected by the cross-app shim.
func TestController_Enter_NonKitsokiModesUnchanged(t *testing.T) {
	c, store, _ := newTestController(t)
	c.AppDef.App.ID = "cloak"
	snap := makeSnapshot("foyer")

	_, err := c.Enter(context.Background(), snap, "story")
	require.NoError(t, err)

	require.Equal(t, "cloak", store.gotAppID,
		"story-target modes must still key against the running app")
	require.Equal(t, "foyer", store.gotScopeKey,
		"story-target modes still key by state path")
}

// TestController_EnterByChatID_AcceptsKitsokiFromAnyApp asserts that a
// chat row whose AppID() is SelfAppID can be resumed from a running
// app with a different id.
func TestController_EnterByChatID_AcceptsKitsokiFromAnyApp(t *testing.T) {
	c, store, _ := newTestController(t, withKitsokiEditMode)
	c.AppDef.App.ID = "dev-story" // running app

	// Seed a kitsoki.edit chat created from a different app earlier.
	kitsokiChat := &fakeChat{
		id:       "kitsoki-chat-id-123",
		appID:    SelfAppID,
		room:     "meta:kitsoki.edit",
		scopeKey: "",
		title:    "Edit kitsoki",
	}
	store.rows = append(store.rows, kitsokiChat)

	snap := makeSnapshot("foyer")
	s, err := c.EnterByChatID(context.Background(), snap, "kitsoki.edit", "kitsoki-chat-id-123")
	require.NoError(t, err, "kitsoki-target chat must be resumable from any running app")
	require.NotNil(t, s)
	require.Equal(t, "kitsoki-chat-id-123", s.Chat.ID())
}

// TestController_EnterByChatID_RejectsForeignAppChat asserts the
// guardrail still rejects a chat that belongs to neither the running
// app nor SelfAppID (catches a genuine app mismatch / dangling id).
func TestController_EnterByChatID_RejectsForeignAppChat(t *testing.T) {
	c, store, _ := newTestController(t, withKitsokiEditMode)
	c.AppDef.App.ID = "dev-story"

	// Seed a chat belonging to some other app.
	foreign := &fakeChat{
		id:       "foreign-id",
		appID:    "totally-other-app",
		room:     "meta:kitsoki.edit",
		scopeKey: "",
		title:    "stranger",
	}
	store.rows = append(store.rows, foreign)

	snap := makeSnapshot("foyer")
	_, err := c.EnterByChatID(context.Background(), snap, "kitsoki.edit", "foreign-id")
	require.Error(t, err, "chat belonging to a different app must still be rejected")
}

// TestController_ListChats_MergesKitsokiChats asserts that calling
// ListChats with the running app's id pulls in cross-app kitsoki chats
// too, so /meta list surfaces them without the user needing to know
// the synthetic SelfAppID.
func TestController_ListChats_MergesKitsokiChats(t *testing.T) {
	c, store, _ := newTestController(t, withKitsokiEditMode)
	c.AppDef.App.ID = "cloak"

	// One app-scoped chat under `cloak` and one kitsoki chat under
	// SelfAppID. Different updated-at so the sort order is deterministic.
	cloakChat := &fakeChat{
		id:        "cloak-chat",
		appID:     "cloak",
		room:      "meta:story",
		scopeKey:  "foyer",
		title:     "improve the story",
		updatedAt: time.Unix(2_000, 0).UTC(),
	}
	kitsokiChat := &fakeChat{
		id:        "kitsoki-chat",
		appID:     SelfAppID,
		room:      "meta:kitsoki.edit",
		scopeKey:  "",
		title:     "Edit kitsoki",
		updatedAt: time.Unix(3_000, 0).UTC(), // newer
	}
	store.rows = append(store.rows, cloakChat, kitsokiChat)

	got, err := c.ListChats(context.Background(), "cloak")
	require.NoError(t, err)
	require.Len(t, got, 2, "ListChats must merge cross-app kitsoki chats with the running app's chats")

	ids := []string{got[0].ID, got[1].ID}
	sort.Strings(ids)
	require.Equal(t, []string{"cloak-chat", "kitsoki-chat"}, ids,
		"both rows must appear in the merged listing")
	// Kitsoki chat is newer, so it sorts first.
	require.Equal(t, "kitsoki-chat", got[0].ID, "newer chat must sort first (UpdatedAt desc)")
}

// TestController_ListChats_SelfAppIDPassthrough asserts that asking
// for SelfAppID explicitly returns ONLY kitsoki chats (no double-list).
func TestController_ListChats_SelfAppIDPassthrough(t *testing.T) {
	c, store, _ := newTestController(t, withKitsokiEditMode)
	c.AppDef.App.ID = "cloak"

	kitsokiChat := &fakeChat{
		id:        "kitsoki-chat",
		appID:     SelfAppID,
		room:      "meta:kitsoki.edit",
		scopeKey:  "",
		title:     "Edit kitsoki",
		updatedAt: time.Unix(3_000, 0).UTC(),
	}
	store.rows = append(store.rows, kitsokiChat)

	got, err := c.ListChats(context.Background(), SelfAppID)
	require.NoError(t, err)
	require.Len(t, got, 1, "asking explicitly for SelfAppID must not double-list")
	require.Equal(t, "kitsoki-chat", got[0].ID)
}
