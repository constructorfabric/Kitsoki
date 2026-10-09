package agentbench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const MCPOSCorpusSchema = "mcp_os_corpus/v1"

// MCPOSCorpus is the replay-only corpus contract used to establish the
// current-toolbox baseline before policy-bearing MCP operating-system slices.
type MCPOSCorpus struct {
	SchemaVersion string      `json:"schema_version"`
	Label         string      `json:"label"`
	Cases         []MCPOSCase `json:"cases"`
}

type MCPOSCase struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

// LoadMCPOSCorpus validates the stable, 12-case baseline contract. It is
// intentionally data-only: no process, provider, or workspace is opened.
func LoadMCPOSCorpus(data []byte) (MCPOSCorpus, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var corpus MCPOSCorpus
	if err := dec.Decode(&corpus); err != nil {
		return MCPOSCorpus{}, err
	}
	if corpus.SchemaVersion != MCPOSCorpusSchema {
		return MCPOSCorpus{}, fmt.Errorf("unsupported MCP OS corpus schema %q", corpus.SchemaVersion)
	}
	if len(corpus.Cases) != 12 {
		return MCPOSCorpus{}, fmt.Errorf("MCP OS corpus has %d cases; want 12", len(corpus.Cases))
	}
	requiredKinds := map[string]bool{
		"story_edit":                 false,
		"runtime_fix":                false,
		"trace_diagnosis":            false,
		"documentation_change":       false,
		"managed_workspace_mutation": false,
	}
	seen := make(map[string]bool, len(corpus.Cases))
	for _, c := range corpus.Cases {
		if c.ID == "" || c.Kind == "" || c.Description == "" {
			return MCPOSCorpus{}, fmt.Errorf("MCP OS corpus case requires id, kind, and description")
		}
		if seen[c.ID] {
			return MCPOSCorpus{}, fmt.Errorf("duplicate MCP OS corpus case %q", c.ID)
		}
		seen[c.ID] = true
		if _, ok := requiredKinds[c.Kind]; ok {
			requiredKinds[c.Kind] = true
		}
	}
	for kind, present := range requiredKinds {
		if !present {
			return MCPOSCorpus{}, fmt.Errorf("MCP OS corpus missing required kind %q", kind)
		}
	}
	return corpus, nil
}

// MCPOSCorpusSHA256 returns the digest convention shared with the Python
// reporter: a compact, key-sorted JSON encoding of the source document.
func MCPOSCorpusSHA256(data []byte) (string, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
