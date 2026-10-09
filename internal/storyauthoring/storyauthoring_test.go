package storyauthoring

import "testing"

func TestHideIntentFromMenu(t *testing.T) {
	tests := []struct {
		name   string
		state  string
		intent string
		want   bool
	}{
		{"entry intent outside authoring", "start", EnterIntent, true},
		{"entry intent inside authoring", RoomState, EnterIntent, true},
		{"capture outside authoring", "start", CaptureIntent, true},
		{"capture inside authoring", RoomState, CaptureIntent, false},
		{"return inside authoring", RoomState, ReturnIntent, false},
		{"ordinary intent", "start", "look", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HideIntentFromMenu(tt.state, tt.intent); got != tt.want {
				t.Fatalf("HideIntentFromMenu(%q, %q) = %v, want %v", tt.state, tt.intent, got, tt.want)
			}
		})
	}
}
