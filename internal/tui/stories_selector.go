package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// StoryOption is one entry in the TUI story selector. The CLI owns discovery
// and passes these values in; the TUI only renders and returns the selected row.
type StoryOption struct {
	Path           string
	AppID          string
	Title          string
	ActiveSessions []string
}

type storySelectorChoiceMsg struct {
	story StoryOption
}

type storySelectorModel struct {
	active   bool
	stories  []StoryOption
	err      string
	selected int
}

func newStorySelectorModel() storySelectorModel {
	return storySelectorModel{}
}

func (m *storySelectorModel) SetStories(stories []StoryOption, errText string) {
	m.stories = append([]StoryOption(nil), stories...)
	m.err = strings.TrimSpace(errText)
	if m.selected >= len(m.stories) {
		m.selected = 0
	}
}

func (m *storySelectorModel) Open(currentPath string) {
	m.active = true
	m.selected = 0
	for i, story := range m.stories {
		if sameStoryPath(currentPath, story.Path) {
			m.selected = i
			break
		}
	}
}

func (m *storySelectorModel) Close() {
	m.active = false
	m.selected = 0
}

func (m storySelectorModel) IsActive() bool { return m.active }

func (m storySelectorModel) Update(msg tea.Msg) (storySelectorModel, tea.Cmd) {
	if !m.active {
		return m, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "esc", "q":
		m.Close()
		return m, nil
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
		return m, nil
	case "down", "j":
		if m.selected < len(m.stories)-1 {
			m.selected++
		}
		return m, nil
	case "enter":
		if len(m.stories) == 0 {
			return m, nil
		}
		chosen := m.stories[m.selected]
		m.Close()
		return m, func() tea.Msg { return storySelectorChoiceMsg{story: chosen} }
	}
	for i := 1; i <= len(m.stories) && i <= 9; i++ {
		if keyMsg.String() == fmt.Sprintf("%d", i) {
			chosen := m.stories[i-1]
			m.selected = i - 1
			m.Close()
			return m, func() tea.Msg { return storySelectorChoiceMsg{story: chosen} }
		}
	}
	return m, nil
}

func (m storySelectorModel) View() string {
	if !m.active {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("stories (up/down to move, Enter to launch, Esc to close)\n\n")
	if m.err != "" {
		sb.WriteString(choiceErrorStyle.Render("(" + m.err + ")"))
		sb.WriteString("\n\n")
	}
	if len(m.stories) == 0 {
		sb.WriteString(choiceHintStyle.Render("No stories discovered. Check .kitsoki.yaml story_dirs or ./stories."))
		return sb.String()
	}
	for i, story := range m.stories {
		marker := "  "
		label := storySelectorLabel(story)
		if i == m.selected {
			marker = "> "
			label = menuItemSelectedStyle.Render(label)
		} else {
			label = menuItemStyle.Render(label)
		}
		sb.WriteString(fmt.Sprintf("%s[%d] %s", marker, i+1, label))
		hint := storySelectorHint(story)
		if hint != "" {
			sb.WriteString(" - ")
			sb.WriteString(menuItemBlockedStyle.Render(hint))
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func storySelectorLabel(story StoryOption) string {
	if story.Title != "" {
		return story.Title
	}
	if story.AppID != "" {
		return story.AppID
	}
	if story.Path != "" {
		return filepath.Base(filepath.Dir(story.Path))
	}
	return "(untitled story)"
}

func storySelectorHint(story StoryOption) string {
	var parts []string
	if story.AppID != "" && story.AppID != storySelectorLabel(story) {
		parts = append(parts, story.AppID)
	}
	if n := len(story.ActiveSessions); n > 0 {
		parts = append(parts, fmt.Sprintf("%d active", n))
	}
	if story.Path != "" {
		parts = append(parts, story.Path)
	}
	return strings.Join(parts, " | ")
}

func sameStoryPath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	aa, aerr := filepath.Abs(a)
	bb, berr := filepath.Abs(b)
	if aerr == nil && berr == nil {
		return filepath.Clean(aa) == filepath.Clean(bb)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
