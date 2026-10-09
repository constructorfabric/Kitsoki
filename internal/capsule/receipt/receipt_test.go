package receipt

import (
	"fmt"
	"testing"

	"kitsoki/internal/artifactjob"
	"kitsoki/internal/capsule/ci"
	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
)

type fakeSigner struct{}

func (fakeSigner) Name() string                  { return "fake" }
func (fakeSigner) Sign(b []byte) (string, error) { return "sig:" + string(b), nil }
func (fakeSigner) Verify(b []byte, s string) error {
	if s != "sig:"+string(b) {
		return fmt.Errorf("bad signature")
	}
	return nil
}
func validInput() BuildInput {
	lock, err := environment.SealLock(environment.Lock{Schema: environment.LockSchema, ID: "ci", DefinitionDigest: "sha256:env-def", Network: "none", Sandbox: "supervised"})
	if err != nil {
		panic(err)
	}
	e, err := executor.Seal(executor.Envelope{JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: "sha256:source", StoryPath: "story", StoryDigest: "sha256:story", Environment: lock, Policy: executor.Policy{Network: "none"}})
	if err != nil {
		panic(err)
	}
	v := ci.Verdict{Schema: ci.VerdictSchema, Pipeline: "change", Outcome: "passed", Checks: []ci.Check{{ID: "tests", Kind: "deterministic", Outcome: "passed", Evidence: []string{"artifact:tests"}}}, PromotionEligible: true, SourceDigest: e.SourceDigest, StoryDigest: e.StoryDigest, EnvironmentDigest: e.Environment.Digest, EnvelopeDigest: e.Digest}
	return BuildInput{Job: artifactjob.Job{ID: "job"}, Envelope: e, Verdict: v, Artifacts: []Artifact{{Handle: "b"}, {Handle: "a"}}, TraceDigest: "sha256:trace"}
}
func TestReceiptIsCanonicalAndTamperFails(t *testing.T) {
	first, v, err := Build(validInput())
	if err != nil || v.Status != "valid" {
		t.Fatalf("build %#v %v", v, err)
	}
	second, _, _ := Build(validInput())
	if first.ReceiptID != second.ReceiptID {
		t.Fatal("receipt was not stable")
	}
	signed, err := Sign(first, fakeSigner{})
	if err != nil {
		t.Fatal(err)
	}
	if got := Verify(signed, fakeSigner{}, true); got.Status != "valid" || !got.PromotionEligible {
		t.Fatalf("verify %#v", got)
	}
	signed.TraceDigest = "sha256:tampered"
	if got := Verify(signed, fakeSigner{}, true); got.Status != "invalid" {
		t.Fatalf("tamper accepted %#v", got)
	}
}

func TestReceiptRejectsKeyFieldTampering(t *testing.T) {
	base, v, err := Build(validInput())
	if err != nil || v.Status != "valid" {
		t.Fatalf("build %#v %v", v, err)
	}
	signed, err := Sign(base, fakeSigner{})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Receipt){
		"source": func(r *Receipt) {
			r.Envelope.SourceDigest = "sha256:other-source"
		},
		"story": func(r *Receipt) {
			r.Envelope.StoryDigest = "sha256:other-story"
		},
		"environment": func(r *Receipt) {
			r.Envelope.Environment.Digest = "sha256:other-env"
		},
		"policy": func(r *Receipt) {
			r.Envelope.Policy.Network = "live"
		},
		"artifact": func(r *Receipt) {
			r.Artifacts[0].Handle = "artifact:other"
		},
		"signer": func(r *Receipt) {
			r.Integrity.Signer = "other"
		},
		"signature": func(r *Receipt) {
			r.Integrity.Signature = "bad"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			tampered := signed
			mutate(&tampered)
			if got := Verify(tampered, fakeSigner{}, true); got.Status != "invalid" {
				t.Fatalf("tamper accepted %#v", got)
			}
		})
	}
}

func TestVerifyRejectsAContentValidReceiptWithInvalidVerdict(t *testing.T) {
	r, _, err := Build(validInput())
	if err != nil {
		t.Fatal(err)
	}
	r.Verdict.Outcome = "not-a-verdict"
	digest, err := digestReceipt(r)
	if err != nil {
		t.Fatal(err)
	}
	r.ReceiptID, r.Integrity.ContentDigest = digest, digest
	if got := Verify(r, nil, false); got.Status != "invalid" {
		t.Fatalf("invalid verdict accepted %#v", got)
	}
}
