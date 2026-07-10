package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCLI_TicketProviderCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "provider.star")
	if err := os.WriteFile(path, []byte(`
def search(ctx):
    return {"tickets": [{"id": "T-1", "title": ctx.inputs["query"]}]}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".yaml", []byte(`
kind: ticket_provider/v1
http:
  enabled: false
`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := execRoot(t, "ticket-provider", "call", "--script", path, "--op", "search", "--arg", "query=from-cli")
	if err != nil {
		t.Fatalf("ticket-provider call: %v\n%s", err, out)
	}
	var env ticketProviderCallEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out)
	}
	if !env.OK {
		t.Fatalf("expected ok envelope, got %+v", env.Error)
	}
	tickets, ok := env.Data["tickets"].([]any)
	if !ok || len(tickets) != 1 {
		t.Fatalf("tickets = %#v, want one row", env.Data["tickets"])
	}
	row := tickets[0].(map[string]any)
	if got := row["title"]; got != "from-cli" {
		t.Fatalf("ticket title = %v, want from-cli", got)
	}
}
