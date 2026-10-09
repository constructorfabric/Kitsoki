package main

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type terminalOutputCaptureModel struct{}

func (terminalOutputCaptureModel) Init() tea.Cmd { return nil }
func (terminalOutputCaptureModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return terminalOutputCaptureModel{}, nil
}
func (terminalOutputCaptureModel) View() string { return "probe" }

func TestCaptureTUIProcessOutputRedirectsAndRestoresWriters(t *testing.T) {
	// Process-global stdout/stderr are intentionally exercised here. Do not make
	// this test parallel.
	cmd := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	program := tea.NewProgram(terminalOutputCaptureModel{}, tea.WithoutRenderer())
	priorStdout, priorStderr := os.Stdout, os.Stderr

	restore, err := captureTUIProcessOutput(cmd, program)
	require.NoError(t, err)
	t.Cleanup(restore)
	require.NotSame(t, priorStdout, os.Stdout)
	require.NotSame(t, priorStderr, os.Stderr)
	require.Same(t, os.Stdout, cmd.OutOrStdout())
	require.Same(t, os.Stderr, cmd.ErrOrStderr())

	// This is the exact cursor-only shape that corrupted the live region. It
	// must drain without sending a message to the not-yet-running program.
	_, err = fmt.Fprint(os.Stderr, "\n\x1b[?25l\x1b[?25h\n")
	require.NoError(t, err)
	restore()
	restore() // idempotent

	require.Same(t, priorStdout, os.Stdout)
	require.Same(t, priorStderr, os.Stderr)
	require.Same(t, &stdout, cmd.OutOrStdout())
	require.Same(t, &stderr, cmd.ErrOrStderr())
}
