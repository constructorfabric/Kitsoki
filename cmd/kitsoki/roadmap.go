package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const roadmapLedgerSchema = "kitsoki-roadmap-ledger/v1"

var roadmapKinds = setOf("goal", "initiative", "epic", "proposal", "feature", "docs", "demo", "product_site", "gate", "task", "issue", "todo", "context")
var roadmapStatuses = setOf("proposed", "in_progress", "partial", "done", "blocked", "superseded", "retired", "pending")
var roadmapHorizons = setOf("", "now", "next", "later", "done", "blocked", "unknown")
var roadmapCheckNames = setOf("proposal", "docs", "feature_yaml", "product_site", "rrweb_demo", "roadmap_deck", "gate", "tests")
var roadmapCheckStatuses = setOf("pending", "done", "blocked", "not_applicable", "missing", "stale")

type roadmapLedger struct {
	Schema string               `yaml:"schema"`
	Items  []roadmapLedgerItem  `yaml:"items"`
	Events []roadmapLedgerEvent `yaml:"events"`
}

type roadmapLedgerItem struct {
	ID          string                 `yaml:"id"`
	Kind        string                 `yaml:"kind"`
	Title       string                 `yaml:"title"`
	Status      string                 `yaml:"status"`
	Horizon     string                 `yaml:"horizon,omitempty"`
	Proposal    string                 `yaml:"proposal,omitempty"`
	FeatureYAML string                 `yaml:"feature_yaml,omitempty"`
	ProductSite string                 `yaml:"product_site,omitempty"`
	RRWebDemo   string                 `yaml:"rrweb_demo,omitempty"`
	Docs        []string               `yaml:"docs,omitempty"`
	Refs        []string               `yaml:"refs,omitempty"`
	Checks      []roadmapLedgerCheck   `yaml:"checks,omitempty"`
	Meta        map[string]interface{} `yaml:"meta,omitempty"`
}

type roadmapLedgerCheck struct {
	Name   string `yaml:"name"`
	Status string `yaml:"status"`
	Ref    string `yaml:"ref,omitempty"`
}

type roadmapLedgerEvent struct {
	ID      string               `yaml:"id"`
	Event   string               `yaml:"event"`
	ItemID  string               `yaml:"item_id"`
	Source  string               `yaml:"source"`
	Actor   string               `yaml:"actor,omitempty"`
	Status  string               `yaml:"status,omitempty"`
	Horizon string               `yaml:"horizon,omitempty"`
	Refs    []string             `yaml:"refs,omitempty"`
	Checks  []roadmapLedgerCheck `yaml:"checks,omitempty"`
	Note    string               `yaml:"note,omitempty"`
}

func roadmapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "roadmap",
		Short: "Roadmap planning and progress artifact tools",
	}
	cmd.AddCommand(roadmapLedgerCmd())
	return cmd
}

func roadmapLedgerCmd() *cobra.Command {
	var ledgerPath string
	cmd := &cobra.Command{
		Use:   "ledger",
		Short: "Maintain a YAML roadmap progress ledger under .artifacts",
		Long: `Maintain a deterministic YAML progress ledger, normally at
.artifacts/roadmap/progress.yaml. The ledger is local/generated state: stories
and ad-hoc agents append idempotent events to it while proposals, feature YAML,
product-site pages, rrweb demos, docs, and gates catch up.`,
	}
	cmd.PersistentFlags().StringVar(&ledgerPath, "ledger", ".artifacts/roadmap/progress.yaml", "roadmap ledger path")
	cmd.AddCommand(roadmapLedgerEventCmd(&ledgerPath))
	cmd.AddCommand(roadmapLedgerCheckCmd(&ledgerPath))
	return cmd
}

func roadmapLedgerEventCmd(ledgerPath *string) *cobra.Command {
	var ev roadmapLedgerEvent
	var item roadmapLedgerItem
	var refs []string
	var docs []string
	var checks []string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "event",
		Short: "Upsert a roadmap item and append an idempotent event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ev.Event = strings.TrimSpace(ev.Event)
			item.ID = strings.TrimSpace(item.ID)
			item.Kind = strings.TrimSpace(item.Kind)
			item.Title = strings.TrimSpace(item.Title)
			item.Status = strings.TrimSpace(item.Status)
			item.Horizon = strings.TrimSpace(item.Horizon)
			ev.Source = strings.TrimSpace(ev.Source)
			ev.Actor = strings.TrimSpace(ev.Actor)
			ev.Note = strings.TrimSpace(ev.Note)
			if ev.Event == "" {
				return errors.New("--event is required")
			}
			if item.ID == "" {
				if item.Proposal != "" {
					item.ID = "proposal/" + strings.TrimSuffix(filepath.Base(item.Proposal), filepath.Ext(item.Proposal))
				} else if item.Title != "" {
					item.ID = "item/" + slugish(item.Title)
				} else {
					return errors.New("--item-id or --title is required")
				}
			}
			if item.Kind == "" {
				item.Kind = "task"
			}
			if item.Status == "" {
				item.Status = "pending"
			}
			if item.Title == "" {
				item.Title = item.ID
			}
			if ev.Source == "" {
				ev.Source = "ad-hoc"
			}
			if ev.Actor == "" {
				ev.Actor = "agent"
			}
			parsedChecks, err := parseRoadmapChecks(checks)
			if err != nil {
				return err
			}
			item.Docs = cleanStrings(docs)
			item.Refs = cleanStrings(append(refs, item.Proposal, item.FeatureYAML, item.ProductSite, item.RRWebDemo))
			item.Checks = parsedChecks
			ev.ItemID = item.ID
			ev.Status = item.Status
			ev.Horizon = item.Horizon
			ev.Refs = item.Refs
			ev.Checks = parsedChecks
			ev.ID = roadmapEventID(ev)

			ledger, err := readRoadmapLedger(*ledgerPath)
			if err != nil {
				return err
			}
			changed := upsertRoadmapItem(&ledger, item)
			if appendRoadmapEvent(&ledger, ev) {
				changed = true
			}
			sortRoadmapLedger(&ledger)
			if errs := validateRoadmapLedger(ledger, ""); len(errs) > 0 {
				return fmt.Errorf("roadmap ledger event would make ledger invalid: %s", strings.Join(errs, "; "))
			}
			if changed {
				if err := writeRoadmapLedger(*ledgerPath, ledger); err != nil {
					return err
				}
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]interface{}{
					"ok":       true,
					"ledger":   *ledgerPath,
					"event_id": ev.ID,
					"item_id":  item.ID,
					"changed":  changed,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "roadmap ledger: %s %s (%s)\n", ev.Event, item.ID, *ledgerPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&ev.Event, "event", "", "event name, e.g. proposal_published, shipped, docs_updated")
	cmd.Flags().StringVar(&item.ID, "item-id", "", "stable item id (default derived from --proposal or --title)")
	cmd.Flags().StringVar(&item.Kind, "kind", "task", "item kind")
	cmd.Flags().StringVar(&item.Title, "title", "", "item title")
	cmd.Flags().StringVar(&item.Status, "status", "pending", "item status")
	cmd.Flags().StringVar(&item.Horizon, "horizon", "", "roadmap horizon: now, next, later, done, blocked, unknown")
	cmd.Flags().StringVar(&item.Proposal, "proposal", "", "proposal markdown path")
	cmd.Flags().StringVar(&item.FeatureYAML, "feature-yaml", "", "feature catalog YAML path")
	cmd.Flags().StringVar(&item.ProductSite, "product-site", "", "product-site/docs path")
	cmd.Flags().StringVar(&item.RRWebDemo, "rrweb-demo", "", "rrweb demo artifact or source path")
	cmd.Flags().StringArrayVar(&docs, "doc", nil, "narrative docs path (repeatable)")
	cmd.Flags().StringArrayVar(&refs, "ref", nil, "supporting reference path or URL (repeatable)")
	cmd.Flags().StringArrayVar(&checks, "check", nil, "coverage check name=status, e.g. docs=pending (repeatable)")
	cmd.Flags().StringVar(&ev.Source, "source", "ad-hoc", "producer, e.g. story:dev-story/design_draft")
	cmd.Flags().StringVar(&ev.Actor, "actor", "agent", "actor label")
	cmd.Flags().StringVar(&ev.Note, "note", "", "short event note")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON result")
	return cmd
}

func roadmapLedgerCheckCmd(ledgerPath *string) *cobra.Command {
	var repoRoot string
	var jsonOut bool
	var strict bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate the roadmap ledger shape and completion coverage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ledger, err := readRoadmapLedger(*ledgerPath)
			if err != nil {
				return err
			}
			errs := validateRoadmapLedger(ledger, repoRoot)
			if strict {
				errs = append(errs, validateStrictRoadmapLedger(ledger)...)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]interface{}{
					"ok":       len(errs) == 0,
					"ledger":   *ledgerPath,
					"problems": errs,
					"items":    len(ledger.Items),
					"events":   len(ledger.Events),
				})
			}
			if len(errs) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "roadmap ledger: OK - %d item(s), %d event(s)\n", len(ledger.Items), len(ledger.Events))
				return nil
			}
			for _, e := range errs {
				fmt.Fprintf(cmd.OutOrStdout(), "roadmap ledger: %s\n", e)
			}
			return fmt.Errorf("roadmap ledger: %d problem(s)", len(errs))
		},
	}
	cmd.Flags().StringVar(&repoRoot, "repo-root", ".", "repo root for path existence checks")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON result")
	cmd.Flags().BoolVar(&strict, "strict", false, "require known work items to declare coverage checks and event history")
	return cmd
}

func readRoadmapLedger(path string) (roadmapLedger, error) {
	var ledger roadmapLedger
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return roadmapLedger{Schema: roadmapLedgerSchema, Items: []roadmapLedgerItem{}, Events: []roadmapLedgerEvent{}}, nil
		}
		return ledger, fmt.Errorf("read roadmap ledger: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return roadmapLedger{Schema: roadmapLedgerSchema, Items: []roadmapLedgerItem{}, Events: []roadmapLedgerEvent{}}, nil
	}
	if err := yaml.Unmarshal(raw, &ledger); err != nil {
		return ledger, fmt.Errorf("parse roadmap ledger: %w", err)
	}
	if ledger.Schema == "" {
		ledger.Schema = roadmapLedgerSchema
	}
	if ledger.Items == nil {
		ledger.Items = []roadmapLedgerItem{}
	}
	if ledger.Events == nil {
		ledger.Events = []roadmapLedgerEvent{}
	}
	return ledger, nil
}

func writeRoadmapLedger(path string, ledger roadmapLedger) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create roadmap ledger dir: %w", err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(ledger); err != nil {
		return fmt.Errorf("marshal roadmap ledger: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("marshal roadmap ledger: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func upsertRoadmapItem(ledger *roadmapLedger, item roadmapLedgerItem) bool {
	for i := range ledger.Items {
		if ledger.Items[i].ID != item.ID {
			continue
		}
		before := roadmapComparableItem(ledger.Items[i])
		mergeRoadmapItem(&ledger.Items[i], item)
		return before != roadmapComparableItem(ledger.Items[i])
	}
	ledger.Items = append(ledger.Items, item)
	return true
}

func mergeRoadmapItem(dst *roadmapLedgerItem, src roadmapLedgerItem) {
	if src.Kind != "" {
		dst.Kind = src.Kind
	}
	if src.Title != "" {
		dst.Title = src.Title
	}
	if src.Status != "" {
		dst.Status = src.Status
	}
	if src.Horizon != "" {
		dst.Horizon = src.Horizon
	}
	if src.Proposal != "" {
		dst.Proposal = src.Proposal
	}
	if src.FeatureYAML != "" {
		dst.FeatureYAML = src.FeatureYAML
	}
	if src.ProductSite != "" {
		dst.ProductSite = src.ProductSite
	}
	if src.RRWebDemo != "" {
		dst.RRWebDemo = src.RRWebDemo
	}
	dst.Docs = mergeSortedStrings(dst.Docs, src.Docs)
	dst.Refs = mergeSortedStrings(dst.Refs, src.Refs)
	dst.Checks = mergeRoadmapChecks(dst.Checks, src.Checks)
}

func appendRoadmapEvent(ledger *roadmapLedger, ev roadmapLedgerEvent) bool {
	for _, existing := range ledger.Events {
		if existing.ID == ev.ID {
			return false
		}
	}
	ledger.Events = append(ledger.Events, ev)
	return true
}

func sortRoadmapLedger(ledger *roadmapLedger) {
	sort.Slice(ledger.Items, func(i, j int) bool { return ledger.Items[i].ID < ledger.Items[j].ID })
	for i := range ledger.Items {
		ledger.Items[i].Refs = cleanStrings(ledger.Items[i].Refs)
		ledger.Items[i].Docs = cleanStrings(ledger.Items[i].Docs)
		ledger.Items[i].Checks = sortChecks(ledger.Items[i].Checks)
	}
	for i := range ledger.Events {
		ledger.Events[i].Refs = cleanStrings(ledger.Events[i].Refs)
		ledger.Events[i].Checks = sortChecks(ledger.Events[i].Checks)
	}
}

func validateRoadmapLedger(ledger roadmapLedger, repoRoot string) []string {
	var errs []string
	if ledger.Schema != roadmapLedgerSchema {
		errs = append(errs, fmt.Sprintf("schema must be %q", roadmapLedgerSchema))
	}
	itemIDs := map[string]bool{}
	for _, item := range ledger.Items {
		if item.ID == "" {
			errs = append(errs, "item id is required")
		}
		if itemIDs[item.ID] {
			errs = append(errs, fmt.Sprintf("%s: duplicate item id", item.ID))
		}
		itemIDs[item.ID] = true
		if item.Title == "" {
			errs = append(errs, fmt.Sprintf("%s: title is required", item.ID))
		}
		if !roadmapKinds[item.Kind] {
			errs = append(errs, fmt.Sprintf("%s: invalid kind %q", item.ID, item.Kind))
		}
		if !roadmapStatuses[item.Status] {
			errs = append(errs, fmt.Sprintf("%s: invalid status %q", item.ID, item.Status))
		}
		if !roadmapHorizons[item.Horizon] {
			errs = append(errs, fmt.Sprintf("%s: invalid horizon %q", item.ID, item.Horizon))
		}
		errs = append(errs, validateRoadmapChecks(item.ID, item.Checks)...)
		if item.Kind == "proposal" && item.Proposal == "" {
			errs = append(errs, fmt.Sprintf("%s: proposal item needs proposal path", item.ID))
		}
		if item.Status == "done" {
			errs = append(errs, validateDoneRoadmapItem(item, repoRoot)...)
		}
	}
	eventIDs := map[string]bool{}
	for _, ev := range ledger.Events {
		if ev.ID == "" {
			errs = append(errs, "event id is required")
		}
		if eventIDs[ev.ID] {
			errs = append(errs, fmt.Sprintf("%s: duplicate event id", ev.ID))
		}
		eventIDs[ev.ID] = true
		if ev.Event == "" {
			errs = append(errs, fmt.Sprintf("%s: event name is required", ev.ID))
		}
		if ev.ItemID == "" || !itemIDs[ev.ItemID] {
			errs = append(errs, fmt.Sprintf("%s: event item_id %q does not resolve", ev.ID, ev.ItemID))
		}
		if ev.Source == "" {
			errs = append(errs, fmt.Sprintf("%s: source is required", ev.ID))
		}
		if ev.Status != "" && !roadmapStatuses[ev.Status] {
			errs = append(errs, fmt.Sprintf("%s: invalid event status %q", ev.ID, ev.Status))
		}
		if !roadmapHorizons[ev.Horizon] {
			errs = append(errs, fmt.Sprintf("%s: invalid event horizon %q", ev.ID, ev.Horizon))
		}
		errs = append(errs, validateRoadmapChecks(ev.ID, ev.Checks)...)
	}
	return errs
}

func validateStrictRoadmapLedger(ledger roadmapLedger) []string {
	if len(ledger.Items) == 0 && len(ledger.Events) == 0 {
		return nil
	}
	var errs []string
	eventsByItem := map[string]int{}
	for _, ev := range ledger.Events {
		eventsByItem[ev.ItemID]++
	}
	requiredChecks := []string{"proposal", "docs", "feature_yaml", "product_site", "rrweb_demo"}
	for _, item := range ledger.Items {
		if item.ID == "" {
			continue
		}
		if eventsByItem[item.ID] == 0 {
			errs = append(errs, fmt.Sprintf("%s: strict mode requires at least one event", item.ID))
		}
		if item.Status != "in_progress" && item.Status != "partial" && item.Status != "done" {
			continue
		}
		checks := map[string]roadmapLedgerCheck{}
		for _, check := range item.Checks {
			checks[check.Name] = check
		}
		for _, name := range requiredChecks {
			if checks[name].Status == "" {
				errs = append(errs, fmt.Sprintf("%s: strict mode requires %s check", item.ID, name))
			}
		}
		if len(item.Refs) == 0 && item.Proposal == "" && len(item.Docs) == 0 && item.FeatureYAML == "" && item.ProductSite == "" && item.RRWebDemo == "" {
			errs = append(errs, fmt.Sprintf("%s: strict mode requires at least one proposal, doc, surface, demo, or ref path", item.ID))
		}
	}
	return errs
}

func validateRoadmapChecks(owner string, checks []roadmapLedgerCheck) []string {
	var errs []string
	seen := map[string]bool{}
	for _, check := range checks {
		if !roadmapCheckNames[check.Name] {
			errs = append(errs, fmt.Sprintf("%s: invalid check name %q", owner, check.Name))
		}
		if seen[check.Name] {
			errs = append(errs, fmt.Sprintf("%s: duplicate check %q", owner, check.Name))
		}
		seen[check.Name] = true
		if !roadmapCheckStatuses[check.Status] {
			errs = append(errs, fmt.Sprintf("%s: invalid check status %q for %s", owner, check.Status, check.Name))
		}
	}
	return errs
}

func validateDoneRoadmapItem(item roadmapLedgerItem, repoRoot string) []string {
	var errs []string
	checks := map[string]roadmapLedgerCheck{}
	for _, check := range item.Checks {
		checks[check.Name] = check
		if check.Status == "pending" || check.Status == "missing" || check.Status == "stale" {
			errs = append(errs, fmt.Sprintf("%s: done item has unresolved %s check (%s)", item.ID, check.Name, check.Status))
		}
	}
	for _, name := range []string{"proposal", "docs", "feature_yaml", "product_site", "rrweb_demo"} {
		if _, ok := checks[name]; !ok {
			errs = append(errs, fmt.Sprintf("%s: done item must declare %s check as done, blocked, or not_applicable", item.ID, name))
		}
	}
	if checkIsDone(checks["proposal"]) && item.Proposal != "" && !repoPathExists(repoRoot, item.Proposal) {
		errs = append(errs, fmt.Sprintf("%s: proposal check is done but %s is missing", item.ID, item.Proposal))
	}
	if checkIsDone(checks["docs"]) {
		for _, p := range item.Docs {
			if !repoPathExists(repoRoot, p) {
				errs = append(errs, fmt.Sprintf("%s: docs check is done but %s is missing", item.ID, p))
			}
		}
		if len(item.Docs) == 0 {
			errs = append(errs, fmt.Sprintf("%s: docs check is done but no --doc path is recorded", item.ID))
		}
	}
	if checkIsDone(checks["feature_yaml"]) && item.FeatureYAML != "" && !repoPathExists(repoRoot, item.FeatureYAML) {
		errs = append(errs, fmt.Sprintf("%s: feature_yaml check is done but %s is missing", item.ID, item.FeatureYAML))
	}
	if checkIsDone(checks["product_site"]) && item.ProductSite != "" && !repoPathExists(repoRoot, item.ProductSite) {
		errs = append(errs, fmt.Sprintf("%s: product_site check is done but %s is missing", item.ID, item.ProductSite))
	}
	if checkIsDone(checks["rrweb_demo"]) && item.RRWebDemo != "" && !repoPathExists(repoRoot, item.RRWebDemo) {
		errs = append(errs, fmt.Sprintf("%s: rrweb_demo check is done but %s is missing", item.ID, item.RRWebDemo))
	}
	return errs
}

func checkIsDone(check roadmapLedgerCheck) bool {
	return check.Status == "done"
}

func repoPathExists(repoRoot, rel string) bool {
	if repoRoot == "" {
		return true
	}
	if rel == "" || strings.Contains(rel, "://") {
		return true
	}
	if filepath.IsAbs(rel) {
		_, err := os.Stat(rel)
		return err == nil
	}
	_, err := os.Stat(filepath.Join(repoRoot, rel))
	return err == nil
}

func parseRoadmapChecks(raw []string) ([]roadmapLedgerCheck, error) {
	var checks []roadmapLedgerCheck
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		name, status, ok := strings.Cut(r, "=")
		if !ok {
			return nil, fmt.Errorf("--check %q must be name=status", r)
		}
		checks = append(checks, roadmapLedgerCheck{Name: strings.TrimSpace(name), Status: strings.TrimSpace(status)})
	}
	return sortChecks(checks), nil
}

func mergeRoadmapChecks(a, b []roadmapLedgerCheck) []roadmapLedgerCheck {
	byName := map[string]roadmapLedgerCheck{}
	for _, c := range a {
		if c.Name != "" {
			byName[c.Name] = c
		}
	}
	for _, c := range b {
		if c.Name != "" {
			byName[c.Name] = c
		}
	}
	out := make([]roadmapLedgerCheck, 0, len(byName))
	for _, c := range byName {
		out = append(out, c)
	}
	return sortChecks(out)
}

func sortChecks(in []roadmapLedgerCheck) []roadmapLedgerCheck {
	out := make([]roadmapLedgerCheck, 0, len(in))
	for _, c := range in {
		c.Name = strings.TrimSpace(c.Name)
		c.Status = strings.TrimSpace(c.Status)
		c.Ref = strings.TrimSpace(c.Ref)
		if c.Name == "" && c.Status == "" {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Status < out[j].Status
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func roadmapEventID(ev roadmapLedgerEvent) string {
	parts := []string{ev.Event, ev.ItemID, ev.Source, ev.Actor, ev.Status, ev.Horizon, ev.Note}
	parts = append(parts, ev.Refs...)
	for _, c := range ev.Checks {
		parts = append(parts, c.Name+"="+c.Status)
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])[:16]
}

func roadmapComparableItem(item roadmapLedgerItem) string {
	b, _ := json.Marshal(item)
	return string(b)
}

func mergeSortedStrings(a, b []string) []string {
	return cleanStrings(append(append([]string{}, a...), b...))
}

func cleanStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func slugish(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func setOf(items ...string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
}
