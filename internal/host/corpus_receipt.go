package host

import (
	"context"

	"kitsoki/internal/corpusreceipt"
)

// CorpusReceiptFreezer is the narrow persistence dependency used by
// host.corpus.freeze_receipt. It is intentionally separate from CorpusProver:
// proof establishes evidence, while this boundary enforces cohort separation
// and configured durability.
type CorpusReceiptFreezer interface {
	Freeze(context.Context, corpusreceipt.Receipt) error
}

// CorpusReceiptHandler adapts an explicit receipt registry to the Corpus Forge
// host capability. A nil registry fails closed; the default Studio runtime does
// not silently create a process-local registry and claim cross-session safety.
func CorpusReceiptHandler(freezer CorpusReceiptFreezer) Handler {
	return func(ctx context.Context, args map[string]any) (Result, error) {
		if freezer == nil {
			return Result{Error: "host.corpus.freeze_receipt: receipt registry is not configured"}, nil
		}
		raw, ok := args["receipt"]
		if !ok || raw == nil {
			return Result{Error: "host.corpus.freeze_receipt: receipt is required"}, nil
		}
		receipt, err := corpusreceipt.Decode(raw)
		if err != nil {
			return Result{Error: "host.corpus.freeze_receipt: " + err.Error()}, nil
		}
		if err := freezer.Freeze(ctx, receipt); err != nil {
			return Result{Error: "host.corpus.freeze_receipt: " + err.Error()}, nil
		}
		return Result{Data: map[string]any{"receipt": raw}}, nil
	}
}
