package testrunner

import (
	"context"
	"errors"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

// RegisterHostStubs installs the flow-fixture host_handlers stubs onto reg,
// using host.ReplacePreservingCapabilities so a stub overrides any builtin of
// the same name without changing its declared capability class. It is
// the single registration path shared by the deterministic flow-test runner
// (RunFlows) and any other surface that wants to drive a session against a
// flow's canned host responses — notably `kitsoki web --flow`, which serves a
// live, interactive session whose agent/host calls are answered by these same
// stubs. Keeping one implementation guarantees the web UI and the flow tests
// resolve a stub identically (Delay, RequestClarification, ByCall, ByOp,
// InfraError, Error, Data — in that resolution order).
//
// The clock used for Delay is taken from the invocation context
// (host.ClockFromContext), so a real run sleeps real time and a fake-clock rig
// sleeps fake time; callers needing deterministic delays must inject a fake
// clock via the scheduler. RequestClarification requires a job store wired into
// the orchestrator (background effects).
func RegisterHostStubs(reg *host.Registry, handlers map[string]HostStub) {
	for name, stub := range handlers {
		stub := stub // capture for closure
		reg.ReplacePreservingCapabilities(name, func(hctx context.Context, args map[string]any) (host.Result, error) {
			// 1. Simulated delay using the clock from context.
			if stub.Delay != "" {
				d, parseErr := app.ParseDuration(stub.Delay)
				if parseErr != nil {
					return host.Result{}, errors.New("stub " + name + ": parse delay: " + parseErr.Error())
				}
				if d > 0 {
					host.ClockFromContext(hctx).Sleep(d)
				}
			}
			// 2. Mid-flight clarification: pause until the user answers.
			if stub.RequestClarification != "" {
				_, cErr := host.RequestClarification(hctx, jobs.ClarificationSchema{
					Prompt: stub.RequestClarification,
					Fields: map[string]string{"answer": "string"},
				})
				if cErr != nil {
					return host.Result{Error: cErr.Error()}, nil
				}
			}
			// 3a. Per-call envelope (ByCall) — dispatch on the author-assigned
			// invoke id threaded into args["call"].
			if len(stub.ByCall) > 0 {
				call, _ := args["call"].(string)
				if env, ok := stub.ByCall[call]; ok {
					if env.InfraError != "" {
						return host.Result{}, errors.New(env.InfraError)
					}
					return host.Result{Data: env.Data, Error: env.Error}, nil
				}
			}
			// 3b. Per-op envelope (ByOp) — dispatch on args["op"].
			if len(stub.ByOp) > 0 {
				op, _ := args["op"].(string)
				if env, ok := stub.ByOp[op]; ok {
					if env.InfraError != "" {
						return host.Result{}, errors.New(env.InfraError)
					}
					return host.Result{Data: env.Data, Error: env.Error}, nil
				}
			}
			// 4. Infrastructure error (indistinguishable from a real failure).
			if stub.InfraError != "" {
				return host.Result{}, errors.New(stub.InfraError)
			}
			// 5. Domain-level error or success.
			return host.Result{Data: stub.Data, Error: stub.Error}, nil
		})
	}
}
