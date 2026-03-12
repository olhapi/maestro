import { readFile } from "node:fs/promises";
import { join } from "node:path";

const checks = [
  { file: "index.html", text: "Local-first orchestration for Codex and ChatGPT" },
  { file: "docs/index.html", text: "Documentation" },
  { file: "docs/install/index.html", text: "Install Maestro" },
  { file: "docs/operations/index.html", text: "Operations and observability" },
  { file: "docs/advanced/e2e-harness/index.html", text: "Real Codex E2E harness" },
];

for (const check of checks) {
  const target = join(process.cwd(), "dist", check.file);
  const html = await readFile(target, "utf8");
  if (!html.includes(check.text)) {
    throw new Error(`expected ${check.file} to include ${JSON.stringify(check.text)}`);
  }
}

console.log("website smoke checks passed");
