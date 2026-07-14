package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/orchestrator"
)

type harnessBuilder func(harnessType, claudeModel, agentBackend, recordingPath, recordPath string, def *app.AppDef, activeProfile host.ActiveProfile) (harness.Harness, error)

type selectableRoutingHarness struct {
	mu sync.Mutex

	harnessType    string
	claudeModel    string
	fallbackAgent  string
	recordingPath  string
	recordPath     string
	def            *app.AppDef
	profiles       map[string]orchestrator.HarnessProfile
	defaultProfile string
	build          harnessBuilder
	logger         *slog.Logger

	selection func() orchestrator.ProfileSelection
	cache     map[string]harness.Harness
}

type selectableRoutingKey struct {
	harnessType string
	claudeModel string
	backend     string
	profileName string
	model       string
	effort      string
	env         string
	recording   string
	record      string
}

func newSelectableRoutingHarness(harnessType, claudeModel, fallbackAgent, recordingPath, recordPath string, def *app.AppDef, profiles map[string]orchestrator.HarnessProfile, defaultProfile string, build harnessBuilder) *selectableRoutingHarness {
	if build == nil {
		build = buildHarnessWithActiveProfile
	}
	return &selectableRoutingHarness{
		harnessType:    harnessType,
		claudeModel:    claudeModel,
		fallbackAgent:  fallbackAgent,
		recordingPath:  recordingPath,
		recordPath:     recordPath,
		def:            def,
		profiles:       profiles,
		defaultProfile: defaultProfile,
		build:          build,
		cache:          map[string]harness.Harness{},
	}
}

func (h *selectableRoutingHarness) SetSelectionResolver(resolve func() orchestrator.ProfileSelection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.selection = resolve
}

func (h *selectableRoutingHarness) WithLogger(logger *slog.Logger) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logger = logger
	for _, selected := range h.cache {
		setHarnessLogger(selected, logger)
	}
}

func (h *selectableRoutingHarness) RunTurn(ctx context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	selected, err := h.selectedHarness()
	if err != nil {
		return mcp.CallToolParams{}, err
	}
	return selected.RunTurn(ctx, in)
}

func (h *selectableRoutingHarness) RunStructured(ctx context.Context, in harness.StructuredInput) (json.RawMessage, error) {
	selected, err := h.selectedHarness()
	if err != nil {
		return nil, err
	}
	structured, ok := selected.(harness.StructuredHarness)
	if !ok {
		return nil, fmt.Errorf("selected routing harness %T does not support arbitrary structured output", selected)
	}
	return structured.RunStructured(ctx, in)
}

func (h *selectableRoutingHarness) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var first error
	for _, selected := range h.cache {
		if err := selected.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (h *selectableRoutingHarness) SetAppDef(def *app.AppDef) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.def = def
	for _, selected := range h.cache {
		if ds, ok := selected.(interface{ SetAppDef(*app.AppDef) }); ok {
			ds.SetAppDef(def)
		}
	}
}

func (h *selectableRoutingHarness) selectedHarness() (harness.Harness, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	backend, active := h.resolveLocked()
	key := selectableRoutingKey{
		harnessType: h.harnessType,
		claudeModel: h.claudeModel,
		backend:     backend,
		profileName: active.Name,
		model:       active.Provider.Model,
		effort:      active.Provider.Effort,
		env:         stableEnvKey(active.Provider.Env),
		recording:   h.recordingPath,
		record:      h.recordPath,
	}
	keyString := key.String()
	if selected, ok := h.cache[keyString]; ok {
		return selected, nil
	}
	selected, err := h.build(h.harnessType, h.claudeModel, backend, h.recordingPath, h.recordPath, h.def, active)
	if err != nil {
		return nil, err
	}
	setHarnessLogger(selected, h.logger)
	h.cache[keyString] = selected
	return selected, nil
}

func (h *selectableRoutingHarness) resolveLocked() (string, host.ActiveProfile) {
	sel := orchestrator.ProfileSelection{Profile: h.defaultProfile}
	if h.selection != nil {
		if current := h.selection(); current.Profile != "" {
			sel = current
		}
	}
	if sel.Profile == "" {
		return h.fallbackAgent, host.ActiveProfile{}
	}
	p, ok := h.profiles[sel.Profile]
	if !ok {
		return h.fallbackAgent, host.ActiveProfile{}
	}
	model := p.Model
	if sel.Model != "" {
		model = sel.Model
	}
	effort := p.Effort
	if sel.Effort != "" {
		effort = sel.Effort
	}
	name := p.Name
	if name == "" {
		name = sel.Profile
	}
	active := host.ActiveProfile{
		Name: name,
		Provider: host.Provider{
			Model:  model,
			Effort: effort,
			Env:    p.Env,
		},
		Quota: p.Quota,
	}
	backend := h.fallbackAgent
	if p.Plugin == "" {
		backend = p.Backend
	}
	return backend, active
}

func (k selectableRoutingKey) String() string {
	return strings.Join([]string{k.harnessType, k.claudeModel, k.backend, k.profileName, k.model, k.effort, k.env, k.recording, k.record}, "\x00")
}

func stableEnvKey(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+env[k])
	}
	return strings.Join(parts, "\x00")
}
