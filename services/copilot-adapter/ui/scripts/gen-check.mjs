// Both clients are projections of their owning service contracts.
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, copyFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const contracts = [
  ["../openapi.yaml", "schema.d.ts"],
  ["../../../csf/tools/codegen/generated/openapi/adapter.openapi.json", "csf-schema.d.ts"],
];
const scratch = mkdtempSync(join(tmpdir(), "workbench-gen-"));
try {
  for (const [source, name] of contracts) {
    const output = `src/api/${name}`;
    const regenerated = join(scratch, name);
    execFileSync("node", ["node_modules/openapi-typescript/bin/cli.js", source, "-o", regenerated], { stdio: "inherit" });
    if (process.argv.includes("--write")) copyFileSync(regenerated, output);
    else if (readFileSync(regenerated, "utf8") !== readFileSync(output, "utf8")) {
      throw new Error(`drift: ${output} is stale; run npm run gen`);
    }
  }
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
