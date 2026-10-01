// Counts the adapter's source by provenance — lines a generator owns versus
// lines a person wrote — and prints the breakdown as a Markdown table for the
// pull request description. It is a reviewer's number, not a user's: nothing
// in the UI consumes it and nothing is committed. A file is generated when its
// header carries the Go convention ("Code generated ... DO NOT EDIT") or
// openapi-typescript's banner.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, extname } from "node:path";

const serviceDir = join(process.cwd(), "..");
const skip = new Set(["node_modules", "dist", ".git"]);
const codeExtensions = new Set([".go", ".ts", ".tsx", ".mjs", ".sql", ".proto"]);

const generatorOf = (head) => {
  if (/openapi-typescript/i.test(head)) return "openapi-typescript";
  if (!/Code generated .*DO NOT EDIT/s.test(head)) return null;
  if (/oapi-codegen/.test(head)) return "oapi-codegen";
  if (/sqlc/.test(head)) return "sqlc";
  if (/MockGen|mockgen/.test(head)) return "mockgen";
  if (/goverter/.test(head)) return "goverter";
  if (/protoc-gen-go|protoc-gen-liquid|liquidproto/i.test(head)) return "protoc (liquidproto)";
  return "generator";
};

const summary = { generated: 0, handwritten: 0, tests: 0, generatedFiles: 0, handwrittenFiles: 0, byGenerator: {} };
const walk = (dir) => {
  for (const entry of readdirSync(dir)) {
    if (skip.has(entry)) continue;
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) { walk(path); continue; }
    if (!codeExtensions.has(extname(path))) continue;
    const source = readFileSync(path, "utf8");
    const lines = source.split("\n").filter((line) => line.trim() !== "").length;
    const generator = generatorOf(source.slice(0, 600));
    if (generator) {
      summary.generated += lines; summary.generatedFiles += 1;
      summary.byGenerator[generator] = (summary.byGenerator[generator] ?? 0) + lines;
    } else if (/(_test\.go|\.test\.tsx?)$/.test(path)) {
      summary.tests += lines; summary.handwrittenFiles += 1;
    } else {
      summary.handwritten += lines; summary.handwrittenFiles += 1;
    }
  }
};
walk(serviceDir);
const total = summary.generated + summary.handwritten + summary.tests;
const pct = (n) => `${Math.round((n / total) * 100)}%`;
const rows = Object.entries(summary.byGenerator).sort(([, a], [, b]) => b - a)
  .map(([name, lines]) => `| ${name} | ${lines.toLocaleString()} | ${pct(lines)} |`);
console.log(`| Source | Lines | Share |\n|---|---:|---:|\n${rows.join("\n")}\n| **generated, total** | **${summary.generated.toLocaleString()}** | **${pct(summary.generated)}** |\n| handwritten | ${summary.handwritten.toLocaleString()} | ${pct(summary.handwritten)} |\n| tests | ${summary.tests.toLocaleString()} | ${pct(summary.tests)} |\n\n${summary.generatedFiles} generated files, ${summary.handwrittenFiles} handwritten (\`npm run provenance\` in ui/).`);
