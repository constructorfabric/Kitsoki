package studio

import "testing"

func TestDiagnoseAttachment_EvidenceBackedFindings(t *testing.T) {
	got := diagnoseAttachment(PingOK{
		OK: true, Checkout: "/checkout-old", Revision: "old", Stale: true,
		Executable: "/bin/kitsoki-old", WorkingDir: "/repo/old",
	}, DiagnoseArgs{
		ExpectedCheckout: "/checkout-new", ExpectedExecutable: "/bin/kitsoki-new", ExpectedWorkingDir: "/repo/new",
		ExpectedProfile: "strict", AttachedProfile: "legacy",
	})
	if len(got.Findings) != 4 {
		t.Fatalf("findings = %#v, want four evidence-backed discrepancies", got.Findings)
	}
	classes := map[string]int{}
	for _, finding := range got.Findings {
		classes[finding.Class]++
		if finding.NextCall.Tool == "" || len(finding.Evidence) == 0 {
			t.Fatalf("finding must carry evidence and next call: %#v", finding)
		}
	}
	if classes["stale_executable"] != 2 || classes["wrong_working_directory"] != 1 || classes["attachment_profile_mismatch"] != 1 {
		t.Fatalf("classes = %#v", classes)
	}
}

func TestDiagnoseAttachment_DoesNotInventMissingExpectation(t *testing.T) {
	got := diagnoseAttachment(PingOK{OK: true, Checkout: "abc", WorkingDir: "/repo"}, DiagnoseArgs{})
	if len(got.Findings) != 0 {
		t.Fatalf("findings = %#v, want no claim without an expectation", got.Findings)
	}
}
