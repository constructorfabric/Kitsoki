// Package corpusreceipt validates and durably records Corpus Forge receipts.
//
// A Registry is deliberately source-neutral: it consumes only the frozen
// corpus-receipt.v1 wire contract produced after independent proof.  It has no
// dependency on Studio sessions, intake adapters, or proof execution.  This
// means a configured FileStore can protect calibration/heldout separation
// across independent Studio sessions, while an unconfigured Registry fails
// closed rather than claiming durability it does not have.
package corpusreceipt
