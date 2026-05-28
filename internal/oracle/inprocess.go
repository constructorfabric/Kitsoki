// Package oracle — in-process transport.
//
// New wraps an AskFunc as an Oracle. This is the seam tests use throughout
// phase B: any test that needs a deterministic or stubbed oracle constructs one
// with New(func(...) AskResponse {...}).
//
// The in-process transport is also the building block for compiled-in custom
// oracles (tools, stubs, deterministic decision trees) that ship alongside
// kitsoki without any subprocess or network overhead.
package oracle

import "context"

// AskFunc is the function signature for an in-process oracle implementation.
// It is identical to Oracle.Ask's signature minus the receiver.
type AskFunc func(ctx context.Context, req AskRequest) (AskResponse, error)

// inProcessOracle wraps an AskFunc as an Oracle.
type inProcessOracle struct {
	fn AskFunc
}

// New returns an Oracle backed by fn. Ask calls fn directly; Close is a no-op.
// fn MUST honour ctx.Done() — if the caller cancels the context while fn is
// running, fn should return the context error so kitsoki can write OracleError.
func New(fn AskFunc) Oracle {
	return inProcessOracle{fn: fn}
}

// Ask calls the wrapped AskFunc. If fn returns a nil error but the context is
// already done, Ask returns the context error so callers that check ctx.Err()
// after a timeout get the expected behaviour. In practice fn should check
// ctx.Done() itself, but this provides a safety net for functions that return
// before checking.
func (o inProcessOracle) Ask(ctx context.Context, req AskRequest) (AskResponse, error) {
	resp, err := o.fn(ctx, req)
	if err != nil {
		return AskResponse{}, err
	}
	// Safety net: if the context was cancelled during the call, propagate it.
	if ctx.Err() != nil {
		return AskResponse{}, ctx.Err()
	}
	return resp, nil
}

// Close is a no-op for in-process oracles.
func (o inProcessOracle) Close() error { return nil }
