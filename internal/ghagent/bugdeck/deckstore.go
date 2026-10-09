package bugdeck

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DeckStore is the agent's local home for hosted bug-report decks: one
// self-contained HTML file per deck under Dir, served over the agent's own HTTP
// server at <PublicBaseURL>/decks/<id>. This is the "stored as an asset on the
// agent, viewable without downloading" surface.
type DeckStore struct {
	Dir string // directory holding <id>.html files

	mu sync.Mutex
}

// NewDeckStore returns a store rooted at dir, creating it if needed.
func NewDeckStore(dir string) (*DeckStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("deck store: dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("deck store: mkdir: %w", err)
	}
	return &DeckStore{Dir: dir}, nil
}

// Put copies the rendered deck at srcHTML into the store under id, returning the
// stored path. id is sanitized into a safe filename.
func (s *DeckStore) Put(id, srcHTML string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dst := s.pathFor(id)
	in, err := os.Open(srcHTML)
	if err != nil {
		return "", fmt.Errorf("deck store: open src: %w", err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("deck store: create dst: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", fmt.Errorf("deck store: copy: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("deck store: close: %w", err)
	}
	return dst, nil
}

// pathFor maps a deck id to its on-disk file, sanitizing the id so it can never
// escape Dir.
func (s *DeckStore) pathFor(id string) string {
	return filepath.Join(s.Dir, SanitizeDeckID(id)+".html")
}

// Exists reports whether a deck for id is already hosted — used to make the
// webhook auto-trigger idempotent against GitHub redeliveries.
func (s *DeckStore) Exists(id string) bool {
	info, err := os.Stat(s.pathFor(id))
	return err == nil && !info.IsDir()
}

// Handler serves stored decks as text/html (so a browser renders them inline
// rather than downloading). Mount it at "/decks/".
func (s *DeckStore) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/decks/")
		id = strings.TrimSuffix(id, ".html")
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		path := s.pathFor(id)
		data, err := os.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
		_, _ = w.Write(data)
	}
}

// DeckID derives a stable, filename-safe deck id from a repo slug + issue number
// (so the same issue always maps to the same hosted deck and the same evidence
// folder). Both the web bug-filer (depositing evidence) and the agent (reading
// it + hosting the deck) MUST agree on this — keep it here as the one source.
func DeckID(repo, number string) string {
	slug := strings.ReplaceAll(strings.TrimSpace(repo), "/", "-")
	return SanitizeDeckID(slug + "-" + strings.TrimSpace(number))
}

// SanitizeDeckID reduces an id to [A-Za-z0-9._-], so it is a safe single path
// segment / filename. Any other rune becomes '-'.
func SanitizeDeckID(id string) string {
	id = strings.TrimSpace(id)
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "deck"
	}
	return out
}
