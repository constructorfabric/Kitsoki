package reconcile

import (
	"context"
	"fmt"
	"strings"
)

type FetchResult struct {
	Remote    string `json:"remote"`
	Branch    string `json:"branch"`
	TargetRef string `json:"target_ref"`
	OldTarget string `json:"old_target,omitempty"`
	NewTarget string `json:"new_target"`
	Fetched   bool   `json:"fetched"`
}

// LocalBareRemotePublisher is the credential-free publish provider used by
// tests and local development. It publishes to a local/bare Git remote only
// after checking the live remote ref still matches the plan's expected target.
type LocalBareRemotePublisher struct {
	Remote string
}

// LocalBareRemoteFetcher is the credential-free fetch provider used by tests
// and local development. It imports one branch from a local/bare Git remote
// into a named remote-tracking ref without accepting credentials or URLs.
type LocalBareRemoteFetcher struct {
	Remote     string
	RemoteName string
}

func (f LocalBareRemoteFetcher) Fetch(ctx context.Context, workspace, branch string) (FetchResult, error) {
	remotePath := strings.TrimSpace(f.Remote)
	if remotePath == "" {
		return FetchResult{}, fmt.Errorf("capsule reconcile: local bare remote is required")
	}
	remoteName := strings.TrimSpace(f.RemoteName)
	if remoteName == "" {
		remoteName = "origin"
	}
	branch = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(branch), "refs/heads/"), "heads/")
	if branch == "" {
		return FetchResult{}, fmt.Errorf("capsule reconcile: fetch branch is required")
	}
	if strings.Contains(branch, ":") || strings.Contains(remoteName, ":") {
		return FetchResult{}, fmt.Errorf("capsule reconcile: invalid fetch ref")
	}
	target := "refs/remotes/" + remoteName + "/" + branch
	if _, err := git(ctx, workspace, "check-ref-format", "refs/heads/"+branch); err != nil {
		return FetchResult{}, fmt.Errorf("capsule reconcile: invalid fetch branch")
	}
	if _, err := git(ctx, workspace, "check-ref-format", target); err != nil {
		return FetchResult{}, fmt.Errorf("capsule reconcile: invalid fetch target")
	}
	old := ""
	if out, err := git(ctx, workspace, "rev-parse", "--verify", target); err == nil {
		old = strings.TrimSpace(out)
	}
	if _, err := git(ctx, workspace, "fetch", remotePath, "refs/heads/"+branch+":"+target); err != nil {
		return FetchResult{}, err
	}
	newOID, err := git(ctx, workspace, "rev-parse", "--verify", target)
	if err != nil {
		return FetchResult{}, err
	}
	return FetchResult{Remote: remoteName, Branch: branch, TargetRef: target, OldTarget: old, NewTarget: strings.TrimSpace(newOID), Fetched: true}, nil
}

func (p LocalBareRemotePublisher) Publish(ctx context.Context, plan Plan, refs ObservedRefs) (ApplyResult, error) {
	if plan.Operation != Publish {
		return ApplyResult{}, fmt.Errorf("capsule reconcile: local bare publisher cannot apply %q", plan.Operation)
	}
	remote := strings.TrimSpace(p.Remote)
	if remote == "" {
		return ApplyResult{}, fmt.Errorf("capsule reconcile: local bare remote is required")
	}
	target := publishTargetRef(plan.TargetRef, remote)
	live, err := git(ctx, plan.Workspace, "ls-remote", remote, target)
	if err != nil {
		return ApplyResult{}, err
	}
	liveOID := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(live), target))
	if fields := strings.Fields(live); len(fields) > 0 {
		liveOID = fields[0]
	}
	if liveOID != refs.Target {
		return ApplyResult{}, fmt.Errorf("capsule reconcile: stale remote ref %s", target)
	}
	if _, err := git(ctx, plan.Workspace, "push", remote, plan.Candidate+":"+target); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{PlanDigest: plan.Digest, OldTarget: refs.Target, NewTarget: plan.Candidate, Applied: true}, nil
}

func publishTargetRef(target, remote string) string {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "refs/") {
		return target
	}
	prefix := remote + "/"
	if strings.HasPrefix(target, prefix) {
		target = strings.TrimPrefix(target, prefix)
	}
	return "refs/heads/" + strings.TrimPrefix(target, "heads/")
}
