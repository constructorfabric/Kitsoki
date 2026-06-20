// inprocess.go implements the in-process transport.
//
// New wraps an AskFunc as an Agent. This is the seam tests use for a
// deterministic or stubbed agent: New(func(...) (AskResponse, error) {...}).
// It is also the building block for compiled-in custom agents (tools, stubs,
// deterministic decision trees) that ship alongside kitsoki without any
// subprocess or network overhead.

package agent

import "context"

// AskFunc is the function signature for an in-process agent implementation.
// It is identical to Agent.Ask's signature minus the receiver.
type AskFunc func(ctx context.Context, req AskRequest) (AskResponse, error)

// inProcessAgent wraps an AskFunc as an Agent.
type inProcessAgent struct {
	fn AskFunc
}

// New returns an Agent backed by fn. Ask calls fn directly; Close is a no-op.
// fn MUST honour ctx.Done() — if the caller cancels the context while fn is
// running, fn should return the context error so kitsoki can write AgentError.
func New(fn AskFunc) Agent {
	return inProcessAgent{fn: fn}
}

// Ask calls the wrapped AskFunc. If fn returns a nil error but the context is
// already done, Ask returns the context error so callers that check ctx.Err()
// after a timeout get the expected behaviour. In practice fn should check
// ctx.Done() itself, but this provides a safety net for functions that return
// before checking.
func (o inProcessAgent) Ask(ctx context.Context, req AskRequest) (AskResponse, error) {
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

// Close is a no-op for in-process agents.
func (o inProcessAgent) Close() error { return nil }
