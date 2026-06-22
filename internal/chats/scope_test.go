package chats_test

import (
	"testing"

	"kitsoki/internal/chats"
)

func TestDisplayScopeKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "scope", want: "scope"},
		{name: "empty", in: "", want: ""},
		{name: "session scoped", in: "\x00session=session-1\x00mcp-smoke", want: "mcp-smoke"},
		{name: "session scoped empty logical scope", in: "\x00session=session-1\x00", want: ""},
	}
	for _, tt := range tests {
		if got := chats.DisplayScopeKey(tt.in); got != tt.want {
			t.Fatalf("%s: DisplayScopeKey(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}
