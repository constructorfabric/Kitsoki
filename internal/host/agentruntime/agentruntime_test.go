package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRegistrySelectsSmallestSatisfyingBackend(t *testing.T) {
	reg := NewRegistry(NewFake(StrengthVMConfined), NewSupervised(), NewFake(StrengthFSConfined))
	rt, cap, ok := reg.Select(context.Background(), StrengthFSConfined)
	if !ok {
		t.Fatal("expected matching runtime")
	}
	if cap.Strength != StrengthFSConfined {
		t.Fatalf("strength = %s, want fs_confined", cap.Strength)
	}
	if rt.Name() != "fake" {
		t.Fatalf("runtime = %s, want fake", rt.Name())
	}
}

func TestSupervisedUsesTempHomeAndAllowlistedEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	rt := NewSupervised()
	running, policy, err := rt.Launch(context.Background(), LaunchSpec{
		Command: "/bin/sh",
		Args:    []string{"-c", "printf 'home=%s\\nsecret=%s\\nprovider=%s\\n' \"$HOME\" \"$SECRET_TOKEN\" \"$ANTHROPIC_API_KEY\""},
		Env:     []string{"PATH=" + os.Getenv("PATH"), "SECRET_TOKEN=leak", "ANTHROPIC_API_KEY=model-key"},
		Min:     StrengthSupervised,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	res, err := running.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "home="+policy.TempHome) {
		t.Fatalf("stdout %q does not contain temp HOME %q", res.Stdout, policy.TempHome)
	}
	if strings.Contains(res.Stdout, "leak") {
		t.Fatalf("SECRET_TOKEN leaked into supervised env: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "provider=model-key") {
		t.Fatalf("provider env was not preserved: %q", res.Stdout)
	}
}

func TestSupervisedKillsProcessGroupOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	rt := NewSupervised()
	running, _, err := rt.Launch(context.Background(), LaunchSpec{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 5 & wait"},
		Env:     []string{"PATH=" + os.Getenv("PATH")},
		Min:     StrengthSupervised,
		Resources: ResourcePolicy{
			Timeout: 100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	res, err := running.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !res.Killed {
		t.Fatalf("expected timeout kill, got %#v", res)
	}
}

func TestSupervisedCapturesFinalDiff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		running, _, err := NewSupervised().Launch(context.Background(), LaunchSpec{
			Command:  args[0],
			Args:     args[1:],
			Dir:      repo,
			Env:      []string{"PATH=" + os.Getenv("PATH")},
			Min:      StrengthSupervised,
			RepoRoot: repo,
		})
		if err != nil {
			t.Fatalf("Launch %v: %v", args, err)
		}
		if res, err := running.Wait(context.Background()); err != nil || res.ExitCode != 0 {
			t.Fatalf("run %v: res=%#v err=%v", args, res, err)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "tracked.txt")
	run("git", "commit", "-m", "init")
	running, _, err := NewSupervised().Launch(context.Background(), LaunchSpec{
		Command:  "/bin/sh",
		Args:     []string{"-c", "printf new > tracked.txt"},
		Dir:      repo,
		Env:      []string{"PATH=" + os.Getenv("PATH")},
		Min:      StrengthSupervised,
		RepoRoot: repo,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	res, err := running.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !strings.Contains(res.FinalDiff, "-old") || !strings.Contains(res.FinalDiff, "+new") {
		t.Fatalf("missing final diff, got:\n%s", res.FinalDiff)
	}
}

func TestFakeEnforceFSAllowsDeclaredRWAndDeniesRepoWrite(t *testing.T) {
	repo := t.TempDir()
	fake := &Fake{
		Backend:   "fake-fs",
		Strength:  StrengthFSConfined,
		EnforceFS: true,
		Writes: []FakeWriteAttempt{
			{Path: "allowed/out.txt", Content: "ok"},
		},
	}
	running, policy, err := fake.Launch(context.Background(), LaunchSpec{
		Dir:      repo,
		RepoRoot: repo,
		Min:      StrengthFSConfined,
		Repo:     RepoReadOnly,
		RW:       []string{"allowed"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if policy.Strength != StrengthFSConfined {
		t.Fatalf("strength = %s", policy.Strength)
	}
	res, err := running.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("allowed write failed: %#v", res)
	}
	if got, err := os.ReadFile(filepath.Join(repo, "allowed/out.txt")); err != nil || string(got) != "ok" {
		t.Fatalf("allowed write not present, got %q err=%v", got, err)
	}

	fake.Writes = []FakeWriteAttempt{{Path: "outside.txt", Content: "nope"}}
	running, _, err = fake.Launch(context.Background(), LaunchSpec{
		Dir:      repo,
		RepoRoot: repo,
		Min:      StrengthFSConfined,
		Repo:     RepoReadOnly,
		RW:       []string{"allowed"},
	})
	if err != nil {
		t.Fatalf("Launch denied case: %v", err)
	}
	res, err = running.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait denied case: %v", err)
	}
	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "repo is read_only") {
		t.Fatalf("repo write was not denied loudly: %#v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied write created outside.txt, err=%v", err)
	}
}
