package corpusintake

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// LoadArenaYAML imports an Arena manifest without trusting its verified_* flags.
func LoadArenaYAML(data []byte, locator string) ([]Candidate, error) {
	var manifest arenaManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode arena manifest: %w", err)
	}
	if manifest.Kind == "" {
		return nil, fmt.Errorf("arena manifest kind: %w", ErrInvalidCandidate)
	}
	return fromArena(manifest, locator)
}

// LoadExternalBakeoffYAML imports a project-scoped external bakeoff manifest.
func LoadExternalBakeoffYAML(data []byte, locator string) ([]Candidate, error) {
	var manifest externalManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode external manifest: %w", err)
	}
	if manifest.Kind != "bugfix_bakeoff_external" {
		return nil, fmt.Errorf("external manifest kind %q: %w", manifest.Kind, ErrInvalidCandidate)
	}
	return fromExternal(manifest, locator)
}

type arenaManifest struct {
	Kind  string      `yaml:"kind"`
	Tasks []arenaTask `yaml:"tasks"`
}
type arenaTask struct {
	ID           string `yaml:"id"`
	Repo         string `yaml:"repo_label"`
	RepoFallback string `yaml:"repo"`
	Archetype    string `yaml:"archetype"`
	Baseline     string `yaml:"baseline_sha"`
	Fix          string `yaml:"fix_sha"`
	Ticket       string `yaml:"ticket"`
	Oracle       any    `yaml:"oracle"`
}

func fromArena(manifest arenaManifest, locator string) ([]Candidate, error) {
	out := make([]Candidate, 0, len(manifest.Tasks))
	for _, task := range manifest.Tasks {
		repo := task.Repo
		if repo == "" {
			repo = task.RepoFallback
		}
		oracle, err := CanonicalOracle(task.Oracle)
		if err != nil {
			return nil, fmt.Errorf("arena task %q: %w", task.ID, err)
		}
		digest, err := Digest(task)
		if err != nil {
			return nil, err
		}
		candidate := Candidate{Kind: KindCorpusCaseV1, ID: task.ID, Repo: repo, Source: SourceArena, BaselineRef: task.Baseline, FixRef: task.Fix, Ticket: task.Ticket, Archetype: task.Archetype, Oracle: oracle, Provenance: Provenance{Adapter: SourceArena, Locator: locator + "#" + task.ID, SourceDigest: digest, Revision: manifest.Kind}}
		if err := candidate.Validate(); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, nil
}

type externalManifest struct {
	Kind    string          `yaml:"kind"`
	Project externalProject `yaml:"project"`
	Bugs    []externalBug   `yaml:"bugs"`
}
type externalProject struct {
	ID     string `yaml:"id"`
	Repo   string `yaml:"repo"`
	Oracle any    `yaml:"oracle"`
}
type externalBug struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Baseline    string `yaml:"baseline_sha"`
	Fix         string `yaml:"fix_sha"`
	Ticket      string `yaml:"ticket"`
	Oracle      any    `yaml:"oracle"`
	OracleTest  string `yaml:"oracle_test"`
	OracleMatch string `yaml:"oracle_match"`
}

func fromExternal(manifest externalManifest, locator string) ([]Candidate, error) {
	out := make([]Candidate, 0, len(manifest.Bugs))
	for _, bug := range manifest.Bugs {
		oracle := bug.Oracle
		if oracle == nil {
			oracle = manifest.Project.Oracle
		}
		oracleWithFixture := map[string]any{"definition": oracle, "oracle_test": bug.OracleTest, "oracle_match": bug.OracleMatch}
		canonical, err := CanonicalOracle(oracleWithFixture)
		if err != nil {
			return nil, fmt.Errorf("external bug %q: %w", bug.ID, err)
		}
		digest, err := Digest(bug)
		if err != nil {
			return nil, err
		}
		ticket := bug.Ticket
		if ticket == "" {
			ticket = bug.Title
		}
		candidate := Candidate{Kind: KindCorpusCaseV1, ID: manifest.Project.ID + "-" + bug.ID, Repo: manifest.Project.Repo, Source: SourceExternalBakeoff, BaselineRef: bug.Baseline, FixRef: bug.Fix, Ticket: ticket, Archetype: "bugfix", Oracle: canonical, Provenance: Provenance{Adapter: SourceExternalBakeoff, Locator: locator + "#" + bug.ID, SourceDigest: digest, Revision: manifest.Kind}}
		if err := candidate.Validate(); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, nil
}
