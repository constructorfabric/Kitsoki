#!/usr/bin/env node
// Hermetic slidey stand-in that simulates a slidey build that hasn't landed
// --estimate --json yet (errors on the unknown flag), for exercising
// demo-doctor's "slidey --estimate --json unavailable" FAIL path.
import process from "node:process";

const args = process.argv.slice(2);
if (args.includes("--json")) {
  process.stderr.write("error: unknown option '--json'\n");
  process.exit(1);
}
process.stdout.write("estimate: (human readable fallback, no --json support)\n");
process.exit(0);
