// qa.go provides the project-local journey-pack lifecycle.  It deliberately
// stays a composition layer: scenario, storyboard, flow, and tutorial formats
// retain their own owners and schemas.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"kitsoki/internal/storyboard"
)

const journeyPackSchema = "kitsoki/journey-pack/v1"

type journeyPack struct {
	SourcePath string `yaml:"-"`
	Schema     string `yaml:"schema"`
	ID         string `yaml:"id"`
	Title      string `yaml:"title"`
	Story      struct {
		App   string `yaml:"app"`
		Entry string `yaml:"entry"`
	} `yaml:"story"`
	Catalogs struct {
		Personas  []string `yaml:"personas"`
		Scenarios []string `yaml:"scenarios"`
	} `yaml:"catalogs"`
	Matrix struct {
		Personas   []string `yaml:"personas"`
		Scenarios  []string `yaml:"scenarios"`
		Transports []string `yaml:"transports"`
	} `yaml:"matrix"`
	Freeze struct {
		RequireRealOriginTrace bool `yaml:"require_real_origin_trace"`
		DeriveFlow             bool `yaml:"derive_flow"`
		DeriveHostCassette     bool `yaml:"derive_host_cassette"`
		ReplayRequired         bool `yaml:"replay_required"`
	} `yaml:"freeze"`
	Outputs struct {
		Storyboard string `yaml:"storyboard"`
		Tutorial   struct {
			Template string `yaml:"template"`
			Publish  string `yaml:"publish"`
		} `yaml:"tutorial"`
		Deck struct {
			Publish string `yaml:"publish"`
		} `yaml:"deck"`
		Tour struct {
			RequireMP4 bool   `yaml:"require_mp4"`
			Publish    string `yaml:"publish"`
		} `yaml:"tour"`
	} `yaml:"outputs"`
	Gate struct {
		StoryStructure     string `yaml:"story_structure"`
		PerTransportJudge  string `yaml:"per_transport_judge"`
		TutorialValidation string `yaml:"tutorial_validation"`
		StoryboardDrift    string `yaml:"storyboard_drift"`
		EvidenceFreshness  string `yaml:"evidence_freshness"`
		DegradedEvidence   string `yaml:"degraded_evidence"`
	} `yaml:"gate"`
	Proof struct {
		OriginPrompt string   `yaml:"origin_prompt"`
		OriginRun    string   `yaml:"origin_run"`
		Response     []string `yaml:"response"`
		Delivered    []string `yaml:"delivered"`
		Boundaries   []string `yaml:"boundaries"`
	} `yaml:"proof"`
}

func qaCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "qa", Short: "Validate and publish project-local journey packs"}
	cmd.AddCommand(qaBootstrapCmd(), qaValidateCmd(), qaPreviewCmd(), qaCheckCmd(), qaFreezeCmd(), qaProduceCmd(), qaStatusCmd(), qaPublishCmd(), qaVerifyCmd())
	return cmd
}

// qaBootstrapCmd creates only portable source files. It intentionally does not
// create a workspace, contact a provider, or manufacture evidence.
func qaBootstrapCmd() *cobra.Command {
	var dir, id, story string
	cmd := &cobra.Command{Use: "bootstrap", Short: "Create a portable project-local onboarding journey-pack skeleton", SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		if dir == "" {
			dir = ".kitsoki/qa/journeys/onboarding"
		}
		if id == "" {
			id = "local/onboarding"
		}
		if story == "" {
			story = ".kitsoki/stories/project/app.yaml"
		}
		if filepath.IsAbs(dir) || filepath.IsAbs(story) || strings.HasPrefix(filepath.Clean(dir), "..") || strings.HasPrefix(filepath.Clean(story), "..") {
			return fmt.Errorf("bootstrap paths must be repo-relative")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		journey := filepath.Join(dir, "journey.yaml")
		if fileExists(journey) {
			return fmt.Errorf("journey pack already exists: %s", journey)
		}
		body := fmt.Sprintf("schema: %s\nid: %s\ntitle: Project onboarding\nstory:\n  app: %s\n  entry: core.landing\ncatalogs:\n  personas: [\"@kitsoki/product-journey/personas\", ../../personas]\n  scenarios: [\"@kitsoki/product-journey/scenarios\", ../../scenarios]\nmatrix:\n  personas: [docs-minded-contributor]\n  scenarios: [project-onboarding]\n  transports: [web]\nfreeze:\n  require_real_origin_trace: true\n  derive_flow: true\n  derive_host_cassette: true\n  replay_required: true\noutputs:\n  storyboard: %s/onboarding.storyboard.yaml\n  tutorial:\n    template: %s/tutorial.md.tmpl\n    publish: docs/tutorials/kitsoki-onboarding.md\n  deck:\n    publish: docs/decks/kitsoki-onboarding.slidey.json\n  tour:\n    require_mp4: true\n    publish: docs/media/kitsoki-onboarding.mp4\ngate:\n  story_structure: required\n  per_transport_judge: required\n  tutorial_validation: required\n  storyboard_drift: required\n  evidence_freshness: required\n  degraded_evidence: block_publish\n", journeyPackSchema, id, story, dir, dir)
		if err := os.WriteFile(journey, []byte(body), 0o644); err != nil {
			return err
		}
		qaRoot := filepath.Clean(filepath.Join(dir, "..", ".."))
		if err := os.MkdirAll(filepath.Join(qaRoot, "personas"), 0o755); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(qaRoot, "scenarios"), 0o755); err != nil {
			return err
		}
		catalog := "schema: kitsoki/journey-catalog/v1\nimports:\n  personas: [\"@kitsoki/product-journey/personas\", personas]\n  scenarios: [\"@kitsoki/product-journey/scenarios\", scenarios]\n"
		if err := os.WriteFile(filepath.Join(qaRoot, "catalog.yaml"), []byte(catalog), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "tutorial.md.tmpl"), []byte("# Project onboarding\n\n<!-- kitsoki:generated:start -->\n<!-- kitsoki:generated:end -->\n"), 0o644); err != nil {
			return err
		}
		storyboard := fmt.Sprintf("version: 1\nid: onboarding\ntitle: Project onboarding\ngoal: Show the project-local Kitsoki entry point.\nsurface: web\nformat: tour\nminTotalMs: 1200\nbinding:\n  story: %s\nscenes:\n  - id: orientation\n    title: Project-local entry point\n    purpose: Establish the first useful screen.\n    narration: Start from the project-local story.\n    screen: The story landing state is visible.\n    dwellMs: 1200\n    expect: [A useful next action is visible.]\n", story)
		if err := os.WriteFile(filepath.Join(dir, "onboarding.storyboard.yaml"), []byte(storyboard), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", journey)
		return nil
	}}
	cmd.Flags().StringVar(&dir, "dir", ".kitsoki/qa/journeys/onboarding", "repo-relative journey directory")
	cmd.Flags().StringVar(&id, "id", "local/onboarding", "journey identifier")
	cmd.Flags().StringVar(&story, "story", ".kitsoki/stories/project/app.yaml", "repo-relative story app")
	return cmd
}

func qaValidateCmd() *cobra.Command {
	return &cobra.Command{Use: "validate <journey.yaml>", Short: "Validate a journey-pack manifest and its local references", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		pack, root, digest, err := loadJourney(args[0])
		if err != nil {
			return err
		}
		if err := validateJourney(pack, root); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "✓ %s — valid (%s)\n", pack.ID, digest)
		return nil
	}}
}

func qaPreviewCmd() *cobra.Command {
	return &cobra.Command{Use: "preview <journey.yaml>", Short: "Print the side-effect-free persona × scenario × transport plan", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		pack, root, _, err := loadJourney(args[0])
		if err != nil {
			return err
		}
		if err := validateJourney(pack, root); err != nil {
			return err
		}
		for _, persona := range pack.Matrix.Personas {
			for _, scenario := range pack.Matrix.Scenarios {
				for _, transport := range pack.Matrix.Transports {
					fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", persona, scenario, transport)
				}
			}
		}
		return nil
	}}
}

type journeyCheckReceipt struct {
	Schema        string            `json:"schema"`
	Journey       string            `json:"journey"`
	Result        string            `json:"result"`
	Flow          string            `json:"flow"`
	Trace         string            `json:"trace"`
	GeneratedAt   time.Time         `json:"generated_at"`
	SourceDigests map[string]string `json:"source_digests"`
}

type journeyFreezeReceipt struct {
	Schema  string `json:"schema"`
	Journey string `json:"journey"`
	Result  string `json:"result"`
	Origin  struct {
		Kind  string `json:"kind"`
		Trace string `json:"trace"`
	} `json:"origin"`
	Replay struct {
		Flow     string `json:"flow"`
		Cassette string `json:"cassette"`
		Status   string `json:"status"`
	} `json:"replay"`
	GeneratedAt   time.Time         `json:"generated_at"`
	SourceDigests map[string]string `json:"source_digests"`
}

// qaCheckCmd deliberately accepts a frozen flow rather than silently starting
// an LLM-backed session. Exploratory real runs remain scenario-qa's interactive
// responsibility; this command is the no-spend replay gate used by CI.
func qaCheckCmd() *cobra.Command {
	var flow, out string
	cmd := &cobra.Command{Use: "check <journey.yaml>", Short: "Run a no-LLM replay check for a journey pack", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		p, root, digest, err := loadJourney(args[0])
		if err != nil {
			return err
		}
		if err = validateJourney(p, root); err != nil {
			return err
		}
		if flow == "" {
			return fmt.Errorf("--flow is required: check never substitutes a live/LLM run")
		}
		flow, err = resolveJourneyPath(root, flow)
		if err != nil {
			return err
		}
		if !fileExists(flow) {
			return fmt.Errorf("flow not found: %s", flow)
		}
		if out == "" {
			out = defaultJourneyArtifacts(root, p.ID, digest)
		}
		if err = os.MkdirAll(out, 0o755); err != nil {
			return err
		}
		trace := filepath.Join(out, "replay.trace.jsonl")
		app := filepath.Join(root, p.Story.App)
		if err = runJourneyKitsoki(cmd, root, "test", "flows", app, "--flows", flow, "--trace-out", trace); err != nil {
			return err
		}
		r := journeyCheckReceipt{Schema: "kitsoki/journey-check-receipt/v1", Journey: p.ID, Result: "passed", Flow: repoRelative(root, flow), Trace: repoRelative(root, trace), GeneratedAt: time.Now().UTC(), SourceDigests: journeyDigests(root, p, digest)}
		return writeJourneyJSON(filepath.Join(out, "check-receipt.json"), r, cmd)
	}}
	cmd.Flags().StringVar(&flow, "flow", "", "repo-relative frozen flow fixture")
	cmd.Flags().StringVar(&out, "out", "", "artifact directory (default: .artifacts/kitsoki-qa/<journey>/<digest>)")
	return cmd
}

func qaFreezeCmd() *cobra.Command {
	var originTrace, flow, cassette, out, originKind string
	cmd := &cobra.Command{Use: "freeze <journey.yaml>", Short: "Pin a real or demo origin to a replay-tested flow and cassette", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		p, root, digest, err := loadJourney(args[0])
		if err != nil {
			return err
		}
		if err = validateJourney(p, root); err != nil {
			return err
		}
		if originTrace == "" || flow == "" {
			return fmt.Errorf("--origin-trace and --flow are required")
		}
		if originKind != "real" && originKind != "demo" {
			return fmt.Errorf("--origin-kind must be real or demo")
		}
		originTrace, err = resolveJourneyPath(root, originTrace)
		if err != nil {
			return err
		}
		flow, err = resolveJourneyPath(root, flow)
		if err != nil {
			return err
		}
		if !fileExists(originTrace) || !fileExists(flow) {
			return fmt.Errorf("origin trace and flow must exist")
		}
		if p.Freeze.DeriveHostCassette {
			if cassette == "" {
				return fmt.Errorf("--cassette is required by freeze policy")
			}
			cassette, err = resolveJourneyPath(root, cassette)
			if err != nil {
				return err
			}
			if !fileExists(cassette) {
				return fmt.Errorf("cassette not found: %s", cassette)
			}
		}
		if out == "" {
			out = defaultJourneyArtifacts(root, p.ID, digest)
		}
		if err = os.MkdirAll(out, 0o755); err != nil {
			return err
		}
		replayTrace := filepath.Join(out, "freeze-replay.trace.jsonl")
		if err = runJourneyKitsoki(cmd, root, "test", "flows", filepath.Join(root, p.Story.App), "--flows", flow, "--trace-out", replayTrace); err != nil {
			return fmt.Errorf("clean replay failed: %w", err)
		}
		if err = verifyJourneyReplay(originTrace, replayTrace); err != nil {
			return fmt.Errorf("clean replay diverged: %w", err)
		}
		var r journeyFreezeReceipt
		r.Schema = "kitsoki/journey-freeze-receipt/v1"
		r.Journey = p.ID
		r.Result = "passed"
		r.Origin.Kind = originKind
		r.Origin.Trace = repoRelative(root, originTrace)
		r.Replay.Flow = repoRelative(root, flow)
		r.Replay.Cassette = repoRelative(root, cassette)
		r.Replay.Status = "passed"
		r.GeneratedAt = time.Now().UTC()
		r.SourceDigests = journeyDigests(root, p, digest)
		return writeJourneyJSON(filepath.Join(out, "freeze-receipt.json"), r, cmd)
	}}
	cmd.Flags().StringVar(&originTrace, "origin-trace", "", "accepted origin trace (real or explicitly demo)")
	cmd.Flags().StringVar(&originKind, "origin-kind", "real", "origin classification: real or demo")
	cmd.Flags().StringVar(&flow, "flow", "", "repo-relative replay flow")
	cmd.Flags().StringVar(&cassette, "cassette", "", "repo-relative host cassette")
	cmd.Flags().StringVar(&out, "out", "", "artifact directory")
	return cmd
}

func qaProduceCmd() *cobra.Command {
	var artifacts string
	cmd := &cobra.Command{Use: "produce <journey.yaml>", Short: "Generate tutorial and tour inputs from a passed freeze receipt", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		p, root, digest, err := loadJourney(args[0])
		if err != nil {
			return err
		}
		if err = validateJourney(p, root); err != nil {
			return err
		}
		if artifacts == "" {
			artifacts = defaultJourneyArtifacts(root, p.ID, digest)
		}
		freezePath := filepath.Join(artifacts, "freeze-receipt.json")
		var freeze journeyFreezeReceipt
		if err = readJSON(freezePath, &freeze); err != nil {
			return fmt.Errorf("read passed freeze receipt: %w", err)
		}
		if freeze.Result != "passed" {
			return fmt.Errorf("freeze receipt is not passed")
		}
		if !sameDigests(freeze.SourceDigests, journeyDigests(root, p, digest)) {
			return fmt.Errorf("freeze receipt is stale for the current journey sources")
		}
		if p.Outputs.Tutorial.Template == "" || p.Outputs.Storyboard == "" {
			return fmt.Errorf("journey requires tutorial.template and outputs.storyboard")
		}
		tmpl, err := os.ReadFile(filepath.Join(root, p.Outputs.Tutorial.Template))
		if err != nil {
			return err
		}
		generated := "<!-- kitsoki:generated:start -->\n\n## Verified replay\n\nRun `kitsoki qa check " + repoRelative(root, args[0]) + " --flow " + freeze.Replay.Flow + "` to execute the no-LLM replay.\n\n- Journey: `" + p.ID + "`\n- Origin: `" + freeze.Origin.Kind + "`\n- Frozen flow: `" + freeze.Replay.Flow + "`\n\n<!-- kitsoki:generated:end -->"
		text := replaceGeneratedRegion(string(tmpl), generated)
		if err = validateJourneyTutorial(text); err != nil {
			return fmt.Errorf("generated tutorial: %w", err)
		}
		if err = os.WriteFile(filepath.Join(artifacts, "tutorial.generated.md"), []byte(text), 0o644); err != nil {
			return err
		}
		if err = runJourneyKitsoki(cmd, root, "storyboard", "emit", "tour", filepath.Join(root, p.Outputs.Storyboard), "--root", root, "--out", filepath.Join(artifacts, "tour.yaml")); err != nil {
			return err
		}
		deckPath := filepath.Join(artifacts, "deck.slidey.json")
		deck := journeyDeck(p, freeze, digest)
		if err = writeJourneyJSON(deckPath, deck, cmd); err != nil {
			return err
		}
		if err = validateJourneyDeck(cmd, deckPath); err != nil {
			return err
		}
		tutorial := struct {
			Status        string            `json:"status"`
			Digest        string            `json:"digest"`
			SourceDigests map[string]string `json:"source_digests"`
		}{Status: "passed", Digest: digestBytes([]byte(text)), SourceDigests: journeyDigests(root, p, digest)}
		if err = writeJourneyJSON(filepath.Join(artifacts, "tutorial-receipt.json"), tutorial, cmd); err != nil {
			return err
		}
		return writeJourneyJSON(filepath.Join(artifacts, "deck-receipt.json"), struct {
			Status        string            `json:"status"`
			Digest        string            `json:"digest"`
			SourceDigests map[string]string `json:"source_digests"`
		}{Status: "passed", Digest: digestFile(deckPath), SourceDigests: journeyDigests(root, p, digest)}, cmd)
	}}
	cmd.Flags().StringVar(&artifacts, "artifacts", "", "artifact directory")
	return cmd
}

func qaStatusCmd() *cobra.Command {
	var artifacts string
	cmd := &cobra.Command{Use: "status <journey.yaml>", Short: "Show the truthful lifecycle state of a journey pack", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		p, root, digest, err := loadJourney(args[0])
		if err != nil {
			return err
		}
		if err = validateJourney(p, root); err != nil {
			return err
		}
		if artifacts == "" {
			artifacts = defaultJourneyArtifacts(root, p.ID, digest)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "journey: %s\nartifacts: %s\n", p.ID, repoRelative(root, artifacts))
		for _, name := range []string{"check-receipt.json", "freeze-receipt.json", "tutorial-receipt.json", "deck-receipt.json", "release-receipt.json"} {
			path := filepath.Join(artifacts, name)
			if fileExists(path) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: present\n", name)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: missing\n", name)
			}
		}
		return nil
	}}
	cmd.Flags().StringVar(&artifacts, "artifacts", "", "artifact directory")
	return cmd
}

// qaPublishCmd is intentionally a gate, not a renderer. It copies only
// generated artifacts that already have a passed deterministic receipt and
// writes a receipt which qa verify recomputes from the source digest.
func qaPublishCmd() *cobra.Command {
	var artifacts, tour, deck, chapters string
	cmd := &cobra.Command{Use: "publish <journey.yaml>", Short: "Publish a release only when real-origin replay and generated products are current", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		p, root, digest, err := loadJourney(args[0])
		if err != nil {
			return err
		}
		if err = validateJourney(p, root); err != nil {
			return err
		}
		if artifacts == "" {
			artifacts = defaultJourneyArtifacts(root, p.ID, digest)
		}
		var freeze journeyFreezeReceipt
		if err = readJSON(filepath.Join(artifacts, "freeze-receipt.json"), &freeze); err != nil {
			return err
		}
		if freeze.Result != "passed" || freeze.Origin.Kind != "real" || freeze.Replay.Status != "passed" {
			return fmt.Errorf("publish requires a passed real-origin freeze receipt")
		}
		if !sameDigests(freeze.SourceDigests, journeyDigests(root, p, digest)) {
			return fmt.Errorf("publish requires a current freeze receipt")
		}
		var tutorial struct {
			Status        string            `json:"status"`
			SourceDigests map[string]string `json:"source_digests"`
		}
		if err = readJSON(filepath.Join(artifacts, "tutorial-receipt.json"), &tutorial); err != nil || tutorial.Status != "passed" || !sameDigests(tutorial.SourceDigests, journeyDigests(root, p, digest)) {
			return fmt.Errorf("publish requires a passed tutorial receipt")
		}
		var deckReceipt struct {
			Status        string            `json:"status"`
			SourceDigests map[string]string `json:"source_digests"`
		}
		if err = readJSON(filepath.Join(artifacts, "deck-receipt.json"), &deckReceipt); err != nil || deckReceipt.Status != "passed" || !sameDigests(deckReceipt.SourceDigests, journeyDigests(root, p, digest)) {
			return fmt.Errorf("publish requires a passed Slidey deck receipt")
		}
		if tour == "" {
			tour = filepath.Join(artifacts, "tour.mp4")
		}
		if deck == "" {
			deck = filepath.Join(artifacts, "deck.slidey.json")
		}
		if p.Outputs.Tour.RequireMP4 && !fileExists(tour) {
			return fmt.Errorf("publish requires a rendered tour MP4: %s", tour)
		}
		if p.Outputs.Storyboard != "" {
			if chapters == "" {
				chapters = tour + ".chapters.json"
			}
			sb, loadErr := storyboard.Load(filepath.Join(root, p.Outputs.Storyboard))
			if loadErr != nil {
				return loadErr
			}
			issues, checkErr := storyboard.CheckChapters(sb, chapters)
			if checkErr != nil {
				return checkErr
			}
			if len(issues) > 0 {
				return fmt.Errorf("publish requires a storyboard capture with no drift: %s", issues[0])
			}
		}
		if p.Outputs.Deck.Publish != "" && !fileExists(deck) {
			return fmt.Errorf("publish requires a proof-aware Slidey deck: %s", deck)
		}
		if err = copyFile(filepath.Join(artifacts, "tutorial.generated.md"), filepath.Join(root, p.Outputs.Tutorial.Publish)); err != nil {
			return err
		}
		if err = copyFile(tour, filepath.Join(root, p.Outputs.Tour.Publish)); err != nil {
			return err
		}
		if err = copyFile(deck, filepath.Join(root, p.Outputs.Deck.Publish)); err != nil {
			return err
		}
		r := journeyReceipt{Schema: "kitsoki/journey-release-receipt/v1", Journey: p.ID, Result: "ready", GeneratedAt: time.Now().UTC(), SourceDigests: journeyDigests(root, p, digest)}
		return writeJourneyJSON(filepath.Join(artifacts, "release-receipt.json"), r, cmd)
	}}
	cmd.Flags().StringVar(&artifacts, "artifacts", "", "artifact directory")
	cmd.Flags().StringVar(&tour, "tour", "", "rendered tour MP4")
	cmd.Flags().StringVar(&chapters, "chapters", "", "tour chapter sidecar (default: <tour>.chapters.json)")
	cmd.Flags().StringVar(&deck, "deck", "", "proof-aware Slidey deck")
	return cmd
}

func qaVerifyCmd() *cobra.Command {
	var receipt string
	cmd := &cobra.Command{Use: "verify <journey.yaml>", Short: "Fail closed unless the journey manifest and release receipt are current and ready", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		pack, root, digest, err := loadJourney(args[0])
		if err != nil {
			return err
		}
		if err := validateJourney(pack, root); err != nil {
			return err
		}
		if receipt == "" {
			receipt = filepath.Join(defaultJourneyArtifacts(root, pack.ID, digest), "release-receipt.json")
		}
		raw, err := os.ReadFile(receipt)
		if err != nil {
			return fmt.Errorf("release receipt %q: %w", receipt, err)
		}
		var r struct {
			Schema        string            `json:"schema"`
			Journey       string            `json:"journey"`
			Result        string            `json:"result"`
			SourceDigests map[string]string `json:"source_digests"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return fmt.Errorf("parse release receipt: %w", err)
		}
		if r.Schema != "kitsoki/journey-release-receipt/v1" || r.Journey != pack.ID || r.Result != "ready" || !sameDigests(r.SourceDigests, journeyDigests(root, pack, digest)) {
			return fmt.Errorf("release receipt is not ready/current for %s", pack.ID)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "✓ %s — release ready\n", pack.ID)
		return nil
	}}
	cmd.Flags().StringVar(&receipt, "receipt", "", "release receipt path (default: current journey artifact directory)")
	return cmd
}

func loadJourney(path string) (*journeyPack, string, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", "", fmt.Errorf("read journey pack: %w", err)
	}
	var p journeyPack
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, "", "", fmt.Errorf("parse journey pack: %w", err)
	}
	p.SourcePath = path
	manifestDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, "", "", err
	}
	root, err := findJourneyRepoRoot(manifestDir, p.Story.App)
	if err != nil {
		return nil, "", "", err
	}
	sum := sha256.Sum256(raw)
	return &p, root, hex.EncodeToString(sum[:]), nil
}

func validateJourney(p *journeyPack, root string) error {
	if p.Schema != journeyPackSchema || p.ID == "" || p.Title == "" || p.Story.App == "" {
		return fmt.Errorf("journey pack requires schema %q, id, title, and story.app", journeyPackSchema)
	}
	for _, ref := range []string{p.Story.App, p.Outputs.Storyboard, p.Outputs.Tutorial.Template, p.Outputs.Tutorial.Publish, p.Outputs.Deck.Publish, p.Outputs.Tour.Publish} {
		if ref == "" {
			continue
		}
		if strings.HasPrefix(ref, "@kitsoki/") {
			continue
		}
		if filepath.IsAbs(ref) || strings.HasPrefix(filepath.Clean(ref), ".."+string(filepath.Separator)) {
			return fmt.Errorf("journey reference must be repo-relative: %q", ref)
		}
	}
	app := filepath.Join(root, p.Story.App)
	if !fileExists(app) {
		return fmt.Errorf("story.app not found: %s", p.Story.App)
	}
	for _, ref := range []string{p.Outputs.Storyboard, p.Outputs.Tutorial.Template} {
		if ref != "" && !fileExists(filepath.Join(root, ref)) {
			return fmt.Errorf("journey source reference not found: %s", ref)
		}
	}
	for _, ref := range append(p.Catalogs.Personas, p.Catalogs.Scenarios...) {
		if ref == "" || strings.HasPrefix(ref, "@kitsoki/") {
			continue
		}
		if filepath.IsAbs(ref) {
			return fmt.Errorf("catalog reference must be relative: %q", ref)
		}
	}
	if len(p.Matrix.Personas) == 0 || len(p.Matrix.Scenarios) == 0 || len(p.Matrix.Transports) == 0 {
		return fmt.Errorf("journey matrix must declare personas, scenarios, and transports")
	}
	for _, t := range p.Matrix.Transports {
		if t != "web" && t != "tui" && t != "vscode" {
			return fmt.Errorf("unsupported journey transport %q", t)
		}
	}
	if err := validateJourneyCatalogs(p); err != nil {
		return err
	}
	if p.Gate.DegradedEvidence != "" && p.Gate.DegradedEvidence != "block_publish" {
		return fmt.Errorf("gate.degraded_evidence must be block_publish when set")
	}
	return nil
}

func validateJourneyCatalogs(p *journeyPack) error {
	personas, err := loadJourneyCatalogIDs(filepath.Dir(p.SourcePath), p.Catalogs.Personas, "personas")
	if err != nil {
		return err
	}
	scenarios, err := loadJourneyCatalogIDs(filepath.Dir(p.SourcePath), p.Catalogs.Scenarios, "scenarios")
	if err != nil {
		return err
	}
	for _, id := range p.Matrix.Personas {
		if !personas[id] {
			return fmt.Errorf("matrix persona %q is not provided by the configured catalogs", id)
		}
	}
	for _, id := range p.Matrix.Scenarios {
		if !scenarios[id] {
			return fmt.Errorf("matrix scenario %q is not provided by the configured catalogs", id)
		}
	}
	return nil
}

func loadJourneyCatalogIDs(manifestDir string, refs []string, collection string) (map[string]bool, error) {
	ids := map[string]bool{}
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		path := ""
		if strings.HasPrefix(ref, "@kitsoki/product-journey/") {
			repo := os.Getenv("KITSOKI_REPO")
			if repo == "" {
				return nil, fmt.Errorf("catalog %q requires KITSOKI_REPO or --kitsoki-repo to resolve %s", ref, ref)
			}
			path = filepath.Join(repo, "tools", "product-journey", collection+".json")
		} else {
			if filepath.IsAbs(ref) {
				return nil, fmt.Errorf("catalog reference must be relative: %q", ref)
			}
			path = filepath.Join(manifestDir, ref)
		}
		found, err := catalogIDs(path, collection)
		if err != nil {
			return nil, err
		}
		for _, id := range found {
			if ids[id] {
				return nil, fmt.Errorf("duplicate %s catalog id %q", collection, id)
			}
			ids[id] = true
		}
	}
	return ids, nil
}

func catalogIDs(path, collection string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err == nil {
		var ids []string
		for _, entry := range entries {
			if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yaml" && filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".json") {
				continue
			}
			child, err := catalogIDs(filepath.Join(path, entry.Name()), collection)
			if err != nil {
				return nil, err
			}
			ids = append(ids, child...)
		}
		return ids, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s catalog %q: %w", collection, path, err)
	}
	var data any
	if filepath.Ext(path) == ".json" {
		err = json.Unmarshal(raw, &data)
	} else {
		err = yaml.Unmarshal(raw, &data)
	}
	if err != nil {
		return nil, fmt.Errorf("parse %s catalog %q: %w", collection, path, err)
	}
	object, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s catalog %q must be an object", collection, path)
	}
	items, ok := object[collection].([]any)
	if !ok {
		items = []any{object}
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s catalog %q contains a non-object", collection, path)
		}
		id, _ := record["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("%s catalog %q entry needs id", collection, path)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func findJourneyRepoRoot(manifestDir, app string) (string, error) {
	if filepath.IsAbs(app) || strings.HasPrefix(filepath.Clean(app), ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("story.app must be repo-relative: %q", app)
	}
	for dir := manifestDir; ; dir = filepath.Dir(dir) {
		if fileExists(filepath.Join(dir, app)) {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return "", fmt.Errorf("story.app not found: %s", app)
}

func fileExists(path string) bool { info, err := os.Stat(path); return err == nil && !info.IsDir() }

// journeyReceipt is intentionally public-shaped so lifecycle adapters can write
// it without importing CLI code. It pins the manifest digest and never claims a
// successful publish without an explicit ready verdict.
type journeyReceipt struct {
	Schema        string            `json:"schema"`
	Journey       string            `json:"journey"`
	Result        string            `json:"result"`
	GeneratedAt   time.Time         `json:"generated_at"`
	SourceDigests map[string]string `json:"source_digests"`
}

func resolveJourneyPath(root, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("journey path must be repo-relative: %q", path)
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("journey path escapes repository: %q", path)
	}
	return filepath.Join(root, clean), nil
}

func defaultJourneyArtifacts(root, id, digest string) string {
	return filepath.Join(root, ".artifacts", "kitsoki-qa", strings.ReplaceAll(id, "/", "-"), digest[:12])
}

func digestBytes(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func digestFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return digestBytes(b)
}

func journeyDigests(root string, p *journeyPack, journey string) map[string]string {
	digests := map[string]string{"journey": journey, "story": digestFile(filepath.Join(root, p.Story.App))}
	if fileExists(filepath.Join(root, ".kitsoki", "project-profile.yaml")) {
		digests["profile"] = digestFile(filepath.Join(root, ".kitsoki", "project-profile.yaml"))
	}
	if p.Outputs.Storyboard != "" {
		digests["storyboard"] = digestFile(filepath.Join(root, p.Outputs.Storyboard))
	}
	if p.Outputs.Tutorial.Template != "" {
		digests["tutorial_template"] = digestFile(filepath.Join(root, p.Outputs.Tutorial.Template))
	}
	return digests
}

func sameDigests(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

func repoRelative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func runJourneyKitsoki(cmd *cobra.Command, root string, args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	run := exec.CommandContext(cmd.Context(), self, args...)
	run.Dir = root
	run.Stdout = cmd.OutOrStdout()
	run.Stderr = cmd.ErrOrStderr()
	run.Stdin = cmd.InOrStdin()
	return run.Run()
}

func writeJourneyJSON(path string, value any, cmd *cobra.Command) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err = os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
	return nil
}

func readJSON(path string, value any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, value)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func replaceGeneratedRegion(text, generated string) string {
	const start, end = "<!-- kitsoki:generated:start -->", "<!-- kitsoki:generated:end -->"
	a, b := strings.Index(text, start), strings.Index(text, end)
	if a >= 0 && b >= a {
		return text[:a] + generated + text[b+len(end):]
	}
	return text + "\n\n" + generated + "\n"
}

// journeyDeck is proof-first. It never presents a demo/replay origin as an
// accepted real journey; the origin is an evidence row reviewers can inspect
// before promotion. Projects may supply concise run evidence under proof in
// their journey manifest; that evidence remains source-controlled and is
// carried into the generated, published deck.
func journeyDeck(p *journeyPack, freeze journeyFreezeReceipt, digest string) map[string]any {
	originStatus := "pending"
	if freeze.Origin.Kind == "real" {
		originStatus = "done"
	}
	originRun := p.Proof.OriginRun
	if originRun == "" {
		originRun = "Inspect the recorded origin trace for provider and timing details."
	}
	originPrompt := p.Proof.OriginPrompt
	if originPrompt == "" {
		originPrompt = "The accepted user request is preserved in the recorded origin trace."
	}
	response := proofItems(p.Proof.Response, "The captured response is retained in the origin trace and replay fixture.")
	delivered := proofItems(p.Proof.Delivered, "Journey source, replay fixture, tutorial, deck, and tour are published together.")
	boundaries := proofItems(p.Proof.Boundaries, "A real origin is required; replay and publication must use captured deterministic evidence.")
	return map[string]any{
		"meta": map[string]any{"mode": "pitch", "title": p.Title, "resolution": map[string]any{"width": 1920, "height": 1080}},
		"scenes": []any{
			map[string]any{"type": "title", "eyebrow": "Kitsoki journey release", "title": p.Title, "subtitle": "A real origin, deterministic replay, and published operator evidence", "narration": "An evidence-led release record."},
			map[string]any{"type": "narrative", "eyebrow": "What was exercised", "lede": originPrompt, "body": "The journey entered " + p.Story.Entry + " through " + p.Story.App + ". The accepted run was constrained to the published journey scope before its trace was frozen for replay."},
			map[string]any{"type": "evidence", "title": "Real-origin receipt", "caption": "The origin is the one place where provider work is permitted; publication is gated on a real trace.", "items": []any{
				map[string]any{"label": "Run receipt", "status": originStatus, "detail": originRun},
				map[string]any{"label": "Origin trace", "status": originStatus, "detail": freeze.Origin.Kind + " origin: " + freeze.Origin.Trace},
				map[string]any{"label": "Journey entry", "status": "done", "detail": p.Story.Entry + " in " + p.Story.App},
			}},
			map[string]any{"type": "objectives", "title": "Captured response evidence", "caption": "These are the concrete next actions preserved from the accepted origin response.", "items": response},
			map[string]any{"type": "evidence", "title": "Replay and release verification", "caption": "The release gate recomputes source digests before publication.", "items": []any{
				map[string]any{"label": "Origin", "status": originStatus, "detail": freeze.Origin.Kind + " origin: " + freeze.Origin.Trace},
				map[string]any{"label": "Replay", "status": "done", "detail": freeze.Replay.Status + ": " + freeze.Replay.Flow},
				map[string]any{"label": "Journey digest", "status": "done", "detail": digest},
			}},
			map[string]any{"type": "objectives", "title": "What shipped", "caption": "These outputs are generated from the journey pack and published only after the release receipt passes.", "items": delivered},
			map[string]any{"type": "objectives", "title": "Explicit boundaries", "caption": "The deck distinguishes the real origin from inexpensive deterministic replay and does not imply unperformed work.", "items": boundaries},
			map[string]any{"type": "narrative", "eyebrow": "Publication posture", "lede": "Real once, replay often", "body": "A demo origin validates artifact shape but cannot publish a successful journey. The release receipt requires a real origin, replay, tutorial, deck, and tour evidence.", "narration": "The pack keeps demo and real proof visibly distinct."},
		},
	}
}

func proofItems(values []string, fallback string) []any {
	if len(values) == 0 {
		values = []string{fallback}
	}
	items := make([]any, 0, len(values))
	for i, value := range values {
		items = append(items, map[string]any{"label": fmt.Sprintf("Evidence %d", i+1), "status": "done", "detail": value})
	}
	return items
}

func validateJourneyDeck(cmd *cobra.Command, path string) error {
	if _, err := exec.LookPath("slidey"); err != nil {
		return fmt.Errorf("Slidey is required to validate the generated deck: %w", err)
	}
	check := exec.CommandContext(cmd.Context(), "slidey", path, "--validate")
	check.Stdout, check.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := check.Run(); err != nil {
		return fmt.Errorf("validate generated Slidey deck: %w", err)
	}
	return nil
}

func validateJourneyTutorial(text string) error {
	const start, end = "<!-- kitsoki:generated:start -->", "<!-- kitsoki:generated:end -->"
	if strings.Count(text, start) != 1 || strings.Count(text, end) != 1 {
		return fmt.Errorf("must contain one protected generated region")
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "/Users/") || strings.Contains(line, "/private/") || strings.Contains(line, "~/.kitsoki/sessions/") {
			return fmt.Errorf("contains a machine-specific path")
		}
	}
	return nil
}

func verifyJourneyReplay(origin, replay string) error {
	originTurns, err := traceTargets(origin)
	if err != nil {
		return err
	}
	replayTurns, err := traceTargets(replay)
	if err != nil {
		return err
	}
	if len(originTurns) == 0 || len(originTurns) != len(replayTurns) {
		return fmt.Errorf("transition count differs: origin=%d replay=%d", len(originTurns), len(replayTurns))
	}
	for i := range originTurns {
		if originTurns[i] != replayTurns[i] {
			return fmt.Errorf("transition %d target differs: origin=%s replay=%s", i+1, originTurns[i], replayTurns[i])
		}
	}
	return nil
}

func traceTargets(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var targets []string
	scanner := bufio.NewScanner(f)
	// Session traces may embed the expanded story source in session.story. A
	// real project story can exceed the default Scanner limit (or our old 4 MiB
	// cap), so QA must read the whole valid trace before comparing replay
	// targets. Keep a bounded ceiling rather than accepting unbounded records.
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var event struct {
			Kind    string         `json:"kind"`
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("parse trace %s: %w", path, err)
		}
		if event.Kind == "harness.returned" {
			if problem, _ := event.Payload["error"].(string); problem != "" {
				return nil, fmt.Errorf("trace contains host error: %s", problem)
			}
		}
		if event.Kind == "machine.transition" {
			if target, _ := event.Payload["to"].(string); target != "" {
				targets = append(targets, target)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}
