// kits.go — the kit extension-surface JSON-RPC methods (S3b,
// .context/kits-implementation-plan.md design decision D2.2): a generic
// `kit.<kit>.<iface>.<op>` fallback that resolves against the server's
// installed-kit dispatcher (internal/kitendpoint), plus `runstatus.kits.list`
// so the SPA (and a future S3c UI loader) can discover what's installed
// without guessing method names.
//
// Like dispatchEditor, this family does not resolve a session from the
// provider — a kit's declared host_interfaces are a property of the
// INSTALLED KIT, not of any particular running session.
package server

import (
	"context"

	"kitsoki/internal/kit"
	"kitsoki/internal/kitendpoint"
)

// KitHeader is the wire shape `runstatus.kits.list` returns per installed
// kit — just enough for a story browser / kit-UI loader to know what's
// available (the SPA's runtime kit-module loader, S5, consumes
// Namespace+Kit+UI to build the import map and mount routes; the rest is
// display metadata).
type KitHeader struct {
	Kit       string        `json:"kit"`
	Namespace string        `json:"namespace"`
	Version   string        `json:"version"`
	Title     string        `json:"title,omitempty"`
	Provides  []string      `json:"provides_stories,omitempty"`
	UI        []KitUIHeader `json:"ui,omitempty"`
}

// KitUIHeader is the wire projection of one kit.UIEntry.
type KitUIHeader struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Entry string `json:"entry"`
	Nav   bool   `json:"nav,omitempty"`
}

// listInstalledKits renders every kit the server's dispatcher knows about as
// []KitHeader, sorted by kit name (Dispatcher.Kits().List() is already
// sorted). Returns an empty (never nil) slice when no dispatcher was
// attached (WithKits not passed) — most instances have no kits installed,
// and the SPA should render "no kits" rather than treat this as an error.
func (s *Server) listInstalledKits() []KitHeader {
	out := []KitHeader{}
	if s.kits == nil {
		return out
	}
	for _, def := range s.kits.Kits().List() {
		out = append(out, KitHeader{
			Kit:       def.Kit,
			Namespace: def.Namespace,
			Version:   def.Version,
			Title:     def.Title,
			Provides:  def.Provides.Stories,
			UI:        kitUIHeaders(def.Provides.UI),
		})
	}
	return out
}

func kitUIHeaders(entries []kit.UIEntry) []KitUIHeader {
	out := make([]KitUIHeader, 0, len(entries))
	for _, e := range entries {
		out = append(out, KitUIHeader{ID: e.ID, Title: e.Title, Entry: e.Entry, Nav: e.Nav})
	}
	return out
}

// dispatchKit handles the generic `kit.<kit>.<iface>.<op>` fallback (S3b).
// It returns handled=false for any method that doesn't parse as a
// four-segment kit.* name, so dispatch's default case can report
// codeMethodMissing for genuinely unknown methods — this family must run
// LAST among the no-session fallbacks precisely so it doesn't shadow a
// mistyped runstatus.* method with a confusing "unknown kit" error.
func (s *Server) dispatchKit(ctx context.Context, method string, params map[string]any) (any, *rpcError, bool) {
	kitName, iface, op, ok := kitendpoint.ParseMethod(method)
	if !ok {
		return nil, nil, false
	}
	if s.kits == nil {
		return nil, &rpcError{Code: codeMethodMissing, Message: "no kits installed on this server: " + method}, true
	}

	// params IS the op's args — the method name already carries kit/iface/op,
	// so (unlike kit_call's MCP shape, which nests args under an "args" key
	// alongside the kit/iface/op fields) there is nothing else to unwrap here.
	result, err := s.kits.Call(ctx, kitName, iface, op, params)
	if err != nil {
		return nil, &rpcError{Code: codeServerError, Message: err.Error()}, true
	}
	out := map[string]any{"ok": true}
	if result.Error != "" {
		out["ok"] = false
		out["error"] = result.Error
	}
	if result.Data != nil {
		out["data"] = result.Data
	}
	return out, nil, true
}
