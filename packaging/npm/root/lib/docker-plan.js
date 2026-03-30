const fs = require("node:fs");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const DEFAULT_HTTP_PORT = "8787";
const HOST_GATEWAY_NAME = "host.docker.internal";
const POSIX_PATH_FLAGS = new Map([
  ["--db", { usage: "file-parent" }],
  ["--workflow", { usage: "file-parent" }],
  ["--extensions", { usage: "file-parent" }],
  ["--logs-root", { usage: "dir" }],
  ["--repo", { usage: "dir" }],
  ["--attach", { usage: "file-parent" }],
]);

const ROOT_FLAGS_WITH_VALUE = new Set(["--db", "--api-url", "--log-level"]);
const ROOT_BOOL_FLAGS = new Set(["--json", "--wide", "--quiet"]);

function normalizeHostPath(rawPath, options = {}) {
  const cwd = options.cwd || process.cwd();
  const env = options.env || process.env;
  const homeDir = options.homeDir || os.homedir();
  const platform = options.platform || process.platform;
  let value = String(rawPath || "").trim();
  if (!value) {
    return value;
  }

  if (value.startsWith("$")) {
    const match = value.match(/^\$([A-Za-z_][A-Za-z0-9_]*)(.*)$/);
    if (match && env[match[1]]) {
      value = `${env[match[1]]}${match[2]}`;
    }
  }

  if (value === "~") {
    value = homeDir;
  } else if (value.startsWith("~/") || value.startsWith("~\\")) {
    value = path.join(homeDir, value.slice(2));
  }

  if (platform === "win32") {
    if (path.win32.isAbsolute(value)) {
      return path.win32.normalize(value);
    }
    return path.win32.resolve(cwd, value);
  }

  if (path.posix.isAbsolute(value)) {
    return path.posix.normalize(value);
  }
  return path.posix.resolve(cwd, value);
}

function isWithinPath(parentPath, candidatePath, platform = process.platform) {
  const pathModule = platform === "win32" ? path.win32 : path.posix;
  const relative = pathModule.relative(parentPath, candidatePath);
  return relative === "" || (!relative.startsWith("..") && !pathModule.isAbsolute(relative));
}

function containerHomeDir(options = {}) {
  const platform = options.platform || process.platform;
  const homeDir = options.homeDir || os.homedir();
  if (platform !== "win32") {
    return homeDir;
  }
  return "/maestro-host/home";
}

function toContainerPath(hostPath, options = {}) {
  const platform = options.platform || process.platform;
  const homeDir = options.homeDir || os.homedir();

  if (platform !== "win32") {
    return hostPath;
  }

  const normalized = path.win32.resolve(hostPath);
  const normalizedHome = path.win32.resolve(homeDir);
  if (isWithinPath(normalizedHome, normalized, platform)) {
    const relative = path.win32.relative(normalizedHome, normalized);
    const segments = relative === "" ? [] : relative.split(path.win32.sep);
    return path.posix.join("/maestro-host/home", ...segments);
  }

  const drive = normalized.slice(0, 1).toLowerCase();
  const remainder = normalized.slice(2).replace(/^\\+/, "");
  const segments = remainder === "" ? [] : remainder.split(path.win32.sep);
  return path.posix.join("/maestro-host", drive, ...segments);
}

function createMountCollector(options = {}) {
  const platform = options.platform || process.platform;
  const homeDir = options.homeDir || os.homedir();
  const fsModule = options.fs || fs;
  const mounts = new Map();

  function add(sourcePath, mountOptions = {}) {
    let resolvedSource = sourcePath;
    let resolvedTarget = toContainerPath(sourcePath, { platform, homeDir });
    if (mountOptions.usage === "file-parent") {
      resolvedSource = path.dirname(sourcePath);
      resolvedTarget = path.posix.dirname(resolvedTarget);
      fsModule.mkdirSync(resolvedSource, { recursive: true });
    } else if (mountOptions.usage === "dir") {
      fsModule.mkdirSync(resolvedSource, { recursive: true });
    } else if (mountOptions.usage === "exact-file") {
      if (!fsModule.existsSync(resolvedSource)) {
        return;
      }
    }

    const key = resolvedTarget;
    const next = {
      source: resolvedSource,
      target: resolvedTarget,
      readOnly: Boolean(mountOptions.readOnly),
    };
    if (mounts.has(key)) {
      const existing = mounts.get(key);
      existing.readOnly = existing.readOnly && next.readOnly;
      mounts.set(key, existing);
      return;
    }
    mounts.set(key, next);
  }

  return {
    add,
    list() {
      return Array.from(mounts.values()).sort((left, right) => left.target.localeCompare(right.target));
    },
  };
}

function rewriteLocalAPIURL(rawValue) {
  const value = String(rawValue || "").trim();
  if (!value) {
    return { value, usesHostGateway: false };
  }
  const hadScheme = /^[a-z]+:\/\//i.test(value);
  const prefixed = hadScheme ? value : `http://${value}`;
  let parsed;
  try {
    parsed = new URL(prefixed);
  } catch {
    return { value, usesHostGateway: false };
  }
  if (!["127.0.0.1", "localhost", "::1", "[::1]"].includes(parsed.hostname)) {
    return { value, usesHostGateway: false };
  }
  parsed.hostname = HOST_GATEWAY_NAME;
  return {
    value: hadScheme ? parsed.toString() : `${parsed.host}${parsed.pathname}${parsed.search}${parsed.hash}`,
    usesHostGateway: true,
  };
}

function parsePublishedPort(rawValue) {
  const value = String(rawValue || "").trim();
  if (!value) {
    return null;
  }
  if (/^\d+$/.test(value)) {
    return {
      hostBinding: `127.0.0.1:${value}:${value}`,
      containerPortFlag: `0.0.0.0:${value}`,
      hostBaseURL: `http://127.0.0.1:${value}`,
    };
  }

  const hostPortMatch = value.match(/^(?<host>[^:]+|\[[^\]]+\]):(?<port>\d+)$/);
  if (!hostPortMatch) {
    return null;
  }

  const host = hostPortMatch.groups.host.replace(/^\[(.*)\]$/, "$1");
  const port = hostPortMatch.groups.port;
  const bindHost = host === "0.0.0.0" || host === "::" ? "127.0.0.1" : host;
  return {
    hostBinding: `${host}:${port}:${port}`,
    containerPortFlag: `0.0.0.0:${port}`,
    hostBaseURL: `http://${bindHost}:${port}`,
  };
}

function flagsWithInlineValues(flagSet, token) {
  for (const flag of flagSet) {
    if (token === flag || token.startsWith(`${flag}=`)) {
      return flag;
    }
  }
  return null;
}

function stripRootFlags(argv) {
  const entries = [];
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i];
    if (token === "--") {
      entries.push({ index: i, token });
      for (let j = i + 1; j < argv.length; j += 1) {
        entries.push({ index: j, token: argv[j] });
      }
      break;
    }
    const valueFlag = flagsWithInlineValues(ROOT_FLAGS_WITH_VALUE, token);
    if (valueFlag) {
      if (token === valueFlag) {
        i += 1;
      }
      continue;
    }
    if (ROOT_BOOL_FLAGS.has(token)) {
      continue;
    }
    entries.push({ index: i, token });
  }
  return entries;
}

function parseCommandPath(argv) {
  const entries = stripRootFlags(argv).filter((entry) => entry.token !== "--");
  if (entries.length === 0) {
    return [];
  }

  const first = entries[0]?.token;
  const second = entries[1]?.token;
  const third = entries[2]?.token;

  if (first === "workflow" && second && !second.startsWith("-")) {
    return ["workflow", second];
  }
  if (first === "issue" && second && !second.startsWith("-")) {
    if (["assets", "images", "comments", "blockers"].includes(second) && third && !third.startsWith("-")) {
      return ["issue", second, third];
    }
    return ["issue", second];
  }
  if (first === "project" && second && !second.startsWith("-")) {
    return ["project", second];
  }
  return [first];
}

function collectCommandPositionals(argv, commandPath) {
  const entries = stripRootFlags(argv);
  const localFlagsWithValue = new Set();
  const localBoolFlags = new Set();

  if (commandPath[0] === "run") {
    for (const flag of ["--workflow", "--extensions", "--logs-root", "--port", "--log-max-bytes", "--log-max-files"]) {
      localFlagsWithValue.add(flag);
    }
    localBoolFlags.add("--i-understand-that-this-will-be-running-without-the-usual-guardrails");
  } else if (commandPath[0] === "init" || (commandPath[0] === "workflow" && commandPath[1] === "init")) {
    for (const flag of [
      "--workspace-root",
      "--codex-command",
      "--agent-mode",
      "--dispatch-mode",
      "--max-concurrent-agents",
      "--max-turns",
      "--max-automatic-retries",
      "--approval-policy",
      "--initial-collaboration-mode",
    ]) {
      localFlagsWithValue.add(flag);
    }
    localBoolFlags.add("--force");
    localBoolFlags.add("--defaults");
  } else if (commandPath[0] === "issue" && commandPath[1] === "comments") {
    for (const flag of ["--body", "--parent", "--attach", "--remove-attachment"]) {
      localFlagsWithValue.add(flag);
    }
  }

  const positionals = [];
  for (let i = commandPath.length; i < entries.length; i += 1) {
    const { token } = entries[i];
    if (token === "--") {
      for (let j = i + 1; j < entries.length; j += 1) {
        positionals.push(entries[j]);
      }
      break;
    }
    const valueFlag = flagsWithInlineValues(localFlagsWithValue, token);
    if (valueFlag) {
      if (token === valueFlag) {
        i += 1;
      }
      continue;
    }
    if (localBoolFlags.has(token)) {
      continue;
    }
    if (token.startsWith("-")) {
      continue;
    }
    positionals.push(entries[i]);
  }
  return positionals;
}

async function reservePort() {
  return await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (!address || typeof address === "string") {
        server.close(() => reject(new Error("failed to allocate an ephemeral port")));
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

function createArgRewriter(argv) {
  const replacements = new Map();
  const appended = [];
  return {
    replaceValue(index, value) {
      replacements.set(index, value);
    },
    replaceInline(index, flag, value) {
      replacements.set(index, `${flag}=${value}`);
    },
    append(...tokens) {
      appended.push(...tokens);
    },
    apply() {
      return argv.map((token, index) => (replacements.has(index) ? replacements.get(index) : token)).concat(appended);
    },
  };
}

async function planDockerInvocation(argv, options = {}) {
  const cwd = options.cwd || process.cwd();
  const env = options.env || process.env;
  const fsModule = options.fs || fs;
  const platform = options.platform || process.platform;
  const homeDir = options.homeDir || os.homedir();
  const imageRef = options.imageRef;
  const uid = options.uid;
  const gid = options.gid;

  if (!imageRef) {
    throw new Error("image ref is required");
  }

  const commandPath = parseCommandPath(argv);
  const mountCollector = createMountCollector({ fs: fsModule, platform, homeDir });
  const rewriter = createArgRewriter(argv);
  const containerHome = containerHomeDir({ platform, homeDir });
  const envVars = new Map([
    ["HOME", containerHome],
    ["MAESTRO_DISABLE_BROWSER_OPEN", "1"],
  ]);
  const dockerArgs = ["run", "--rm", "--init"];
  let usesHostGateway = platform === "linux";

  if (platform !== "win32" && Number.isInteger(uid) && Number.isInteger(gid)) {
    dockerArgs.push("--user", `${uid}:${gid}`);
  }

  if (process.stdin.isTTY || !process.stdin.isTTY) {
    dockerArgs.push("-i");
  }
  if (process.stdin.isTTY && process.stdout.isTTY && process.stderr.isTTY && commandPath[0] !== "mcp") {
    dockerArgs.push("-t");
  }

  const hostMaestroDir = path.join(homeDir, ".maestro");
  fsModule.mkdirSync(hostMaestroDir, { recursive: true });
  mountCollector.add(hostMaestroDir, { usage: "dir" });

  const hostDaemonRegistryDir = path.join(hostMaestroDir, "launcher", "daemons");
  fsModule.mkdirSync(hostDaemonRegistryDir, { recursive: true });
  envVars.set("MAESTRO_DAEMON_REGISTRY_DIR", toContainerPath(hostDaemonRegistryDir, { platform, homeDir }));

  const hostCodexDir = path.join(homeDir, ".codex");
  if (fsModule.existsSync(hostCodexDir)) {
    mountCollector.add(hostCodexDir, { usage: "dir" });
  }

  for (const maybeFile of [".gitconfig", ".config/git"]) {
    const absolute = path.join(homeDir, maybeFile);
    if (fsModule.existsSync(absolute)) {
      mountCollector.add(absolute, { usage: fsModule.statSync(absolute).isDirectory() ? "dir" : "exact-file" });
    }
  }
  const sshDir = path.join(homeDir, ".ssh");
  if (fsModule.existsSync(sshDir)) {
    mountCollector.add(sshDir, { usage: "dir" });
  }

  if (typeof env.SSH_AUTH_SOCK === "string" && env.SSH_AUTH_SOCK.trim() !== "") {
    const resolvedSocket = normalizeHostPath(env.SSH_AUTH_SOCK, { cwd, env, homeDir, platform });
    if (fsModule.existsSync(resolvedSocket)) {
      mountCollector.add(path.dirname(resolvedSocket), { usage: "dir" });
      envVars.set("SSH_AUTH_SOCK", toContainerPath(resolvedSocket, { platform, homeDir }));
    }
  }

  mountCollector.add(cwd, { usage: "dir" });
  const containerCwd = toContainerPath(cwd, { platform, homeDir });
  dockerArgs.push("--workdir", containerCwd);

  for (const key of ["OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"]) {
    if (typeof env[key] === "string" && env[key] !== "") {
      envVars.set(key, env[key]);
    }
  }

  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i];
    const pathFlag = flagsWithInlineValues(new Set(POSIX_PATH_FLAGS.keys()), token);
    if (pathFlag) {
      const descriptor = POSIX_PATH_FLAGS.get(pathFlag);
      if (token === pathFlag) {
        const next = argv[i + 1];
        if (typeof next === "string") {
          const resolved = normalizeHostPath(next, { cwd, env, homeDir, platform });
          mountCollector.add(resolved, { usage: descriptor.usage });
          rewriter.replaceValue(i + 1, toContainerPath(resolved, { platform, homeDir }));
          i += 1;
        }
        continue;
      }
      const rawValue = token.slice(`${pathFlag}=`.length);
      const resolved = normalizeHostPath(rawValue, { cwd, env, homeDir, platform });
      mountCollector.add(resolved, { usage: descriptor.usage });
      rewriter.replaceInline(i, pathFlag, toContainerPath(resolved, { platform, homeDir }));
      continue;
    }

    const apiFlag = flagsWithInlineValues(new Set(["--api-url"]), token);
    if (apiFlag) {
      if (token === apiFlag) {
        const next = argv[i + 1];
        if (typeof next === "string") {
          const rewritten = rewriteLocalAPIURL(next);
          if (rewritten.usesHostGateway) {
            usesHostGateway = true;
          }
          rewriter.replaceValue(i + 1, rewritten.value);
          i += 1;
        }
        continue;
      }
      const rewritten = rewriteLocalAPIURL(token.slice("--api-url=".length));
      if (rewritten.usesHostGateway) {
        usesHostGateway = true;
      }
      rewriter.replaceInline(i, "--api-url", rewritten.value);
    }
  }

  const positionals = collectCommandPositionals(argv, commandPath);
  if (commandPath[0] === "run" && positionals[0]) {
    const resolvedRepoPath = normalizeHostPath(positionals[0].token, { cwd, env, homeDir, platform });
    mountCollector.add(resolvedRepoPath, { usage: fsModule.existsSync(resolvedRepoPath) ? "dir" : "file-parent" });
    rewriter.replaceValue(positionals[0].index, toContainerPath(resolvedRepoPath, { platform, homeDir }));
  }
  if ((commandPath[0] === "init" || (commandPath[0] === "workflow" && commandPath[1] === "init")) && positionals[0]) {
    const resolvedRepoPath = normalizeHostPath(positionals[0].token, { cwd, env, homeDir, platform });
    const usage = fsModule.existsSync(resolvedRepoPath) && fsModule.statSync(resolvedRepoPath).isDirectory() ? "dir" : "file-parent";
    mountCollector.add(resolvedRepoPath, { usage });
    rewriter.replaceValue(positionals[0].index, toContainerPath(resolvedRepoPath, { platform, homeDir }));
  }
  if (commandPath[0] === "issue" && ["assets", "images"].includes(commandPath[1]) && commandPath[2] === "add" && positionals[1]) {
    const resolvedAssetPath = normalizeHostPath(positionals[1].token, { cwd, env, homeDir, platform });
    mountCollector.add(resolvedAssetPath, { usage: "file-parent" });
    rewriter.replaceValue(positionals[1].index, toContainerPath(resolvedAssetPath, { platform, homeDir }));
  }

  let hostBaseURL = "";
  if (commandPath[0] === "run") {
    let portTokenIndex = -1;
    let portValue = DEFAULT_HTTP_PORT;
    for (let i = 0; i < argv.length; i += 1) {
      const token = argv[i];
      if (token === "--port" && typeof argv[i + 1] === "string") {
        portTokenIndex = i + 1;
        portValue = argv[i + 1];
        break;
      }
      if (token.startsWith("--port=")) {
        portTokenIndex = i;
        portValue = token.slice("--port=".length);
        break;
      }
    }
    const portPlan = parsePublishedPort(portValue);
    if (portPlan) {
      hostBaseURL = portPlan.hostBaseURL;
      dockerArgs.push("-p", portPlan.hostBinding);
      if (portTokenIndex === -1) {
        rewriter.append("--port", portPlan.containerPortFlag);
      } else if (argv[portTokenIndex].startsWith("--port=")) {
        rewriter.replaceInline(portTokenIndex, "--port", portPlan.containerPortFlag);
      } else {
        rewriter.replaceValue(portTokenIndex, portPlan.containerPortFlag);
      }
    }

    const mcpPort = await reservePort();
    dockerArgs.push("-p", `127.0.0.1:${mcpPort}:${mcpPort}`);
    envVars.set("MAESTRO_MCP_LISTEN_ADDR", `0.0.0.0:${mcpPort}`);
    envVars.set("MAESTRO_MCP_ADVERTISED_URL", `http://${HOST_GATEWAY_NAME}:${mcpPort}/mcp`);
  }

  if (usesHostGateway && platform === "linux") {
    dockerArgs.push("--add-host", `${HOST_GATEWAY_NAME}:host-gateway`);
  }

  for (const mount of mountCollector.list()) {
    const suffix = mount.readOnly ? ":ro" : "";
    dockerArgs.push("-v", `${mount.source}:${mount.target}${suffix}`);
  }

  for (const [key, value] of envVars) {
    dockerArgs.push("-e", `${key}=${value}`);
  }

  const rewrittenArgv = rewriter.apply();
  dockerArgs.push(imageRef, ...rewrittenArgv);

  return {
    commandPath,
    dockerArgs,
    hostBaseURL,
    rewrittenArgv,
  };
}

function resolveBinPathFromInstallDir(installDir, platform = process.platform) {
  const suffix = platform === "win32" ? "maestro.cmd" : "maestro";
  return path.join(installDir, "node_modules", ".bin", suffix);
}

function isMainModule(metaURL) {
  return Boolean(process.argv[1]) && pathToFileURL(process.argv[1]).href === metaURL;
}

module.exports = {
  DEFAULT_HTTP_PORT,
  HOST_GATEWAY_NAME,
  collectCommandPositionals,
  containerHomeDir,
  isMainModule,
  normalizeHostPath,
  parseCommandPath,
  parsePublishedPort,
  planDockerInvocation,
  resolveBinPathFromInstallDir,
  rewriteLocalAPIURL,
  toContainerPath,
};
