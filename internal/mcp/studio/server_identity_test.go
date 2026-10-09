package studio

import "testing"

func TestPingIdentityUsesExplicitKitsokiRepoOutsideProject(t *testing.T) {
	const (
		downstream  = "/work/gears-rust"
		kitsokiRepo = "/work/Kitsoki/staging"
		kitsokiHead = "kitsoki-head"
	)
	var dirs []string
	gitOutput := func(dir string, args ...string) string {
		dirs = append(dirs, dir)
		if len(args) == 2 && args[0] == "rev-parse" && args[1] == "HEAD" {
			if dir == kitsokiRepo {
				return kitsokiHead
			}
			return "downstream-head"
		}
		return ""
	}

	got := applyPingCheckoutIdentity(PingOK{
		OK: true, Revision: kitsokiHead, Modified: "false",
	}, downstream, kitsokiRepo, gitOutput)
	if got.WorkingDir != downstream {
		t.Fatalf("working_dir = %q, want downstream %q", got.WorkingDir, downstream)
	}
	if got.Checkout != kitsokiHead {
		t.Fatalf("checkout = %q, want explicit Kitsoki HEAD %q", got.Checkout, kitsokiHead)
	}
	if got.Stale || got.ReloadHint != "" {
		t.Fatalf("matching explicit Kitsoki checkout reported stale: %+v", got)
	}
	if len(dirs) != 1 || dirs[0] != kitsokiRepo {
		t.Fatalf("git identity dirs = %#v, want only %q", dirs, kitsokiRepo)
	}
}
