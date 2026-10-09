#!/usr/bin/env node
// Cassette stand-in for the real `kitsoki agent ask` binary. Reads the
// harness bridge's prompt from stdin and returns a fixed, recorded-looking
// reply: JSON when the prompt asked for a schema-forced structured
// response, plain text otherwise. Used only by
// test/kitsoki-llm-client.test.mjs — never a live LLM call.
let prompt = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  prompt += chunk;
});
process.stdin.on("end", () => {
  if (prompt.includes("Return ONLY JSON matching this schema")) {
    process.stdout.write(
      JSON.stringify({
        actions: [{ selector: '[data-testid="save-btn"]', description: "Save button", method: "click", arguments: [] }]
      })
    );
  } else {
    process.stdout.write("This is a canned plain-text reply from the stub agent.");
  }
  process.exit(0);
});
