const { spawn } = require("node:child_process");

const DEFAULT_BROWSER_TIMEOUT_MS = 3000;
const DEFAULT_BROWSER_POLL_INTERVAL_MS = 50;

function terminalsInteractive(streams = process) {
  return Boolean(streams.stdout && streams.stdout.isTTY && streams.stderr && streams.stderr.isTTY);
}

function browserCommandFor(platform, url) {
  switch (platform) {
    case "darwin":
      return ["open", [url]];
    case "linux":
    case "freebsd":
    case "openbsd":
    case "netbsd":
      return ["xdg-open", [url]];
    case "win32":
      return ["rundll32", ["url.dll,FileProtocolHandler", url]];
    default:
      throw new Error(`unsupported platform ${platform}`);
  }
}

async function waitForHealthy(url, options = {}) {
  const timeoutMs = options.timeoutMs || DEFAULT_BROWSER_TIMEOUT_MS;
  const pollIntervalMs = options.pollIntervalMs || DEFAULT_BROWSER_POLL_INTERVAL_MS;
  const fetchImpl = options.fetchImpl || fetch;
  const deadline = Date.now() + timeoutMs;
  let lastError = null;

  while (Date.now() < deadline) {
    try {
      const response = await fetchImpl(url);
      if (response.ok) {
        await response.arrayBuffer();
        return;
      }
      lastError = new Error(`health returned ${response.status}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, pollIntervalMs));
  }

  if (lastError) {
    throw lastError;
  }
  throw new Error(`timed out waiting for ${url}`);
}

async function openDashboardWhenReady(baseURL, options = {}) {
  if (!baseURL || !terminalsInteractive(options.streams || process)) {
    return;
  }

  const normalizedBaseURL = String(baseURL).replace(/\/+$/, "");
  await waitForHealthy(`${normalizedBaseURL}/health`, options);
  const [command, args] = browserCommandFor(options.platform || process.platform, normalizedBaseURL);
  const child = spawn(command, args, {
    detached: true,
    stdio: "ignore",
  });
  child.unref();
}

module.exports = {
  browserCommandFor,
  openDashboardWhenReady,
  terminalsInteractive,
  waitForHealthy,
};
