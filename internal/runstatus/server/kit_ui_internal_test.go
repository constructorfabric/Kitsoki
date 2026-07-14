package server

import (
	"strings"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/kit"
	"kitsoki/internal/kitendpoint"
)

// fakeIndexHTML stands in for the real vite-plugin-singlefile bundle
// (internal/runstatus/web's assets/index.html), which is gitignored and not
// staged in this dev environment (see assets/.gitkeep) — injectKitRegistry
// only depends on there being a <head>...</head>, so a synthetic stand-in
// exercises the splicing logic without needing `make build`.
const fakeIndexHTML = "<!doctype html><html><head><title>t</title></head><body></body></html>"

func newTestUIKitDispatcher(t *testing.T) *kitendpoint.Dispatcher {
	t.Helper()
	def, err := kit.LoadDir("testdata/kits/uikit")
	if err != nil {
		t.Fatalf("kit.LoadDir: %v", err)
	}
	kits := kit.NewRegistry()
	if err := kits.Add(def); err != nil {
		t.Fatalf("kits.Add: %v", err)
	}
	return kitendpoint.NewDispatcher(kits, host.NewRegistry())
}

func TestInjectKitRegistry_SplicesBeforeHeadClose(t *testing.T) {
	srv := &Server{kits: newTestUIKitDispatcher(t)}
	out := srv.injectKitRegistry([]byte(fakeIndexHTML))
	html := string(out)

	if !strings.Contains(html, `id="kitsoki-kits"`) {
		t.Fatalf("expected an injected kitsoki-kits script, got: %s", html)
	}
	if !strings.Contains(html, `"kit":"uikit"`) {
		t.Errorf("expected the uikit fixture in the registry JSON, got: %s", html)
	}
	if !strings.Contains(html, `type="importmap"`) {
		t.Errorf("expected an injected import map, got: %s", html)
	}
	if !strings.Contains(html, `@kitsoki-test/uikit/ui/panel`) {
		t.Errorf("expected the import map to key on the kit's ui module id, got: %s", html)
	}

	headClose := strings.Index(html, "</head>")
	kitsScript := strings.Index(html, `id="kitsoki-kits"`)
	if headClose < 0 || kitsScript < 0 || kitsScript > headClose {
		t.Errorf("expected the injected script before </head>; headClose=%d kitsScript=%d", headClose, kitsScript)
	}
	// Original body must be untouched.
	if !strings.Contains(html, "<body></body>") {
		t.Errorf("expected the original body to be preserved, got: %s", html)
	}
}

func TestInjectKitRegistry_NoDispatcherIsNoop(t *testing.T) {
	srv := &Server{}
	out := srv.injectKitRegistry([]byte(fakeIndexHTML))
	if string(out) != fakeIndexHTML {
		t.Errorf("expected no change with no dispatcher attached, got: %s", out)
	}
}

func TestInjectKitRegistry_EmptyKitRegistryIsNoop(t *testing.T) {
	kits := kit.NewRegistry()
	srv := &Server{kits: kitendpoint.NewDispatcher(kits, host.NewRegistry())}
	out := srv.injectKitRegistry([]byte(fakeIndexHTML))
	if string(out) != fakeIndexHTML {
		t.Errorf("expected no change with zero kits installed, got: %s", out)
	}
}

func TestInjectKitRegistry_NoHeadCloseIsNoop(t *testing.T) {
	srv := &Server{kits: newTestUIKitDispatcher(t)}
	noHead := "<html><body>no head tag here</body></html>"
	out := srv.injectKitRegistry([]byte(noHead))
	if string(out) != noHead {
		t.Errorf("expected no change when </head> is absent, got: %s", out)
	}
}

func TestKitImportMapEntries_SortedAndNamespaced(t *testing.T) {
	def, err := kit.LoadDir("testdata/kits/uikit")
	if err != nil {
		t.Fatalf("kit.LoadDir: %v", err)
	}
	reg := kit.NewRegistry()
	if err := reg.Add(def); err != nil {
		t.Fatalf("Add: %v", err)
	}
	entries := kitImportMapEntries(reg)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].ModuleID != "@kitsoki-test/uikit/ui/panel" {
		t.Errorf("ModuleID = %q, want @kitsoki-test/uikit/ui/panel", entries[0].ModuleID)
	}
	if entries[0].URL != "/kit/kitsoki-test/uikit/ui/panel.mjs" {
		t.Errorf("URL = %q, want /kit/kitsoki-test/uikit/ui/panel.mjs", entries[0].URL)
	}
}
