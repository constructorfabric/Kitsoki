package reportcontract

import "strings"

// Kind identifies the durable report intent. It is stored in result metadata
// and provider payloads so downstream ticket systems can route bug reports and
// continuous-improvement reports differently while preserving one evidence
// bundle shape.
type Kind string

const (
	// KindBug is a defect or broken-expectation report.
	KindBug Kind = "bug"
	// KindMetaImprove is a continuous-improvement report produced from meta
	// introspection.
	KindMetaImprove Kind = "meta-improve"
)

// String returns the stable wire spelling for k.
func (k Kind) String() string { return string(k) }

// Labels returns default routing labels for remote sinks. A fresh slice is
// returned so callers can append provider-specific labels without mutating the
// shared contract.
func (k Kind) Labels() []string {
	switch k {
	case KindMetaImprove:
		return []string{"kitsoki", "meta-improve"}
	case KindBug:
		return []string{"kitsoki", "bug"}
	default:
		if strings.TrimSpace(string(k)) == "" {
			return []string{"kitsoki"}
		}
		return []string{"kitsoki", string(k)}
	}
}

// ReviewFocus returns the short evidence-review rubric for k. It is prose, not
// a parser contract, and is kept here so filed reports and agent prompts do not
// drift on what "improve" means.
func (k Kind) ReviewFocus() string {
	switch k {
	case KindMetaImprove:
		return "false starts, unexpected outputs, prompt/tool/script changes, permission cleanup, and no-LLM regression coverage"
	case KindBug:
		return "expected behavior, actual behavior, reproducible steps, affected state/component, and evidence needed to fix the defect"
	default:
		return "evidence, owner, next action, and regression coverage"
	}
}

// Destination identifies where a report should be posted after evidence is
// captured. The zero value is not meaningful; use NormalizeDestination for
// operator or RPC input.
type Destination string

const (
	// DestinationConfigured uses the server's configured sink: GitHub when a
	// ticket repo is configured, otherwise local artifacts.
	DestinationConfigured Destination = "configured"
	// DestinationLocal forces a local artifact report.
	DestinationLocal Destination = "local"
	// DestinationTicketProvider writes local evidence first, then posts through
	// a configured ticket_provider/v1 script.
	DestinationTicketProvider Destination = "ticket-provider"
)

// NormalizeDestination maps UI aliases and empty input to the canonical
// destination spellings used by RPC results and provider payloads.
func NormalizeDestination(raw string) Destination {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "configured", "default":
		return DestinationConfigured
	case "local", "local-artifact":
		return DestinationLocal
	case "provider", "ticket-provider", "ticket_provider":
		return DestinationTicketProvider
	default:
		return DestinationConfigured
	}
}

// String returns the stable wire spelling for d.
func (d Destination) String() string { return string(d) }

// PostingDescription renders the destination in report prose. It deliberately
// avoids naming a specific provider unless the destination requires one.
func (d Destination) PostingDescription() string {
	switch d {
	case DestinationLocal:
		return "Requested destination: local artifact report."
	case DestinationTicketProvider:
		return "Requested destination: configured ticket provider after local evidence capture."
	default:
		return "Requested destination: configured sink (GitHub when `--ticket-repo` is set, otherwise local artifact report)."
	}
}

const (
	// ToolRead lets a report agent inspect explicit files from the context.
	ToolRead = "Read"
	// ToolGlob lets a report agent enumerate story/report files without shell.
	ToolGlob = "Glob"
	// ToolGrep lets a report agent search prompts, scripts, traces, and prior
	// reports without shell.
	ToolGrep = "Grep"
	// ToolBugCreate is the only side-effecting tool the bug-reporting meta
	// agents need: the CLI owns markdown creation and target-root policy.
	ToolBugCreate = "Bash(kitsoki bug create*)"
)

// ReadOnlyTools returns the canonical allowlist for report reviewers that must
// inspect but not mutate. A fresh slice is returned for caller-local edits.
func ReadOnlyTools() []string {
	return []string{ToolRead, ToolGlob, ToolGrep}
}

// BugFilerTools returns the canonical allowlist for meta bug reporters. It is
// ReadOnlyTools plus the single bug-create command pattern.
func BugFilerTools() []string {
	return []string{ToolRead, ToolGlob, ToolGrep, ToolBugCreate}
}

const (
	// ArtifactScreenshot is the optional browser screenshot sidecar.
	ArtifactScreenshot = "screenshot.png"
	// ArtifactHAR is the scrubbed browser or server-recorder network capture.
	ArtifactHAR = "har.json"
	// ArtifactRRWeb is the browser session replay sidecar.
	ArtifactRRWeb = "rrweb.json"
	// ArtifactConsole is the recent console and page-error sidecar.
	ArtifactConsole = "console.json"
	// ArtifactTrace is the redacted Kitsoki session trace sidecar.
	ArtifactTrace = "trace.redacted.jsonl"

	// LabelScreenshot is the human label for ArtifactScreenshot.
	LabelScreenshot = "Screenshot"
	// LabelHAR is the human label for ArtifactHAR.
	LabelHAR = "HAR capture (scrubbed)"
	// LabelRRWeb is the human label for ArtifactRRWeb.
	LabelRRWeb = "Session replay (rrweb)"
	// LabelConsole is the human label for ArtifactConsole.
	LabelConsole = "Console log"
	// LabelTrace is the human label for ArtifactTrace.
	LabelTrace = "Depersonalized session trace (redacted)"
)

// EvidenceArtifact describes one standard browser-backed evidence sidecar.
// It contains only metadata; callers supply bytes after their own capture and
// scrubbing path has run.
type EvidenceArtifact struct {
	Name  string
	Label string
	Image bool
}

// BrowserEvidenceArtifacts returns the evidence sidecar set in report order.
// A fresh slice is returned so callers can filter without affecting others.
func BrowserEvidenceArtifacts() []EvidenceArtifact {
	return []EvidenceArtifact{
		{Name: ArtifactScreenshot, Label: LabelScreenshot, Image: true},
		{Name: ArtifactHAR, Label: LabelHAR},
		{Name: ArtifactRRWeb, Label: LabelRRWeb},
		{Name: ArtifactConsole, Label: LabelConsole},
		{Name: ArtifactTrace, Label: LabelTrace},
	}
}

// ExpectedBrowserEvidenceSentence is the short report-body description of the
// standard sidecars. It is shared so bug and improve reports point readers at
// the same filenames.
func ExpectedBrowserEvidenceSentence() string {
	return "Expected artifacts: scrubbed HAR (`har.json`), session replay (`rrweb.json` when browser capture is available), recent console (`console.json` when present), and redacted trace (`trace.redacted.jsonl` when the session can be resolved)."
}
