import { readFile } from "node:fs/promises";
import { join } from "node:path";

const checks = [
  { file: "index.html", text: "Local-first orchestration for coding agents", mode: "include" },
  { file: "index.html", text: "Documentation stays one click from the product story.", mode: "include" },
  { file: "index.html", text: "/images/screens/architecture-runtime.svg", mode: "include" },
  { file: "index.html", text: "class=\"mermaid\"", mode: "omit" },
  { file: "docs/index.html", text: "Documentation", mode: "include" },
  { file: "docs/install/index.html", text: "Install Maestro", mode: "include" },
  { file: "docs/architecture/index.html", text: "The shortest operational view of the system:", mode: "include" },
  { file: "docs/architecture/index.html", text: "private MCP daemon", mode: "include" },
  { file: "docs/architecture/index.html", text: "class=\"mermaid\"", mode: "include" },
  { file: "docs/operations/index.html", text: "Operations and observability", mode: "include" },
  { file: "docs/advanced/e2e-harness/index.html", text: "Real Codex E2E harness", mode: "include" },
];

for (const check of checks) {
  const target = join(process.cwd(), "dist", check.file);
  const html = await readFile(target, "utf8");
  const hasText = html.includes(check.text);
  if (check.mode === "include" && !hasText) {
    throw new Error(`expected ${check.file} to include ${JSON.stringify(check.text)}`);
  }
  if (check.mode === "omit" && hasText) {
    throw new Error(`expected ${check.file} to omit ${JSON.stringify(check.text)}`);
  }
}

console.log("website smoke checks passed");
