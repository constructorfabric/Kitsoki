/**
 * Feature-catalog schema guard: every features/*.yaml must parse, satisfy the
 * zod schema (including the cross-field tour-step rules), and pass the
 * cross-file catalog checks. This is the in-editor-loop twin of
 * `make features-check` (which additionally byte-compares the committed
 * generated manifests).
 */
import { describe, it, expect } from "vitest";
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";
import { parse } from "yaml";
import { FeatureSchema, validateCatalog, type Feature } from "../../scripts/features/schema.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
// tests/unit → tests → runstatus → tools → repo root
const repoRoot = path.resolve(__dirname, "../../../..");
const featuresDir = path.join(repoRoot, "features");

const files = fs.readdirSync(featuresDir).filter((f) => /\.ya?ml$/.test(f));

describe("feature catalog", () => {
  it("has at least one feature file", () => {
    expect(files.length).toBeGreaterThan(0);
  });

  const loaded: Array<{ file: string; feature: Feature }> = [];

  for (const name of files) {
    it(`features/${name} satisfies the schema`, () => {
      const doc = parse(fs.readFileSync(path.join(featuresDir, name), "utf8"));
      const res = FeatureSchema.safeParse(doc);
      if (!res.success) {
        const detail = res.error.issues
          .map((i) => `${i.path.join(".") || "(root)"}: ${i.message}`)
          .join("\n");
        expect.fail(`schema violations:\n${detail}`);
      }
      loaded.push({ file: path.join("features", name), feature: res.data });
    });
  }

  it("passes the cross-file catalog checks", () => {
    expect(validateCatalog(loaded, repoRoot)).toEqual([]);
  });
});
