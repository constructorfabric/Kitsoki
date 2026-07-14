package host_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"kitsoki/internal/bugprivacy"
	"kitsoki/internal/host"
)

func TestBugPrivacyAgentCheckerUsesTopLadderRung(t *testing.T) {
	var calls int
	runner := func(_ context.Context, args []string, stdin, _ string) (host.ClaudeRun, error) {
		calls++
		if got := flagValue(args, "--model"); got != "cheap-model" {
			t.Fatalf("model = %q, want top ladder model", got)
		}
		if got := flagValue(args, "--effort"); got != "low" {
			t.Fatalf("effort = %q, want top ladder effort", got)
		}
		if strings.Contains(stdin, "expensive-model") {
			t.Fatalf("privacy prompt should not mention later ladder rungs: %s", stdin)
		}
		out := host.ParseMCPConfigSubmitOutput(args)
		if out == "" {
			t.Fatal("validator output path not found")
		}
		if err := os.WriteFile(out, []byte(`{"decision":"pass","categories":[],"reason":"safe"}`), 0o600); err != nil {
			t.Fatalf("write validator output: %v", err)
		}
		return host.ClaudeRun{Stdout: "safe"}, nil
	}
	ctx := host.WithClaudeRunner(context.Background(), runner)
	checker := host.NewBugPrivacyAgentChecker(host.LadderConfig{
		Models: []host.LadderModel{
			{Backend: "claude", Provider: "cheap", Model: "cheap-model"},
			{Backend: "claude", Provider: "expensive", Model: "expensive-model"},
		},
		Efforts: []string{"low", "high"},
	}, map[string]host.Provider{
		"cheap":     {Model: "cheap-model"},
		"expensive": {Model: "expensive-model"},
	}, "")

	decision, err := checker.CheckBugReportPrivacy(ctx, bugprivacy.Report{
		Surface: "test",
		Title:   "safe report",
		Body:    "nothing sensitive",
	}, nil)
	if err != nil {
		t.Fatalf("CheckBugReportPrivacy: %v", err)
	}
	if !decision.Pass || decision.Reason != "safe" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want exactly one top-rung decide", calls)
	}
}

func flagValue(args []string, flag string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}
