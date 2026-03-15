#!/usr/bin/env node

import { spawn } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import path from "node:path";
import { createRequire } from "node:module";
import { setTimeout as delay } from "node:timers/promises";

const installDir = process.argv[2];

if (!installDir) {
  console.error("usage: smoke_installed_dashboard.mjs <install_dir>");
  process.exit(1);
}

const resolvedInstallDir = path.resolve(installDir);
const requireFromInstall = createRequire(path.join(resolvedInstallDir, "package.json"));
const { getExePath } = requireFromInstall("@olhapi/maestro/lib/get-exe-path");
const exePath = getExePath();
const logPath = path.join(resolvedInstallDir, "maestro-run-smoke.log");
const dbPath = path.join(resolvedInstallDir, "maestro-run-smoke.db");

const port = await freePort();
const baseURL = `http://127.0.0.1:${port}`;
const logFD = fs.openSync(logPath, "w");

const child = spawn(
  exePath,
  [
    "run",
    "--db",
    dbPath,
    "--port",
    String(port),
    "--i-understand-that-this-will-be-running-without-the-usual-guardrails",
  ],
  {
    cwd: resolvedInstallDir,
    detached: true,
    stdio: ["ignore", logFD, logFD],
  },
);

child.unref();
fs.closeSync(logFD);

try {
  await waitForHealthy(`${baseURL}/health`);

  const htmlResponse = await fetch(`${baseURL}/`);
  if (!htmlResponse.ok) {
    throw new Error(`dashboard root returned ${htmlResponse.status}`);
  }

  const html = await htmlResponse.text();
  if (!/<html/i.test(html) || !/<div id="root"><\/div>/i.test(html)) {
    throw new Error("dashboard root did not return the embedded app shell");
  }

  const scriptMatch = html.match(/<script[^>]+src="([^"]+)"/i);
  if (!scriptMatch) {
    throw new Error("dashboard root did not reference a frontend script bundle");
  }

  const assetURL = new URL(scriptMatch[1], baseURL).toString();
  const assetResponse = await fetch(assetURL);
  if (!assetResponse.ok) {
    throw new Error(`dashboard asset returned ${assetResponse.status}`);
  }

  const contentType = assetResponse.headers.get("content-type") ?? "";
  if (!/javascript|ecmascript/i.test(contentType)) {
    throw new Error(`dashboard asset returned unexpected content type ${contentType}`);
  }

  const assetBody = await assetResponse.text();
  if (assetBody.trim() === "") {
    throw new Error("dashboard asset body was empty");
  }
} catch (error) {
  const details = buildFailureDetails(error, logPath);
  console.error(details);
  process.exitCode = 1;
} finally {
  await terminateProcess(child.pid);
}

async function freePort() {
  return await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (!address || typeof address === "string") {
        server.close(() => reject(new Error("failed to resolve ephemeral port")));
        return;
      }
      server.close((error) => {
        if (error) {
          reject(error);
          return;
        }
        resolve(address.port);
      });
    });
  });
}

async function waitForHealthy(url) {
  const deadline = Date.now() + 15000;
  let lastError = null;

  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) {
        await response.arrayBuffer();
        return;
      }
      lastError = new Error(`health returned ${response.status}`);
    } catch (error) {
      lastError = error;
    }
    await delay(250);
  }

  throw new Error(`timed out waiting for ${url}: ${formatError(lastError)}`);
}

async function terminateProcess(pid) {
  if (!pid) {
    return;
  }
  try {
    process.kill(pid, "SIGTERM");
  } catch (error) {
    if (error && error.code !== "ESRCH") {
      throw error;
    }
  }
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    if (!processExists(pid)) {
      return;
    }
    await delay(100);
  }
  try {
    process.kill(pid, "SIGKILL");
  } catch (error) {
    if (error && error.code !== "ESRCH") {
      throw error;
    }
  }
}

function processExists(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    if (error && error.code === "ESRCH") {
      return false;
    }
    throw error;
  }
}

function buildFailureDetails(error, logPath) {
  const details = [`dashboard smoke failed: ${formatError(error)}`];
  if (fs.existsSync(logPath)) {
    const logs = fs.readFileSync(logPath, "utf8").trim();
    if (logs !== "") {
      details.push("maestro run log:");
      details.push(logs);
    }
  }
  return details.join("\n");
}

function formatError(error) {
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}
