package server

// White-box test for toolBreadcrumbs — the shared helper both SSE stream
// handlers use to turn one assistant StreamEvent into per-tool frames.

import (
	"testing"

	"kitsoki/internal/host"
)

func TestToolBreadcrumbs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ev   host.StreamEvent
		want []host.StreamToolUse
	}{
		{
			name: "all parallel tools surface in order",
			ev: host.StreamEvent{
				Type: "assistant",
				Tool: "Bash", Preview: "ls",
				Tools: []host.StreamToolUse{
					{Name: "Bash", Preview: "ls"},
					{Name: "Read", Preview: "a.md"},
					{Name: "Read", Preview: "b.md"},
				},
			},
			want: []host.StreamToolUse{
				{Name: "Bash", Preview: "ls"},
				{Name: "Read", Preview: "a.md"},
				{Name: "Read", Preview: "b.md"},
			},
		},
		{
			name: "falls back to scalar Tool when Tools is empty",
			ev:   host.StreamEvent{Type: "assistant", Tool: "Read", Preview: "p.md"},
			want: []host.StreamToolUse{{Name: "Read", Preview: "p.md"}},
		},
		{
			name: "no tools at all yields nil",
			ev:   host.StreamEvent{Type: "assistant", Text: "just a thought"},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := toolBreadcrumbs(tc.ev)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d breadcrumbs, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("breadcrumb[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
