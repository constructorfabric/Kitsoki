package main

import (
	"fmt"
	"strings"

	"kitsoki/internal/capsule/receipt"
)

func capsuleFakeReceiptSigner(id string) (receipt.Signer, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	if strings.ContainsAny(id, "/\\") {
		return nil, fmt.Errorf("capsule receipt: invalid fake signer id %q", id)
	}
	return receipt.FakeSigner{ID: id}, nil
}
