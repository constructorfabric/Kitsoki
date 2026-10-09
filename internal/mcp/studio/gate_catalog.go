package studio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// GateDefinition is a checked-in, declarative command contract. Agents select
// a gate and fill named, validated inputs; they never supply a shell command or
// an argv fragment. A gate command is therefore reviewable policy rather than
// an alternate host.run surface.
type GateDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Program     string          `json:"-"`
	Arguments   []string        `json:"-"`
	Inputs      []GateInputSpec `json:"inputs,omitempty"`
	Timeout     time.Duration   `json:"timeout_seconds"`
}

// GateInputSpec constrains one named value accepted by a gate. The small set
// of kinds deliberately avoids accepting an arbitrary command flag or path.
type GateInputSpec struct {
	Name        string `json:"name"`
	Required    bool   `json:"required,omitempty"`
	Default     string `json:"default,omitempty"`
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
}

const (
	gateInputPackageTarget = "go_package_target"
	gateInputStoryApp      = "story_app"
)

// GateCatalog is an immutable collection of known gate definitions. Its hash
// is captured alongside successful evidence so catalog revisions cannot be
// mistaken for equivalent proof.
type GateCatalog struct {
	definitions map[string]GateDefinition
	hash        string
}

// NewGateCatalog validates and freezes a supplied catalog. Tests can inject a
// narrow catalog; production uses DefaultGateCatalog.
func NewGateCatalog(definitions []GateDefinition) (GateCatalog, error) {
	if len(definitions) == 0 {
		return GateCatalog{}, errors.New("gate catalog must contain at least one definition")
	}
	byName := make(map[string]GateDefinition, len(definitions))
	for _, definition := range definitions {
		if err := validateGateDefinition(definition); err != nil {
			return GateCatalog{}, err
		}
		if _, exists := byName[definition.Name]; exists {
			return GateCatalog{}, fmt.Errorf("duplicate gate %q", definition.Name)
		}
		definition.Arguments = append([]string(nil), definition.Arguments...)
		definition.Inputs = append([]GateInputSpec(nil), definition.Inputs...)
		byName[definition.Name] = definition
	}
	return GateCatalog{definitions: byName, hash: hashGateDefinitions(byName)}, nil
}

func validateGateDefinition(definition GateDefinition) error {
	if strings.TrimSpace(definition.Name) == "" || strings.TrimSpace(definition.Program) == "" || len(definition.Arguments) == 0 {
		return errors.New("gate name, program, and arguments are required")
	}
	if strings.ContainsAny(definition.Name, " /\\") || definition.Timeout <= 0 {
		return fmt.Errorf("gate %q has an unsafe name or timeout", definition.Name)
	}
	seen := map[string]bool{}
	for _, input := range definition.Inputs {
		if input.Name == "" || seen[input.Name] || (input.Kind != gateInputPackageTarget && input.Kind != gateInputStoryApp) {
			return fmt.Errorf("gate %q has an invalid input", definition.Name)
		}
		seen[input.Name] = true
	}
	return nil
}

// DefaultGateCatalog is the initial checked-in catalog. The arguments are
// direct-exec vectors, not shell snippets. The focused studio test gate is the
// first repo-declared gate; additional repository gates belong here rather than
// in caller-provided host.run commands.
func DefaultGateCatalog() GateCatalog {
	catalog, err := NewGateCatalog([]GateDefinition{
		{
			Name: "go_test", Description: "Run Go tests for one safe package target.", Program: "go",
			Arguments: []string{"test", "{target}"}, Timeout: 10 * time.Minute,
			Inputs: []GateInputSpec{{Name: "target", Default: "./...", Kind: gateInputPackageTarget, Description: "Go package target such as ./internal/mcp/studio."}},
		},
		{
			Name: "story_validate", Description: "Replay and validate one checked-in story flow.", Program: "go",
			Arguments: []string{"run", "./cmd/kitsoki", "test", "flows", "{app}"}, Timeout: 5 * time.Minute,
			Inputs: []GateInputSpec{{Name: "app", Required: true, Kind: gateInputStoryApp, Description: "Checked-in stories/<name>/app.yaml path."}},
		},
		{
			Name: "story_test", Description: "Run the verbose deterministic flow test for one story.", Program: "go",
			Arguments: []string{"run", "./cmd/kitsoki", "test", "flows", "{app}", "--v"}, Timeout: 5 * time.Minute,
			Inputs: []GateInputSpec{{Name: "app", Required: true, Kind: gateInputStoryApp, Description: "Checked-in stories/<name>/app.yaml path."}},
		},
		{
			Name: "mcp_test", Description: "Run the no-LLM Studio MCP stdio smoke.", Program: "go",
			Arguments: []string{"run", "./cmd/kitsoki", "mcp-test", "--stories-dir", "./stories", "--timeout", "20s"}, Timeout: 2 * time.Minute,
		},
		{
			Name: "web_build", Description: "Build the checked-in web surface.", Program: "make",
			Arguments: []string{"web"}, Timeout: 10 * time.Minute,
		},
		{
			Name: "studio_unit", Description: "Run the focused Studio MCP unit suite.", Program: "go",
			Arguments: []string{"test", "./internal/mcp/studio"}, Timeout: 2 * time.Minute,
		},
	})
	if err != nil {
		panic(err)
	}
	return catalog
}

func (c GateCatalog) Hash() string { return c.hash }

func (c GateCatalog) Definition(name string) (GateDefinition, bool) {
	definition, ok := c.definitions[name]
	if !ok {
		return GateDefinition{}, false
	}
	definition.Arguments = append([]string(nil), definition.Arguments...)
	definition.Inputs = append([]GateInputSpec(nil), definition.Inputs...)
	return definition, true
}

func (c GateCatalog) Definitions() []GateDefinition {
	names := make([]string, 0, len(c.definitions))
	for name := range c.definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]GateDefinition, 0, len(names))
	for _, name := range names {
		definition, _ := c.Definition(name)
		out = append(out, definition)
	}
	return out
}

func (d GateDefinition) render(input map[string]string) ([]string, error) {
	values := make(map[string]string, len(d.Inputs))
	for _, spec := range d.Inputs {
		value := strings.TrimSpace(input[spec.Name])
		if value == "" {
			value = spec.Default
		}
		if value == "" && spec.Required {
			return nil, fmt.Errorf("gate %q requires %q", d.Name, spec.Name)
		}
		if value != "" && !validGateInput(spec.Kind, value) {
			return nil, fmt.Errorf("gate %q rejected %s input", d.Name, spec.Name)
		}
		values[spec.Name] = value
	}
	for key := range input {
		if _, known := values[key]; !known {
			return nil, fmt.Errorf("gate %q does not accept %q", d.Name, key)
		}
	}
	args := make([]string, len(d.Arguments))
	for i, argument := range d.Arguments {
		if strings.HasPrefix(argument, "{") && strings.HasSuffix(argument, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(argument, "{"), "}")
			value, known := values[name]
			if !known || value == "" {
				return nil, fmt.Errorf("gate %q has unresolved argument %q", d.Name, name)
			}
			args[i] = value
			continue
		}
		args[i] = argument
	}
	return args, nil
}

func validGateInput(kind, value string) bool {
	if strings.ContainsAny(value, "\x00\n\r\t ") || strings.HasPrefix(value, "-") || strings.Contains(value, "\\") {
		return false
	}
	switch kind {
	case gateInputPackageTarget:
		return value == "./..." || (strings.HasPrefix(value, "./") && !strings.Contains(value, ".."))
	case gateInputStoryApp:
		return strings.HasPrefix(value, "stories/") && strings.HasSuffix(value, "/app.yaml") && !strings.Contains(value, "..")
	default:
		return false
	}
}

func hashGateDefinitions(definitions map[string]GateDefinition) string {
	type stableDefinition struct {
		Name, Description, Program string
		Arguments                  []string
		Inputs                     []GateInputSpec
		TimeoutSeconds             int64
	}
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	stable := make([]stableDefinition, 0, len(names))
	for _, name := range names {
		definition := definitions[name]
		stable = append(stable, stableDefinition{Name: definition.Name, Description: definition.Description, Program: definition.Program, Arguments: definition.Arguments, Inputs: definition.Inputs, TimeoutSeconds: int64(definition.Timeout.Seconds())})
	}
	b, _ := json.Marshal(stable)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
