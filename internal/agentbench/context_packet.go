package agentbench

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// ContextPacketV1 names the stable wire format for compiled task context.
const ContextPacketV1 = "context-packet/v1"

// ContextRequirement is one cited contract or task fact included in a packet.
type ContextRequirement struct {
	ID, Source, Content string
	Stable              bool
}

// ContextExemplar is a deterministic example appended to the task section.
type ContextExemplar struct{ ID, Content string }

// ContextPacket is the hashed, provider-independent output of packet compilation.
type ContextPacket struct {
	Schema     string `json:"schema"`
	StableHash string `json:"stable_hash"`
	TaskHash   string `json:"task_hash"`
	Content    string `json:"content"`
}

// CompileContextPacket builds a byte-stable packet. Stable requirements are
// sorted into the cacheable prefix; task requirements and exemplars follow.
// It returns an error for missing requirement or exemplar identifiers.
func CompileContextPacket(requirements []ContextRequirement, exemplars []ContextExemplar) (ContextPacket, error) {
	if len(requirements) == 0 {
		return ContextPacket{}, fmt.Errorf("context packet requires at least one requirement")
	}
	r := append([]ContextRequirement(nil), requirements...)
	for _, q := range r {
		if q.ID == "" || q.Source == "" {
			return ContextPacket{}, fmt.Errorf("context requirement needs id and source")
		}
	}
	sort.Slice(r, func(i, j int) bool {
		if r[i].Stable != r[j].Stable {
			return r[i].Stable
		}
		if r[i].Source != r[j].Source {
			return r[i].Source < r[j].Source
		}
		return r[i].ID < r[j].ID
	})
	e := append([]ContextExemplar(nil), exemplars...)
	sort.Slice(e, func(i, j int) bool { return e[i].ID < e[j].ID })
	var stable, task strings.Builder
	for _, q := range r {
		line := fmt.Sprintf("[%s] %s\n%s\n", q.ID, q.Source, q.Content)
		if q.Stable {
			stable.WriteString(line)
		} else {
			task.WriteString(line)
		}
	}
	for _, x := range e {
		if x.ID == "" {
			return ContextPacket{}, fmt.Errorf("context exemplar needs id")
		}
		task.WriteString(fmt.Sprintf("[exemplar:%s]\n%s\n", x.ID, x.Content))
	}
	stableText, taskText := stable.String(), task.String()
	content := "# Stable contract\n" + stableText + "\n# Task context\n" + taskText
	h := func(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
	return ContextPacket{Schema: ContextPacketV1, StableHash: h(stableText), TaskHash: h(taskText), Content: content}, nil
}
