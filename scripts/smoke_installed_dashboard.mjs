#!/usr/bin/env node

import { spawn } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { pathToFileURL } from "node:url";

if (isMainModule()) {
  await main(process.argv[2]);
}

export { collectDashboardAssets, extractViteMappedAssets };

async function main(installDir) {
  if (!installDir) {
    console.error("usage: smoke_installed_dashboard.mjs <install_dir>");
    process.exit(1);
  }

  const resolvedInstallDir = path.resolve(installDir);
  const exePath = process.env.MAESTRO_SMOKE_EXE
    ? path.resolve(process.env.MAESTRO_SMOKE_EXE)
    : path.join(
        resolvedInstallDir,
        "node_modules",
        ".bin",
        process.platform === "win32" ? "maestro.cmd" : "maestro",
      );
  const logPath = path.join(resolvedInstallDir, "maestro-run-smoke.log");
  const dbPath = path.join(resolvedInstallDir, "maestro-run-smoke.db");
  const daemonRegistryDir = path.join(resolvedInstallDir, ".maestro-daemons");

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
      env: {
        ...process.env,
        MAESTRO_DAEMON_REGISTRY_DIR: daemonRegistryDir,
        MAESTRO_IMAGE: process.env.MAESTRO_IMAGE || "",
      },
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

    const assets = collectDashboardAssets(baseURL, html);
    if (assets.entryScripts.length === 0) {
      throw new Error("dashboard root did not reference a frontend script bundle");
    }

    const fetchedAssets = new Map();
    for (const entryURL of assets.entryScripts) {
      const entryAsset = await validateServedAsset(entryURL, assets.byURL.get(entryURL));
      fetchedAssets.set(entryURL, entryAsset);
      for (const depPath of extractViteMappedAssets(entryAsset.body)) {
        recordAsset(assets.byURL, depPath, baseURL, "vite-dependency");
      }
    }

    const failures = [];
    for (const [assetURL, metadata] of assets.byURL.entries()) {
      try {
        if (!fetchedAssets.has(assetURL)) {
          fetchedAssets.set(assetURL, await validateServedAsset(assetURL, metadata));
        }
      } catch (error) {
        failures.push(formatError(error));
      }
    }
    if (failures.length > 0) {
      throw new Error(failures.join("\n"));
    }
  } catch (error) {
    const details = buildFailureDetails(error, logPath);
    console.error(details);
    process.exitCode = 1;
  } finally {
    await terminateProcess(child.pid);
  }
}

function isMainModule() {
  return Boolean(process.argv[1]) && pathToFileURL(process.argv[1]).href === import.meta.url;
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

function collectDashboardAssets(baseURL, html) {
  const assets = new Map();

  for (const tag of extractTags(html, "script")) {
    if ((extractAttribute(tag, "type") ?? "").toLowerCase() !== "module") {
      continue;
    }
    const src = extractAttribute(tag, "src");
    if (src) {
      recordAsset(assets, src, baseURL, "entry-script");
    }
  }

  for (const tag of extractTags(html, "link")) {
    const rel = (extractAttribute(tag, "rel") ?? "").toLowerCase();
    const href = extractAttribute(tag, "href");
    if (!href) {
      continue;
    }
    if (rel === "modulepreload") {
      recordAsset(assets, href, baseURL, "modulepreload");
    } else if (rel === "stylesheet") {
      recordAsset(assets, href, baseURL, "stylesheet");
    }
  }

  return {
    byURL: assets,
    entryScripts: Array.from(assets.entries())
      .filter(([, metadata]) => metadata.sources.has("entry-script"))
      .map(([assetURL]) => assetURL),
  };
}

function extractTags(html, tagName) {
  return Array.from(html.matchAll(new RegExp(`<${tagName}\\b[^>]*>`, "gi")), (match) => match[0]);
}

function extractAttribute(tag, name) {
  const match = tag.match(new RegExp(`\\b${name}=(["'])(.*?)\\1`, "i"));
  return match?.[2] ?? null;
}

function recordAsset(assets, rawURL, baseURL, source) {
  const resolvedURL = new URL(rawURL, baseURL).toString();
  const entry = assets.get(resolvedURL) ?? { sourceOrder: [], sources: new Set() };
  if (!entry.sources.has(source)) {
    entry.sources.add(source);
    entry.sourceOrder.push(source);
  }
  assets.set(resolvedURL, entry);
}

function extractViteMappedAssets(entryBody) {
  if (!entryBody.includes("__vite__mapDeps")) {
    return [];
  }
  const depMatch = entryBody.match(/m\.f=(\[[^\]]*\])/);
  if (!depMatch) {
    throw new Error("entry bundle did not expose a parsable Vite dependency list");
  }

  let parsed;
  try {
    parsed = JSON.parse(depMatch[1]);
  } catch (error) {
    throw new Error(`failed to parse Vite dependency list: ${formatError(error)}`);
  }
  if (!Array.isArray(parsed)) {
    throw new Error("Vite dependency list was not an array");
  }
  return parsed.filter(
    (asset) => typeof asset === "string" && /\.(css|js)(?:$|[?#])/.test(asset),
  );
}

async function validateServedAsset(assetURL, metadata) {
  const response = await fetch(assetURL);
  if (!response.ok) {
    throw new Error(describeAssetFailure(assetURL, metadata, `returned ${response.status}`));
  }

  const contentType = response.headers.get("content-type") ?? "";
  const body = await response.text();
  if (body.trim() === "") {
    throw new Error(describeAssetFailure(assetURL, metadata, "body was empty"));
  }
  if (looksLikeHTML(body)) {
    throw new Error(
      describeAssetFailure(assetURL, metadata, `returned HTML: ${previewBody(body)}`),
    );
  }

  const expectedPattern = expectedContentType(assetURL);
  if (expectedPattern && !expectedPattern.test(contentType)) {
    throw new Error(
      describeAssetFailure(
        assetURL,
        metadata,
        `returned unexpected content type ${contentType}`,
      ),
    );
  }

  return { body, contentType };
}

function describeAssetFailure(assetURL, metadata, message) {
  const label = new URL(assetURL).pathname;
  const sources = metadata?.sourceOrder?.join(", ") ?? "unknown";
  return `asset ${label} (${sources}) ${message}`;
}

function expectedContentType(assetURL) {
  const pathname = new URL(assetURL).pathname;
  if (pathname.endsWith(".js")) {
    return /javascript|ecmascript/i;
  }
  if (pathname.endsWith(".css")) {
    return /text\/css/i;
  }
  return null;
}

function looksLikeHTML(body) {
  return /<!doctype html|<html/i.test(body) || body.includes(`<div id="root"></div>`);
}

function previewBody(body) {
  return body.replace(/\s+/g, " ").trim().slice(0, 120);
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
