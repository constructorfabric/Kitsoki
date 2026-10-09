package studio

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/capsuletest"
)

type gateFakeCommandRunner struct {
	calls  [][]string
	result GateCommandResult
	err    error
}

func (r *gateFakeCommandRunner) Run(_ context.Context, dir, program string, args ...string) (GateCommandResult, error) {
	r.calls = append(r.calls, append([]string{dir, program}, args...))
	return r.result, r.err
}

type gateFakeProbe struct {
	heads []string
	err   error
}

func (p *gateFakeProbe) Head(_ context.Context, _ ManagedWorkspace) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	if len(p.heads) == 0 {
		return "head", nil
	}
	head := p.heads[0]
	if len(p.heads) > 1 {
		p.heads = p.heads[1:]
	}
	return head, nil
}

type gateFixture struct {
	objectiveID string
	workspaceID string
	service     *ManagedWorkspaceService
	runner      *gateFakeCommandRunner
	probe       *gateFakeProbe
	gates       *GateRunner
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	repo := capsuletest.Open(t, "clean-repo")
	objectiveID := "objective-1"
	workspaceID := filepath.Base(repo)
	service, err := NewManagedWorkspaceService(filepath.Dir(repo), "dev-workspace.sh", &workspaceFakeRunner{workspace: repo}, workspaceObjective(t, 8), func() time.Time {
		return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	})
	require.NoError(t, err)
	_, err = service.Create(context.Background(), WorkspaceCreateInput{ObjectiveID: objectiveID, ID: workspaceID, Branch: "agent/gate-test"})
	require.NoError(t, err)
	runner := &gateFakeCommandRunner{result: GateCommandResult{Output: "green\n"}}
	probe := &gateFakeProbe{heads: []string{"abc123", "abc123"}}
	gates, err := NewGateRunner(DefaultGateCatalog(), service, runner, probe, func() time.Time { return time.Date(2026, 7, 10, 12, 1, 0, 0, time.UTC) })
	require.NoError(t, err)
	return &gateFixture{objectiveID: objectiveID, workspaceID: workspaceID, service: service, runner: runner, probe: probe, gates: gates}
}

func (f *gateFixture) input(gate string) GateRunInput {
	return GateRunInput{ObjectiveID: f.objectiveID, WorkspaceID: f.workspaceID, Gate: gate}
}

func TestGateRun_PassedAttachesFreshObjectiveEvidence(t *testing.T) {
	f := newGateFixture(t)
	result := f.gates.Run(context.Background(), f.input("studio_unit"))
	require.Equal(t, GatePassed, result.Status)
	require.NotNil(t, result.Receipt)
	assert.Equal(t, ReceiptEvidenceRecorded, result.Receipt.ObjectiveReceipt.Type)
	assert.Equal(t, "gate", result.Receipt.Evidence.Kind)
	assert.Equal(t, "abc123", result.Receipt.WorkspaceHead)
	assert.Equal(t, result.Receipt.PolicyHash, result.Receipt.Evidence.PolicyHash)
	assert.NotEmpty(t, result.Receipt.CatalogHash)
	assert.NotEmpty(t, result.Receipt.GateHash)
	require.Len(t, f.runner.calls, 1)
	assert.Equal(t, []string{filepath.Join(f.service.root, f.workspaceID), "go", "test", "./internal/mcp/studio"}, f.runner.calls[0])
}

func TestGateRun_DomainFailureIsDataWithoutPassingEvidence(t *testing.T) {
	f := newGateFixture(t)
	f.runner.result = GateCommandResult{ExitCode: 7, Output: "tests failed\n"}
	result := f.gates.Run(context.Background(), f.input("studio_unit"))
	assert.Equal(t, GateFailed, result.Status)
	assert.Equal(t, 7, result.ExitCode)
	assert.Contains(t, result.Output, "tests failed")
	assert.Nil(t, result.Receipt)

	receipts, err := f.service.objectives.store.ListReceipts(context.Background(), f.objectiveID)
	require.NoError(t, err)
	for _, receipt := range receipts {
		assert.NotEqual(t, ReceiptEvidenceRecorded, receipt.Type, "red gates must not satisfy close evidence")
	}
}

func TestGateRun_ClassifiesTimeoutInfrastructureAndStale(t *testing.T) {
	t.Run("timed out", func(t *testing.T) {
		f := newGateFixture(t)
		f.runner.err = context.DeadlineExceeded
		result := f.gates.Run(context.Background(), f.input("studio_unit"))
		assert.Equal(t, GateTimedOut, result.Status)
	})
	t.Run("infrastructure error", func(t *testing.T) {
		f := newGateFixture(t)
		f.runner.err = errors.New("executable missing")
		result := f.gates.Run(context.Background(), f.input("studio_unit"))
		assert.Equal(t, GateInfraError, result.Status)
		assert.Contains(t, result.Error, "executable missing")
	})
	t.Run("head changed", func(t *testing.T) {
		f := newGateFixture(t)
		f.probe.heads = []string{"before", "after"}
		result := f.gates.Run(context.Background(), f.input("studio_unit"))
		assert.Equal(t, GateStale, result.Status)
		assert.Equal(t, "before", result.BeforeHead)
		assert.Equal(t, "after", result.AfterHead)
		assert.Nil(t, result.Receipt)
	})
}

func TestGateRun_RejectsCallerSuppliedCommandFragmentsAndForeignAuthority(t *testing.T) {
	f := newGateFixture(t)
	result := f.gates.Run(context.Background(), GateRunInput{
		ObjectiveID: f.objectiveID, WorkspaceID: f.workspaceID, Gate: "go_test",
		Args: map[string]string{"target": "-run=anything"},
	})
	assert.Equal(t, GatePolicyRejected, result.Status)
	assert.Contains(t, result.Error, "rejected")
	assert.Empty(t, f.runner.calls)

	result = f.gates.Run(context.Background(), GateRunInput{
		ObjectiveID: f.objectiveID, WorkspaceID: f.workspaceID, Gate: "studio_unit",
		Args: map[string]string{"cmd": "rm -rf /"},
	})
	assert.Equal(t, GatePolicyRejected, result.Status)
	assert.Empty(t, f.runner.calls)

	result = f.gates.Run(context.Background(), GateRunInput{ObjectiveID: "another-objective", WorkspaceID: f.workspaceID, Gate: "studio_unit"})
	assert.Equal(t, GatePolicyRejected, result.Status)
	assert.Empty(t, f.runner.calls)
}

func TestGateCatalog_IsDeterministicAndToolsRemainUnregisteredByDefault(t *testing.T) {
	f := newGateFixture(t)
	catalog, err := f.gates.Catalog(f.workspaceID)
	require.NoError(t, err)
	assert.NotEmpty(t, catalog.CatalogHash)
	assert.Equal(t, []string{"go_test", "mcp_test", "story_test", "story_validate", "studio_unit", "web_build"}, gateNames(catalog.Gates))

	ctx := context.Background()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "gate-test", Version: "v1"}, nil)
	RegisterGateTools(server, f.gates)
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	_, err = server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "gate-client", Version: "v1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	assert.Contains(t, toolNames(tools.Tools), "gate.catalog")
	assert.Contains(t, toolNames(tools.Tools), "gate.run")
}

func gateNames(definitions []GateDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func toolNames(tools []*mcpsdk.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestGateCatalog_StoryInputsAreConstrainedPaths(t *testing.T) {
	catalog := DefaultGateCatalog()
	story, ok := catalog.Definition("story_test")
	require.True(t, ok)
	args, err := story.render(map[string]string{"app": "stories/bugfix/app.yaml"})
	require.NoError(t, err)
	assert.Equal(t, []string{"run", "./cmd/kitsoki", "test", "flows", "stories/bugfix/app.yaml", "--v"}, args)
	_, err = story.render(map[string]string{"app": "stories/../../outside/app.yaml"})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "rejected"))
}
