// Package queue provides the durable, receipt-bound admission and processing
// state for the Capsule merge queue. Git integration is deliberately injected:
// the queue serializes candidates and records evidence, while the protected
// workspace lifecycle remains the only component permitted to land a tree.
package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kitsoki/internal/capsule/receipt"
)

const Schema = "capsule-merge-queue/v1"

type Status string

const (
	Queued  Status = "queued"
	Running Status = "running"
	Landed  Status = "landed"
	Ejected Status = "ejected"
)

type Candidate struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	Branch         string    `json:"branch"`
	SHA            string    `json:"sha"`
	ReceiptID      string    `json:"receipt_id"`
	ReceiptRef     string    `json:"receipt_ref,omitempty"`
	Backend        string    `json:"backend"`
	Paths          []string  `json:"paths,omitempty"`
	Position       int       `json:"position"`
	Status         Status    `json:"status"`
	Submitted      time.Time `json:"submitted_at"`
	Started        time.Time `json:"started_at,omitempty"`
	Completed      time.Time `json:"completed_at,omitempty"`
	SpeculativeSHA string    `json:"speculative_sha,omitempty"`
	Evidence       []string  `json:"evidence,omitempty"`
	EjectionReason string    `json:"ejection_reason,omitempty"`
}

type State struct {
	Schema     string      `json:"schema"`
	Candidates []Candidate `json:"candidates"`
}
type PathScopeManifest struct {
	Schema string   `json:"schema"`
	Paths  []string `json:"paths,omitempty"`
}
type Submit struct {
	Branch, SHA         string
	Receipt             receipt.Receipt
	ReceiptRef, Backend string
	Paths               []string
	Now                 time.Time
}

// Integration constructs a speculative tree from the current target and the
// preceding queued candidates, then lands only through the protected lifecycle.
type Integration interface {
	Speculate(context.Context, Candidate, []Candidate) (Speculation, error)
	Land(context.Context, Speculation) error
}
type Speculation struct {
	SHA      string   `json:"sha"`
	Evidence []string `json:"evidence,omitempty"`
}
type Gate interface {
	Run(context.Context, Speculation) (GateResult, error)
}
type GateResult struct {
	Passed   bool     `json:"passed"`
	Evidence []string `json:"evidence,omitempty"`
}
type ProcessDeps struct {
	Integration Integration
	Gate        Gate
	Now         func() time.Time
}

type Store struct {
	ProjectRoot string
	LockWait    time.Duration
}

func (s Store) Submit(in Submit) (Candidate, error) {
	if err := validate(in); err != nil {
		return Candidate{}, err
	}
	return s.mutate(func(state *State) (Candidate, error) {
		for _, c := range state.Candidates {
			if c.SHA == in.SHA && c.ReceiptID == in.Receipt.ReceiptID {
				return c, nil
			}
		}
		now := in.Now.UTC()
		if now.IsZero() {
			now = time.Now().UTC()
		}
		c := Candidate{ID: candidateID(in.SHA, in.Receipt.ReceiptID), ProjectID: in.Receipt.ProjectID, Branch: in.Branch, SHA: in.SHA, ReceiptID: in.Receipt.ReceiptID, ReceiptRef: in.ReceiptRef, Backend: defaultBackend(in.Backend), Paths: cleanPaths(in.Paths), Position: len(state.Candidates) + 1, Status: Queued, Submitted: now}
		state.Candidates = append(state.Candidates, c)
		return c, nil
	})
}

func (s Store) List() (State, error) { return s.readState() }

// Process drains candidates in submission order. A recovered running entry is
// retried deterministically; no terminal candidate can be duplicated.
func (s Store) Process(ctx context.Context, deps ProcessDeps) (State, error) {
	if deps.Integration == nil || deps.Gate == nil {
		return State{}, fmt.Errorf("queue: integration and gate are required")
	}
	return s.withLock(func(path string) (State, error) {
		state, err := read(path)
		if err != nil {
			return State{}, err
		}
		for i := range state.Candidates {
			if state.Candidates[i].Status == Running {
				state.Candidates[i].Status = Queued
				state.Candidates[i].Started = time.Time{}
			}
		}
		if err := write(path, state); err != nil {
			return State{}, err
		}
		for i := range state.Candidates {
			if state.Candidates[i].Status != Queued {
				continue
			}
			c := &state.Candidates[i]
			c.Status = Running
			c.Started = now(deps)
			c.EjectionReason = ""
			c.Evidence = nil
			if err := write(path, state); err != nil {
				return State{}, err
			}
			ahead := activeAhead(state.Candidates[:i])
			spec, err := deps.Integration.Speculate(ctx, *c, ahead)
			if err != nil {
				eject(c, now(deps), "speculation_failed", err.Error())
				if err := write(path, state); err != nil {
					return State{}, err
				}
				continue
			}
			c.SpeculativeSHA = spec.SHA
			c.Evidence = append(c.Evidence, spec.Evidence...)
			result, err := deps.Gate.Run(ctx, spec)
			c.Evidence = append(c.Evidence, result.Evidence...)
			if err != nil {
				eject(c, now(deps), "gate_failed", err.Error())
				if err := write(path, state); err != nil {
					return State{}, err
				}
				continue
			}
			if !result.Passed {
				eject(c, now(deps), "gate_failed", "deterministic gate failed")
				if err := write(path, state); err != nil {
					return State{}, err
				}
				continue
			}
			if err := deps.Integration.Land(ctx, spec); err != nil {
				eject(c, now(deps), "landing_failed", err.Error())
				if err := write(path, state); err != nil {
					return State{}, err
				}
				continue
			}
			c.Status = Landed
			c.Completed = now(deps)
			if err := write(path, state); err != nil {
				return State{}, err
			}
		}
		return state, nil
	})
}

func eject(c *Candidate, at time.Time, reason, evidence string) {
	c.Status = Ejected
	c.Completed = at
	c.EjectionReason = reason
	if evidence != "" {
		c.Evidence = append(c.Evidence, evidence)
	}
}
func activeAhead(cs []Candidate) []Candidate {
	out := make([]Candidate, 0, len(cs))
	for _, c := range cs {
		if c.Status == Queued || c.Status == Running || c.Status == Landed {
			out = append(out, c)
		}
	}
	return out
}
func now(deps ProcessDeps) time.Time {
	if deps.Now != nil {
		return deps.Now().UTC()
	}
	return time.Now().UTC()
}

func (s Store) readState() (State, error) {
	_, path, err := s.paths()
	if err != nil {
		return State{}, err
	}
	return read(path)
}
func (s Store) mutate(fn func(*State) (Candidate, error)) (Candidate, error) {
	var out Candidate
	_, err := s.withLock(func(path string) (State, error) {
		state, err := read(path)
		if err != nil {
			return State{}, err
		}
		out, err = fn(&state)
		if err != nil {
			return State{}, err
		}
		return state, write(path, state)
	})
	return out, err
}
func (s Store) withLock(fn func(string) (State, error)) (State, error) {
	dir, path, err := s.paths()
	if err != nil {
		return State{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return State{}, err
	}
	unlock, err := lock(filepath.Join(dir, "state.lock"), s.LockWait)
	if err != nil {
		return State{}, err
	}
	defer unlock()
	return fn(path)
}
func (s Store) paths() (string, string, error) {
	root, err := filepath.Abs(s.ProjectRoot)
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(root, ".capsules", "queue")
	return dir, filepath.Join(dir, "state.json"), nil
}

func validate(in Submit) error {
	if strings.TrimSpace(in.Branch) == "" || strings.ContainsAny(in.Branch, "\n\r") {
		return fmt.Errorf("queue: branch is required")
	}
	if len(in.SHA) != 40 || strings.Trim(in.SHA, "0123456789abcdef") != "" {
		return fmt.Errorf("queue: candidate SHA must be a lowercase full git SHA")
	}
	if got := receipt.Verify(in.Receipt, nil, false); got.Status != "valid" || !got.PromotionEligible {
		return fmt.Errorf("queue: receipt is not a valid promotion-eligible capsule CI receipt")
	}
	if in.Receipt.Envelope.SourceDigest != in.SHA {
		return fmt.Errorf("queue: receipt candidate SHA does not match submission")
	}
	return nil
}
func read(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{Schema: Schema, Candidates: []Candidate{}}, nil
	}
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return State{}, fmt.Errorf("queue: parse state: %w", err)
	}
	if state.Schema != Schema {
		return State{}, fmt.Errorf("queue: unsupported state schema %q", state.Schema)
	}
	return state, nil
}
func write(path string, state State) error {
	state.Schema = Schema
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(append(raw, '\n')); err == nil {
		err = tmp.Chmod(0o600)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
func lock(path string, wait time.Duration) (func(), error) {
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return func() { _ = f.Close(); _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("queue: acquire serializer: %w", err)
		}
		if wait <= 0 || time.Now().After(deadline) {
			return nil, fmt.Errorf("queue: acquire serializer: %w", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func candidateID(sha, receiptID string) string {
	sum := sha256.Sum256([]byte(sha + "\n" + receiptID))
	return "queue-" + hex.EncodeToString(sum[:])[:12]
}
func defaultBackend(v string) string {
	if strings.TrimSpace(v) == "" {
		return "local"
	}
	return v
}
func cleanPaths(paths []string) []string {
	out := append([]string(nil), paths...)
	sort.Strings(out)
	return out
}
