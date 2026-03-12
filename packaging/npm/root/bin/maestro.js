#!/usr/bin/env node

const { spawnSync } = require("node:child_process");
const { getExePath } = require("../lib/get-exe-path");

let exePath;
try {
  exePath = getExePath();
} catch (error) {
  const message = error instanceof Error ? error.message : String(error);
  process.stderr.write(`${message}\n`);
  process.exit(1);
}

const result = spawnSync(exePath, process.argv.slice(2), { stdio: "inherit" });
if (result.error) {
  process.stderr.write(`${result.error.message}\n`);
  process.exit(1);
}
if (typeof result.status === "number") {
  process.exit(result.status);
}
if (result.signal) {
  process.stderr.write(`maestro terminated with signal ${result.signal}\n`);
}
process.exit(1);
