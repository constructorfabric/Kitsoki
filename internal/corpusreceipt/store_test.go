package corpusreceipt_test

import (
	"context"
	"testing"

	"kitsoki/internal/corpusreceipt"
)

func TestFileStorePersistsAcrossRegistries(t *testing.T) {
	dir := t.TempDir()
	store, err := corpusreceipt.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := (corpusreceipt.Registry{Store: store}).Freeze(context.Background(), receipt("calibration-a", corpusreceipt.RoleCalibration, "case-1")); err != nil {
		t.Fatal(err)
	}
	storeAgain, err := corpusreceipt.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = (corpusreceipt.Registry{Store: storeAgain}).Freeze(context.Background(), receipt("heldout-a", corpusreceipt.RoleHeldout, "case-1"))
	if err == nil {
		t.Fatal("second registry accepted cross-session overlap")
	}
}
