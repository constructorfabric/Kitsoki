package record

import (
	"context"
	"testing"

	"kitsoki/internal/artifactjob"
	"kitsoki/internal/capsule/ci"
	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
	"kitsoki/internal/capsule/reconcile"
)

func TestPromotionGateRequiresMatchingVerifiedCandidateReceipt(t *testing.T) {
	root := t.TempDir()
	lock, err := environment.SealLock(environment.Lock{Schema: environment.LockSchema, ID: "ci", DefinitionDigest: "sha256:env-def", Network: "none", Sandbox: "supervised"})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := executor.Seal(executor.Envelope{
		JobID: "job", ProjectID: "p", DefinitionDigest: "sha256:def",
		Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: "candidate",
		StoryPath: "stories/ci/app.yaml", StoryDigest: "sha256:story", Environment: lock,
		Policy: executor.Policy{Network: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	verdict := ci.Verdict{Schema: ci.VerdictSchema, Pipeline: "change", Outcome: "passed", Checks: []ci.Check{{ID: "test", Outcome: "passed", Evidence: []string{"artifact:test"}}}, PromotionEligible: true, SourceDigest: envelope.SourceDigest, StoryDigest: envelope.StoryDigest, EnvironmentDigest: envelope.Environment.Digest, EnvelopeDigest: envelope.Digest}
	result := ci.RunResult{Job: artifactjob.Job{ID: "job"}, Envelope: envelope, Verdict: verdict, Execution: executor.Result{VerdictArtifact: "artifact:verdict"}}
	stored, err := Persist(root, result)
	if err != nil {
		t.Fatal(err)
	}
	if err := (ci.FileRunStore{ProjectRoot: root}).Write(ci.RunRecord{JobID: "job", Result: result, ReceiptID: stored.Receipt.ReceiptID, ReceiptVerification: stored.Verification.Status}); err != nil {
		t.Fatal(err)
	}
	gate := PromotionGate{ProjectRoot: root}
	plan := reconcile.Plan{Candidate: "candidate"}
	if err := gate.Verify(context.Background(), stored.Receipt.ReceiptID, plan); err != nil {
		t.Fatal(err)
	}
	plan.Candidate = "other"
	if err := gate.Verify(context.Background(), stored.Receipt.ReceiptID, plan); err == nil {
		t.Fatal("accepted receipt for another candidate")
	}
}
