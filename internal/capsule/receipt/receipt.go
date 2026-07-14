package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/artifactjob"
	"kitsoki/internal/capsule/ci"
	"kitsoki/internal/capsule/executor"
)

const Schema = "capsule-ci-receipt/v1"

type Artifact struct {
	Handle string `json:"handle"`
	Kind   string `json:"kind,omitempty"`
	Digest string `json:"digest,omitempty"`
}
type Integrity struct {
	ContentDigest string `json:"content_digest"`
	Signer        string `json:"signer,omitempty"`
	Signature     string `json:"signature,omitempty"`
}
type Receipt struct {
	Schema      string            `json:"schema"`
	ReceiptID   string            `json:"receipt_id"`
	JobID       string            `json:"job_id"`
	ProjectID   string            `json:"project_id"`
	Envelope    executor.Envelope `json:"envelope"`
	Verdict     ci.Verdict        `json:"verdict"`
	Artifacts   []Artifact        `json:"artifacts,omitempty"`
	TraceDigest string            `json:"trace_digest"`
	Integrity   Integrity         `json:"integrity"`
}
type BuildInput struct {
	Job         artifactjob.Job
	Envelope    executor.Envelope
	Verdict     ci.Verdict
	Artifacts   []Artifact
	TraceDigest string
}
type Verification struct {
	Status            string   `json:"status"`
	Missing           []string `json:"missing,omitempty"`
	Errors            []string `json:"errors,omitempty"`
	PromotionEligible bool     `json:"promotion_eligible"`
}
type Signer interface {
	Name() string
	Sign([]byte) (string, error)
	Verify([]byte, string) error
}
type FakeSigner struct {
	ID string
}

func (s FakeSigner) Name() string {
	if strings.TrimSpace(s.ID) == "" {
		return "fake"
	}
	return s.ID
}
func (s FakeSigner) Sign(b []byte) (string, error) {
	return "fake-signature:" + s.Name() + ":" + string(b), nil
}
func (s FakeSigner) Verify(b []byte, sig string) error {
	want, _ := s.Sign(b)
	if sig != want {
		return fmt.Errorf("capsule receipt: bad signature")
	}
	return nil
}

func Build(in BuildInput) (Receipt, Verification, error) {
	missing := missingFacts(in)
	if len(missing) > 0 {
		return Receipt{}, Verification{Status: "incomplete", Missing: missing}, nil
	}
	if err := ci.ValidateVerdict(in.Verdict, in.Envelope, ci.ResultContract{}); err != nil {
		return Receipt{}, Verification{Status: "invalid", Errors: []string{err.Error()}}, nil
	}
	artifacts := append([]Artifact(nil), in.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Handle < artifacts[j].Handle })
	r := Receipt{Schema: Schema, JobID: string(in.Job.ID), ProjectID: in.Envelope.ProjectID, Envelope: in.Envelope, Verdict: in.Verdict, Artifacts: artifacts, TraceDigest: in.TraceDigest}
	digest, err := digestReceipt(r)
	if err != nil {
		return Receipt{}, Verification{}, err
	}
	r.ReceiptID = digest
	r.Integrity.ContentDigest = digest
	return r, Verification{Status: "valid", PromotionEligible: in.Verdict.PromotionEligible}, nil
}
func Sign(r Receipt, s Signer) (Receipt, error) {
	if s == nil {
		return r, fmt.Errorf("capsule receipt: signer is required")
	}
	if err := verifyContent(r); err != nil {
		return r, err
	}
	sig, err := s.Sign([]byte(r.Integrity.ContentDigest))
	if err != nil {
		return r, err
	}
	r.Integrity.Signer = s.Name()
	r.Integrity.Signature = sig
	return r, nil
}
func Verify(r Receipt, s Signer, requireSignature bool) Verification {
	if r.Schema != Schema {
		return Verification{Status: "invalid", Errors: []string{"unsupported receipt schema"}}
	}
	if err := verifyContent(r); err != nil {
		return Verification{Status: "invalid", Errors: []string{err.Error()}}
	}
	if err := ci.ValidateVerdict(r.Verdict, r.Envelope, ci.ResultContract{}); err != nil {
		return Verification{Status: "invalid", Errors: []string{err.Error()}}
	}
	if requireSignature && (s == nil || r.Integrity.Signature == "") {
		return Verification{Status: "invalid", Errors: []string{"required signature is missing"}}
	}
	if r.Integrity.Signature != "" {
		if s == nil {
			return Verification{Status: "invalid", Errors: []string{"signer unavailable"}}
		}
		if r.Integrity.Signer != s.Name() {
			return Verification{Status: "invalid", Errors: []string{"unexpected signer"}}
		}
		if err := s.Verify([]byte(r.Integrity.ContentDigest), r.Integrity.Signature); err != nil {
			return Verification{Status: "invalid", Errors: []string{err.Error()}}
		}
	}
	return Verification{Status: "valid", PromotionEligible: r.Verdict.PromotionEligible}
}
func CanonicalJSON(r Receipt) ([]byte, error) {
	r.ReceiptID = ""
	r.Integrity.ContentDigest = ""
	r.Integrity.Signer = ""
	r.Integrity.Signature = ""
	return json.Marshal(r)
}
func digestReceipt(r Receipt) (string, error) {
	raw, err := CanonicalJSON(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
func verifyContent(r Receipt) error {
	want := r.Integrity.ContentDigest
	if want == "" {
		want = r.ReceiptID
	}
	got, err := digestReceipt(r)
	if err != nil {
		return err
	}
	if want == "" || want != got || r.ReceiptID != got {
		return fmt.Errorf("capsule receipt: content digest mismatch")
	}
	return nil
}
func missingFacts(in BuildInput) []string {
	var out []string
	if in.Job.ID == "" {
		out = append(out, "job_id")
	}
	if in.Envelope.Digest == "" {
		out = append(out, "envelope_digest")
	}
	if in.Envelope.Environment.Digest == "" {
		out = append(out, "environment_lock")
	}
	if in.Verdict.Schema == "" {
		out = append(out, "verdict")
	}
	if strings.TrimSpace(in.TraceDigest) == "" {
		out = append(out, "trace_digest")
	}
	return out
}
