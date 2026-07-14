package host_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/host/agentruntime"
	"kitsoki/internal/store"
)

func TestAgentStreamerSandbox_EmitsRuntimeEventsAndParsesReply(t *testing.T) {
	t.Parallel()
	sink := &memSink{}
	ctx := agentCtxForTest(sink)
	ctx = host.WithCallID(ctx, "call-runtime-1")
	ctx = host.WithAgentRuntimeRegistry(ctx, agentruntime.NewRegistry(&agentruntime.Fake{
		Backend:  "fake-fs",
		Strength: agentruntime.StrengthFSConfined,
		LaunchResult: agentruntime.Result{
			Stdout: strings.Join([]string{
				`{"type":"system","subtype":"init","session_id":"sess-runtime"}`,
				`{"type":"result","subtype":"success","result":"sandboxed ok","session_id":"sess-runtime"}`,
			}, "\n") + "\n",
		},
	}))

	cr, sid, err := (host.AgentStreamer{
		Bin:        "fake-agent",
		CLIArgs:    []string{"-p"},
		Stdin:      "prompt",
		WorkingDir: ".",
		Sandbox: &host.AgentSandboxSpec{
			MinStrength: agentruntime.StrengthFSConfined,
			Repo:        agentruntime.RepoReadOnly,
			RW:          []string{".artifacts/goal"},
			Degrade:     agentruntime.DegradeFail,
		},
	}).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Stdout != "sandboxed ok" {
		t.Fatalf("stdout = %q", cr.Stdout)
	}
	if sid != "sess-runtime" {
		t.Fatalf("session id = %q", sid)
	}

	var sawStart, sawEnd bool
	for _, ev := range sink.events {
		switch string(ev.Kind) {
		case "agent.runtime.start":
			sawStart = true
			if ev.CallID != "call-runtime-1" {
				t.Fatalf("runtime start call_id = %q", ev.CallID)
			}
			var payload map[string]any
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatalf("start payload: %v", err)
			}
			if payload["backend"] != "fake-fs" || payload["strength"] != "fs_confined" || payload["repo"] != "read_only" {
				t.Fatalf("unexpected start payload: %#v", payload)
			}
		case "agent.runtime.end":
			sawEnd = true
		}
	}
	if !sawStart || !sawEnd {
		t.Fatalf("missing runtime events: %#v", sink.events)
	}
}

func TestAgentStreamerSandbox_FakeFSAllowsDeclaredWriteAndDeniesRepoWrite(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	allowed := filepath.Join(repo, ".artifacts/goal")

	sink := &memSink{}
	ctx := host.WithCallID(agentCtxForTest(sink), "call-runtime-fs")
	ctx = host.WithAgentRuntimeRegistry(ctx, agentruntime.NewRegistry(&agentruntime.Fake{
		Backend:   "fake-fs",
		Strength:  agentruntime.StrengthFSConfined,
		EnforceFS: true,
		Writes: []agentruntime.FakeWriteAttempt{
			{Path: filepath.Join(allowed, "ok.txt"), Content: "ok"},
		},
		LaunchResult: agentruntime.Result{
			Stdout: `{"type":"result","subtype":"success","result":"accepted","session_id":"sess-fs"}` + "\n",
		},
	}))
	cr, _, err := (host.AgentStreamer{
		Bin:        "fake-agent",
		CLIArgs:    []string{"-p"},
		Stdin:      "prompt",
		WorkingDir: repo,
		Sandbox: &host.AgentSandboxSpec{
			MinStrength: agentruntime.StrengthFSConfined,
			Repo:        agentruntime.RepoReadOnly,
			RW:          []string{allowed},
			Degrade:     agentruntime.DegradeFail,
		},
	}).Run(ctx)
	if err != nil {
		t.Fatalf("Run allowed: %v", err)
	}
	if cr.ExitCode != 0 || cr.Stdout != "accepted" {
		t.Fatalf("allowed run = %#v", cr)
	}
	if got, err := os.ReadFile(filepath.Join(allowed, "ok.txt")); err != nil || string(got) != "ok" {
		t.Fatalf("allowed file = %q err=%v", got, err)
	}
	if !hasRuntimeEvent(sink.events, "agent.runtime.start", "fs_confined", 0) {
		t.Fatalf("allowed run missing fs_confined runtime start/end events: %#v", sink.events)
	}

	sink = &memSink{}
	ctx = host.WithCallID(agentCtxForTest(sink), "call-runtime-fs-deny")
	ctx = host.WithAgentRuntimeRegistry(ctx, agentruntime.NewRegistry(&agentruntime.Fake{
		Backend:   "fake-fs",
		Strength:  agentruntime.StrengthFSConfined,
		EnforceFS: true,
		Writes: []agentruntime.FakeWriteAttempt{
			{Path: filepath.Join(repo, "outside.txt"), Content: "denied"},
		},
		LaunchResult: agentruntime.Result{
			Stdout: `{"type":"result","subtype":"success","result":"should not matter","session_id":"sess-fs"}` + "\n",
		},
	}))
	cr, _, err = (host.AgentStreamer{
		Bin:        "fake-agent",
		CLIArgs:    []string{"-p"},
		Stdin:      "prompt",
		WorkingDir: repo,
		Sandbox: &host.AgentSandboxSpec{
			MinStrength: agentruntime.StrengthFSConfined,
			Repo:        agentruntime.RepoReadOnly,
			RW:          []string{allowed},
			Degrade:     agentruntime.DegradeFail,
		},
	}).Run(ctx)
	if err != nil {
		t.Fatalf("Run denied: %v", err)
	}
	if cr.ExitCode == 0 || !strings.Contains(cr.Stderr, "repo is read_only") {
		t.Fatalf("denied run = %#v", cr)
	}
	if _, err := os.Stat(filepath.Join(repo, "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied write created outside file, err=%v", err)
	}
	if !hasRuntimeEvent(sink.events, "agent.runtime.end", "fs_confined", 126) {
		t.Fatalf("denied run missing loud runtime end event: %#v", sink.events)
	}
}

func hasRuntimeEvent(events []store.Event, kind, strength string, exitCode float64) bool {
	for _, ev := range events {
		if string(ev.Kind) != kind {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			continue
		}
		if payload["strength"] != strength {
			continue
		}
		if exitCode != 0 && payload["exit_code"] != exitCode {
			continue
		}
		return true
	}
	return false
}

func TestAgentStreamerSandbox_FailsClosedWhenMinStrengthUnmet(t *testing.T) {
	t.Parallel()
	ctx := host.WithCallID(context.Background(), "call-runtime-fail")
	ctx = host.WithAgentRuntimeRegistry(ctx, agentruntime.NewRegistry(agentruntime.NewFake(agentruntime.StrengthSupervised)))

	cr, _, err := (host.AgentStreamer{
		Bin:        "fake-agent",
		CLIArgs:    []string{"-p"},
		Stdin:      "prompt",
		WorkingDir: ".",
		Sandbox: &host.AgentSandboxSpec{
			MinStrength: agentruntime.StrengthFSConfined,
			Degrade:     agentruntime.DegradeFail,
		},
	}).Run(ctx)
	if err != nil {
		t.Fatalf("Run returned Go error: %v", err)
	}
	if cr.Infra == nil || !strings.Contains(cr.Infra.Error(), "no backend satisfies") {
		t.Fatalf("infra = %v, want no backend satisfies", cr.Infra)
	}
}
