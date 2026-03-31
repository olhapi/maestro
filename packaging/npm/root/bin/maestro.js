#!/usr/bin/env node

const nodeMajor = Number.parseInt(process.versions.node.split(".")[0], 10);
if (!Number.isInteger(nodeMajor) || nodeMajor < 24) {
  process.stderr.write(`Maestro's npm launcher requires Node 24 or newer; found ${process.versions.node}\n`);
  process.exit(1);
}

const { main } = require("../lib/cli");

main(process.argv.slice(2)).catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  process.stderr.write(`${message}\n`);
  process.exit(1);
});
