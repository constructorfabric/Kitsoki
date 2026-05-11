# VHS tape for the dev-story demo.
# Paced slower than cloak.tape so each room's view is readable.
# Showcases: the Oracle Room's persistent Claude Code session, then
# workspace navigation, the proposal lifecycle, and the inbox.
#
# The tape points KITSOKI_ORACLE_CLAUDE_BIN at demo/fake-oracle-claude.sh so the
# Oracle Room response is deterministic and does not depend on a live Claude
# CLI during recording.
#
# Run: vhs demo/dev-story.tape
# Output: demo/dev-story.gif

Output demo/dev-story.gif

Set Width 1280
Set Height 900
Set FontSize 14
Set FontFamily "JetBrains Mono"
Set Theme "Molokai"
Set TypingSpeed 110ms

Env KITSOKI_ORACLE_CLAUDE_BIN "demo/fake-oracle-claude.sh"

# Launch dev-story with the replay harness.
Type "./kitsoki run testdata/apps/dev-story/app.yaml --harness replay --recording testdata/apps/dev-story/recording.yaml"
Enter
Sleep 4s

# Turn 1: open the Oracle Room — chat list view loads (empty list initially).
Type "consult the oracle"
Enter
Sleep 3500ms

# Turn 2: ask Claude a question — auto-creates a chat, runs one turn,
#         appends both Q+A to the persistent transcript.
Type "how does the auth token refresh work"
Enter
Sleep 5500ms

# Turn 3: leave the Oracle — chat persists in SQLite for next time.
Type "go back"
Enter
Sleep 3s

# Turn 4: now start real work — navigate to the workspace manager.
Type "open workspace manager"
Enter
Sleep 3500ms

# Turn 5: enter a workspace.
Type "enter my-api-project"
Enter
Sleep 3s

# Turn 6: confirm entry — workspace_loading → workspace_room.
Type "enter workspace"
Enter
Sleep 3500ms

# Turn 7: open the Terminal Room.
Type "open terminal"
Enter
Sleep 3s

# Turn 8: propose a shell command.
Type "run git status"
Enter
Sleep 3500ms

# Turn 9: refine the proposal (iterate before running).
Type "refine with --short"
Enter
Sleep 3500ms

# Turn 10: accept — transition to executing.
Type "accept"
Enter
Sleep 2500ms

# Turn 11: advance to the result view.
Type "accept"
Enter
Sleep 4s

# Turn 12: check the inbox.
Type "check my inbox"
Enter
Sleep 3500ms

# Turn 13: return to main.
Type "home"
Enter
Sleep 4s
