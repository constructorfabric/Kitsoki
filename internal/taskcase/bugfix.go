package taskcase

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	goyaml "github.com/goccy/go-yaml"
)

type bugfixManifest struct {
	Kind    string `yaml:"kind"`
	Version int    `yaml:"version"`
	Project struct {
		ID        string `yaml:"id"`
		Repo      string `yaml:"repo"`
		LocalOnly bool   `yaml:"local_only"`
	} `yaml:"project"`
	Bugs []bugfixBug `yaml:"bugs"`
	Run  struct {
		Story string `yaml:"story"`
	} `yaml:"run"`
}

type bugfixBug struct {
	ID            string `yaml:"id"`
	Title         string `yaml:"title"`
	BaselineSHA   string `yaml:"baseline_sha"`
	FixSHA        string `yaml:"fix_sha"`
	OracleTest    string `yaml:"oracle_test"`
	OracleMatch   string `yaml:"oracle_match"`
	Ticket        string `yaml:"ticket"`
	ReferenceOnly bool   `yaml:"reference_only"`
}

func LoadBugfixManifest(path string, onlyIDs []string) ([]Case, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m bugfixManifest
	if err := goyaml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse bugfix manifest %s: %w", path, err)
	}
	return adaptBugfixManifest(m, onlyIDs)
}

func adaptBugfixManifest(m bugfixManifest, onlyIDs []string) ([]Case, error) {
	if m.Project.ID == "" {
		return nil, fmt.Errorf("bugfix manifest project.id is required")
	}
	wanted := stringSet(onlyIDs)
	var out []Case
	seen := map[string]bool{}
	for _, bug := range m.Bugs {
		if bug.ID == "" {
			return nil, fmt.Errorf("%s bug row has empty id", m.Project.ID)
		}
		if len(wanted) > 0 && !wanted[bug.ID] {
			continue
		}
		seen[bug.ID] = true
		if bug.ReferenceOnly {
			if len(wanted) > 0 {
				return nil, fmt.Errorf("bug %s is reference-only and is not armable", bug.ID)
			}
			continue
		}
		story := m.Run.Story
		if story == "" {
			story = "stories/bugfix/app.yaml"
		}
		prompt := strings.TrimSpace(bug.Ticket)
		if prompt == "" {
			prompt = strings.TrimSpace(bug.Title)
		}
		id := m.Project.ID + "-" + bug.ID
		command := fmt.Sprintf("python3 tools/bugfix-bakeoff/external/bench.py verify --project %s --bug %s", m.Project.ID, bug.ID)
		if m.Project.LocalOnly {
			command += " --repo-dir <local-checkout>"
		}
		c := Case{
			Kind: Kind,
			ID:   id,
			Lane: LaneBugfix,
			Source: Source{
				CorpusRef:   "bugfix-bakeoff:" + m.Project.ID + "/" + bug.ID,
				Repo:        m.Project.Repo,
				BaselineRef: bug.BaselineSHA,
				SuccessRef:  bug.FixSHA,
			},
			Story: Story{
				App:        filepath.ToSlash(story),
				Entrypoint: "ticket",
			},
			TrainableSurface: TrainableSurface{
				WeightKind: WeightGraph,
				TargetRefs: []string{filepath.ToSlash(story)},
			},
			Input: Input{PromptOrTicket: prompt},
			Oracle: Oracle{
				Kind:       OracleRedGreen,
				Command:    command,
				Comparator: "hidden_oracle",
				AcceptanceBar: AcceptanceBar{
					MinPassRate: 1,
				},
			},
			CostPolicy: CostPolicy{
				LivePolicy: LiveOperatorApproved,
				Ladder:     "external-bakeoff",
			},
			Artifacts: Artifacts{
				Root: ".artifacts/history-training/" + id + "/",
			},
		}
		out = append(out, c)
	}
	var missing []string
	for id := range wanted {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return nil, fmt.Errorf("unknown bug id(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func stringSet(ids []string) map[string]bool {
	out := map[string]bool{}
	for _, raw := range ids {
		for _, part := range strings.Split(raw, ",") {
			id := strings.TrimSpace(part)
			if id != "" {
				out[id] = true
			}
		}
	}
	return out
}
