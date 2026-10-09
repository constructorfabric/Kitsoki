package storyauthoring

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "embed"
)

const (
	RoomState     = "story_authoring"
	EnterIntent   = "author_story"
	CaptureIntent = "story_authoring_capture"
	ReturnIntent  = "return_to_story"
	ClearIntent   = "clear_story_authoring"

	RequestWorld     = "story_authoring_request"
	NoteWorld        = "story_authoring_note"
	ReturnStateWorld = "story_authoring_return_state"

	SchemaRef = "builtin://story-authoring-note"
)

func IsFrameworkRoom(state string) bool {
	return state == RoomState || strings.HasPrefix(state, RoomState+".")
}

func IsFrameworkIntent(intent string) bool {
	switch intent {
	case EnterIntent, CaptureIntent, ReturnIntent, ClearIntent:
		return true
	default:
		return false
	}
}

func IsFrameworkWorldKey(key string) bool {
	switch key {
	case RequestWorld, NoteWorld, ReturnStateWorld:
		return true
	default:
		return false
	}
}

func IsFrameworkTransition(state, intent string) bool {
	return IsFrameworkRoom(state) || intent == EnterIntent
}

func HideIntentFromMenu(state, intent string) bool {
	if intent == EnterIntent {
		return true
	}
	if IsFrameworkRoom(state) {
		return false
	}
	return IsFrameworkIntent(intent)
}

//go:embed story_authoring_note.schema.json
var noteSchema []byte

// ResolveSchemaPath materializes builtin schema references to a stable temp
// file path. Non-builtin references return ok=false and are left to callers'
// ordinary path resolver.
func ResolveSchemaPath(ref string) (path string, ok bool, err error) {
	if ref != SchemaRef {
		return "", false, nil
	}
	path, err = materializeSchema("story_authoring_note", noteSchema)
	return path, true, err
}

func materializeSchema(name string, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	dir := filepath.Join(os.TempDir(), "kitsoki-builtin-schemas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s_%x.json", name, sum[:8]))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
