package bugdeck

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"kitsoki/internal/runstatus/harscrub"
)

// ClipFilename is the name BuildSpec expects the rrweb clip to be written under,
// in the same directory as the spec, so the spec's video scene can reference it.
const ClipFilename = "clip.rrweb.json"

// maxHARRows caps the network-summary table so a long trace stays a readable
// slide rather than an unbounded wall. Dropped rows are noted in the caption.
const maxHARRows = 12

// Evidence is the captured material for one filed bug report. RRWeb and HAR are
// the raw (already-scrubbed) bytes of the issue's rrweb.json and har.json
// release assets; the rest is issue metadata used for deck framing. RRWeb and
// HAR are both optional — the deck degrades to whichever evidence is present.
type Evidence struct {
	RRWeb       []byte // rrweb.json events (optional)
	HAR         []byte // har.json network trace (optional)
	Title       string // issue title
	IssueURL    string // issue html url (for the CTA)
	IssueNumber string // issue number, e.g. "62"
	FiledBy     string // reporter handle, if known
}

// BuildSpec turns evidence into a slidey deck spec (JSON bytes) and the clip
// bytes to write alongside it as [ClipFilename]. It is a pure, deterministic
// transform: same evidence in, byte-identical spec out — no clock, no network,
// no LLM, and no narration (so rendering never calls a TTS voice).
//
// clip is nil when there is no rrweb evidence; the caller writes it next to the
// spec only when non-nil, and the spec omits the replay scene to match.
func BuildSpec(ev Evidence) (spec []byte, clip []byte, err error) {
	title := strings.TrimSpace(ev.Title)
	if title == "" {
		title = "kitsoki bug report"
	}

	scenes := []scene{{
		Type:     "title",
		Eyebrow:  "kitsoki bug report · auto-generated, no LLM",
		Title:    title,
		Subtitle: "Session replay + network evidence, built deterministically from the report",
	}}

	if len(ev.RRWeb) > 0 {
		if !json.Valid(ev.RRWeb) {
			return nil, nil, fmt.Errorf("rrweb evidence is not valid JSON")
		}
		clip = ev.RRWeb
		scenes = append(scenes, scene{
			Type:     "video",
			Mode:     "embedded",
			Eyebrow:  "Session replay",
			Title:    "What the reporter saw",
			RRWeb:    ClipFilename,
			Chapters: "auto",
			Caption:  "Deterministic DOM replay captured at report time — plays in your browser, no download.",
		})
	} else {
		scenes = append(scenes, scene{
			Type:    "narrative",
			Eyebrow: "Session replay",
			Lede:    "No rrweb replay was attached to this report.",
			Body:    "This bug report did not carry a session replay, so there is nothing to play back here. The network evidence below is still summarized from the captured HAR.",
		})
	}

	rows, total := summarizeHAR(ev.HAR)
	if len(rows) > 0 {
		caption := "Captured request/response trace leading up to the report."
		if total > len(rows) {
			caption = fmt.Sprintf("Showing the first %d of %d captured exchanges leading up to the report.", len(rows), total)
		}
		scenes = append(scenes, scene{
			Type:    "table",
			Variant: "data",
			Title:   "Network summary (HAR)",
			Columns: []string{"Method", "Path", "Status", "Time"},
			Rows:    rows,
			Caption: caption,
		})
	} else {
		scenes = append(scenes, scene{
			Type:    "narrative",
			Eyebrow: "Network",
			Lede:    "No HAR network trace was attached to this report.",
			Body:    "There was no captured request/response trace to summarize for this report.",
		})
	}

	tagline := "Evidence-first bug reports — no LLM in the loop"
	if n := strings.TrimSpace(ev.IssueNumber); n != "" {
		tagline = fmt.Sprintf("Issue #%s · evidence-first, no LLM in the loop", n)
	}
	cta := scene{Type: "cta", Wordmark: "kitsoki", Tagline: tagline}
	if u := strings.TrimSpace(ev.IssueURL); u != "" {
		cta.URL = u
	}
	scenes = append(scenes, cta)

	doc := deckSpec{
		Meta: specMeta{
			Title:      title,
			Mode:       "pitch", // required by slidey or scenes render blank
			Theme:      "rose-pine-moon",
			Resolution: &resolution{Width: 1920, Height: 1080},
		},
		Scenes: scenes,
	}
	spec, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal deck spec: %w", err)
	}
	spec = append(spec, '\n')
	return spec, clip, nil
}

// summarizeHAR parses a scrubbed HAR and returns up to [maxHARRows] table rows
// (Method, Path, Status, Time) in capture order, plus the total entry count so
// the caller can note truncation. Best-effort: invalid/empty input yields no
// rows. The Path column prefers the JSON-RPC method name embedded in the request
// body (so `/rpc · session.get` reads better than a bare `/rpc`).
func summarizeHAR(harBytes []byte) (rows []tableRow, total int) {
	if len(harBytes) == 0 {
		return nil, 0
	}
	var har harscrub.Har
	if err := json.Unmarshal(harBytes, &har); err != nil {
		return nil, 0
	}
	total = len(har.Log.Entries)
	for _, e := range har.Log.Entries {
		if len(rows) >= maxHARRows {
			break
		}
		method := strings.TrimSpace(e.Request.Method)
		if method == "" {
			method = "—"
		}
		path := harPath(e.Request.URL)
		if rpc := rpcMethod(e.Request.PostData); rpc != "" {
			path = path + " · " + rpc
		}
		status := "—"
		if e.Response.Status > 0 {
			status = fmt.Sprintf("%d", e.Response.Status)
		}
		rows = append(rows, tableRow{Cells: []string{method, path, status, fmt.Sprintf("%.0fms", e.Time)}})
	}
	return rows, total
}

// harPath reduces a request URL to its path for the summary table, tolerating
// both absolute URLs and bare paths.
func harPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "—"
	}
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		return u.Path
	}
	return raw
}

// rpcMethod extracts the JSON-RPC "method" from a request body, or "" when the
// body is absent or not a JSON-RPC envelope.
func rpcMethod(pd *harscrub.PostData) string {
	if pd == nil || strings.TrimSpace(pd.Text) == "" {
		return ""
	}
	var env struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal([]byte(pd.Text), &env); err != nil {
		return ""
	}
	return strings.TrimSpace(env.Method)
}

// --- slidey spec shape (a deliberately small subset; verified against real
// decks under docs/decks/*.slidey.json) ---

type deckSpec struct {
	Meta   specMeta `json:"meta"`
	Scenes []scene  `json:"scenes"`
}

type specMeta struct {
	Title      string      `json:"title"`
	Mode       string      `json:"mode"`
	Theme      string      `json:"theme,omitempty"`
	Resolution *resolution `json:"resolution,omitempty"`
}

type resolution struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// scene is the union of the few scene types this package emits (title, video,
// table, narrative, cta). omitempty keeps each rendered scene to just its
// relevant fields.
type scene struct {
	Type     string     `json:"type"`
	Eyebrow  string     `json:"eyebrow,omitempty"`
	Title    string     `json:"title,omitempty"`
	Subtitle string     `json:"subtitle,omitempty"`
	Caption  string     `json:"caption,omitempty"`
	Lede     string     `json:"lede,omitempty"`     // narrative
	Body     string     `json:"body,omitempty"`     // narrative
	Mode     string     `json:"mode,omitempty"`     // video
	RRWeb    string     `json:"rrweb,omitempty"`    // video
	Chapters string     `json:"chapters,omitempty"` // video
	Variant  string     `json:"variant,omitempty"`  // table
	Columns  []string   `json:"columns,omitempty"`  // table
	Rows     []tableRow `json:"rows,omitempty"`     // table
	Wordmark string     `json:"wordmark,omitempty"` // cta
	Tagline  string     `json:"tagline,omitempty"`  // cta
	URL      string     `json:"url,omitempty"`      // cta
}

type tableRow struct {
	Cells []string `json:"cells"`
}
