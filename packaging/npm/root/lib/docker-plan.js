const fs = require("node:fs");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");
const { DatabaseSync } = require("node:sqlite");
const { pathToFileURL } = require("node:url");

const DEFAULT_HTTP_PORT = "8787";
const DEFAULT_DATABASE_PATH = "~/.maestro/maestro.db";
const DEFAULT_WORKSPACE_ROOT = "~/.maestro/worktrees";
const HOST_GATEWAY_NAME = "host.docker.internal";
const UNRESOLVED_ENV_TOKEN_PATTERN = /\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))/;
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

function pathModuleForPlatform(platform = process.platform) {
  return platform === "win32" ? path.win32 : path.posix;
}

function expandPathValue(value, options = {}) {
  const env = options.env || process.env;
  const homeDir = options.homeDir || os.homedir();
  const platform = options.platform || process.platform;
  const pathModule = pathModuleForPlatform(platform);
  let result = String(value || "").trim();
  if (!result) {
    return result;
  }

  result = result.replace(/\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))/g, (match, braced, bare) => {
    const name = braced || bare;
    const resolved = env[name];
    if (typeof resolved === "string" && resolved.trim() !== "") {
      return resolved;
    }
    return match;
  });

  if (result === "~") {
    return homeDir;
  }
  if (result.startsWith("~/") || result.startsWith("~\\")) {
    return pathModule.join(homeDir, result.slice(2));
  }

  return result;
}

function resolvePathValue(baseDir, raw, fallback, options = {}) {
  const platform = options.platform || process.platform;
  const pathModule = pathModuleForPlatform(platform);
  let value = String(raw || "").trim();
  if (value === "") {
    value = String(fallback || "").trim();
  }
  if (value === "") {
    return value;
  }
  value = expandPathValue(value, options);
  if (value === "") {
    return value;
  }
  if (value.startsWith("$")) {
    return pathModule.normalize(value);
  }
  if (pathModule.isAbsolute(value)) {
    return pathModule.normalize(value);
  }
  if (!baseDir) {
    baseDir = options.cwd || process.cwd();
  }
  return pathModule.normalize(pathModule.join(baseDir, value));
}

function hasUnresolvedExpandedEnvPath(_rawPath, resolvedPath, _options = {}) {
  return UNRESOLVED_ENV_TOKEN_PATTERN.test(String(resolvedPath || "").trim());
}

function normalizeHostPath(rawPath, options = {}) {
  return resolvePathValue(options.cwd || process.cwd(), rawPath, "", options);
}

function isWithinPath(parentPath, candidatePath, pathModule = path.posix) {
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
  const hostPathModule = pathModuleForPlatform(platform);

  if (platform !== "win32") {
    return hostPath;
  }

  const normalized = hostPathModule.resolve(hostPath);
  const normalizedHome = hostPathModule.resolve(homeDir);
  if (isWithinPath(normalizedHome, normalized, hostPathModule)) {
    const relative = hostPathModule.relative(normalizedHome, normalized);
    const segments = relative === "" ? [] : relative.split(hostPathModule.sep);
    return path.posix.join("/maestro-host/home", ...segments);
  }

  const drive = normalized.slice(0, 1).toLowerCase();
  const remainder = normalized.slice(2).replace(/^\\+/, "");
  const segments = remainder === "" ? [] : remainder.split(hostPathModule.sep);
  return path.posix.join("/maestro-host", drive, ...segments);
}

function createMountCollector(options = {}) {
  const platform = options.platform || process.platform;
  const homeDir = options.homeDir || os.homedir();
  const fsModule = options.fs || fs;
  const hostPathModule = pathModuleForPlatform(platform);
  const mounts = new Map();

  function add(sourcePath, mountOptions = {}) {
    let resolvedSource = sourcePath;
    let resolvedTarget = toContainerPath(sourcePath, { platform, homeDir });
    if (mountOptions.usage === "file-parent") {
      resolvedSource = hostPathModule.dirname(resolvedSource);
      resolvedTarget = path.posix.dirname(resolvedTarget);
      fsModule.mkdirSync(resolvedSource, { recursive: true });
    } else if (mountOptions.usage === "dir") {
      fsModule.mkdirSync(resolvedSource, { recursive: true });
    } else if (mountOptions.usage === "exact-file") {
      if (!fsModule.existsSync(resolvedSource)) {
        return;
      }
    }

    for (const existing of mounts.values()) {
      if (isWithinPath(existing.source, resolvedSource, hostPathModule) && isWithinPath(existing.target, resolvedTarget, path.posix)) {
        return;
      }
    }
    for (const [key, existing] of mounts) {
      if (isWithinPath(resolvedSource, existing.source, hostPathModule) && isWithinPath(resolvedTarget, existing.target, path.posix)) {
        mounts.delete(key);
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

  const rawHost = hostPortMatch.groups.host;
  const host = rawHost.replace(/^\[(.*)\]$/, "$1");
  const port = hostPortMatch.groups.port;
  const publishedHost = rawHost.startsWith("[") ? rawHost : host;
  let bindHost = publishedHost;
  if (host === "0.0.0.0") {
    bindHost = "127.0.0.1";
  } else if (host === "::") {
    bindHost = "[::1]";
  }
  return {
    hostBinding: `${publishedHost}:${port}:${port}`,
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

function findFlagValue(argv, flag) {
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i];
    if (token === flag) {
      return typeof argv[i + 1] === "string" ? argv[i + 1] : "";
    }
    if (token.startsWith(`${flag}=`)) {
      return token.slice(flag.length + 1);
    }
  }
  return "";
}

function parseWorkflowFrontMatter(content) {
  const normalized = String(content || "").replace(/^\uFEFF/, "").replace(/\r\n/g, "\n");
  if (!normalized.startsWith("---\n")) {
    return "";
  }

  const end = normalized.indexOf("\n---\n", 4);
  return end === -1 ? normalized.slice(4) : normalized.slice(4, end);
}

function parseWorkflowScalar(rawValue, workflowPath, keyName) {
  let value = String(rawValue || "").trim();
  if (!value) {
    return null;
  }
  if (value.startsWith("[") || value.startsWith("{") || value === "|" || value === ">") {
    throw new Error(`${keyName} must be a string at ${workflowPath}`);
  }

  if (value.startsWith('"')) {
    if (!value.endsWith('"')) {
      throw new Error(`${keyName} must be a string at ${workflowPath}`);
    }
    value = value.slice(1, -1).replace(/\\(["\\/bfnrt])/g, (match, escape) => {
      switch (escape) {
        case "b":
          return "\b";
        case "f":
          return "\f";
        case "n":
          return "\n";
        case "r":
          return "\r";
        case "t":
          return "\t";
        default:
          return escape;
      }
    });
    return value;
  }

  if (value.startsWith("'")) {
    if (!value.endsWith("'")) {
      throw new Error(`${keyName} must be a string at ${workflowPath}`);
    }
    return value.slice(1, -1).replace(/''/g, "'");
  }

  return value;
}

function nextSignificantLine(lines, startIndex) {
  for (let i = startIndex; i < lines.length; i += 1) {
    const trimmed = lines[i].trim();
    if (!trimmed || trimmed.startsWith("#")) {
      continue;
    }
    return {
      indent: lines[i].match(/^[ \t]*/)?.[0].length || 0,
      index: i,
      trimmed,
    };
  }
  return null;
}

function parseWorkflowWorkspaceRoot(workflowPath, content, options = {}) {
  const pathModule = pathModuleForPlatform(options.platform || process.platform);
  const workflowDir = pathModule.dirname(workflowPath);
  const frontMatter = parseWorkflowFrontMatter(content);
  if (!frontMatter) {
    return resolvePathValue(workflowDir, "", DEFAULT_WORKSPACE_ROOT, options);
  }

  const lines = frontMatter.split("\n");
  let workspaceRoot = null;
  let inWorkspaceBlock = false;
  let workspaceIndent = 0;

  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i];
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) {
      continue;
    }

    const indent = line.match(/^[ \t]*/)?.[0].length || 0;
    if (indent === 0) {
      inWorkspaceBlock = false;

      const workspaceRootMatch = trimmed.match(/^workspace_root\s*:\s*(.*)$/);
      if (workspaceRootMatch) {
        workspaceRoot = parseWorkflowScalar(workspaceRootMatch[1], workflowPath, "workspace.root");
        continue;
      }

      const workspaceMatch = trimmed.match(/^workspace\s*:\s*(.*)$/);
      if (!workspaceMatch) {
        continue;
      }

      const inline = workspaceMatch[1].trim();
      if (!inline) {
        inWorkspaceBlock = true;
        workspaceIndent = indent;
        continue;
      }

      const inlineRootMatch = inline.match(/^\{\s*root\s*:\s*(.*?)\s*\}$/) || inline.match(/^root\s*:\s*(.*)$/);
      if (inlineRootMatch) {
        workspaceRoot = parseWorkflowScalar(inlineRootMatch[1], workflowPath, "workspace.root");
      }
      continue;
    }

    if (!inWorkspaceBlock || indent <= workspaceIndent) {
      continue;
    }

    const rootMatch = trimmed.match(/^root\s*:\s*(.*)$/);
    if (!rootMatch) {
      continue;
    }

    const rawRootValue = rootMatch[1];
    if (!rawRootValue.trim()) {
      const next = nextSignificantLine(lines, i + 1);
      if (next && next.indent > indent) {
        throw new Error(`workspace.root must be a string at ${workflowPath}`);
      }
      workspaceRoot = null;
      continue;
    }

    workspaceRoot = parseWorkflowScalar(rawRootValue, workflowPath, "workspace.root");
  }

  const resolved = resolvePathValue(workflowDir, workspaceRoot, DEFAULT_WORKSPACE_ROOT, options);
  if (workspaceRoot != null && hasUnresolvedExpandedEnvPath(workspaceRoot, resolved, options)) {
    throw new Error(`failed to resolve workspace root: unresolved environment variable in ${JSON.stringify(resolved)}`);
  }

  return resolved;
}

function resolveWorkflowWorkspaceRoot(workflowPath, options = {}) {
  const fsModule = options.fs || fs;
  const pathModule = pathModuleForPlatform(options.platform || process.platform);
  const workflowDir = pathModule.dirname(workflowPath);
  const defaultRoot = resolvePathValue(workflowDir, "", DEFAULT_WORKSPACE_ROOT, options);

  let stat;
  try {
    stat = fsModule.statSync(workflowPath);
  } catch (error) {
    if (error && error.code === "ENOENT") {
      return defaultRoot;
    }
    throw new Error(`failed to inspect workflow file ${workflowPath}: ${error.message}`);
  }

  if (!stat.isFile()) {
    throw new Error(`workflow path is not a file: ${workflowPath}`);
  }

  let content;
  try {
    content = fsModule.readFileSync(workflowPath, "utf8");
  } catch (error) {
    throw new Error(`failed to read workflow file ${workflowPath}: ${error.message}`);
  }

  return parseWorkflowWorkspaceRoot(workflowPath, content, options);
}

function resolveDatabasePath(argv, options = {}) {
  const rawDbPath = findFlagValue(argv, "--db");
  const cwd = options.cwd || process.cwd();
  const resolved = resolvePathValue(cwd, rawDbPath, DEFAULT_DATABASE_PATH, options);
  if (hasUnresolvedExpandedEnvPath(rawDbPath, resolved, options)) {
    throw new Error(`failed to resolve database path: unresolved environment variable in ${JSON.stringify(resolved)}`);
  }
  return resolved;
}

function shouldSkipRunDatabaseDiscovery(error) {
  if (!error || typeof error !== "object") {
    return false;
  }
  if (error.code !== "ERR_SQLITE_ERROR") {
    return false;
  }
  return /no such table: projects/.test(String(error.message || ""));
}

function discoverProjectWorkflowsFromDatabase(dbPath, options = {}) {
  let db;
  try {
    db = new DatabaseSync(dbPath, { readOnly: true });
    const pathModule = pathModuleForPlatform(options.platform || process.platform);
    const tableInfo = db.prepare("PRAGMA table_info(projects)").all();
    const columns = new Set(tableInfo.map((row) => String(row.name || "").trim()).filter(Boolean));
    if (!columns.has("repo_path")) {
      return [];
    }

    const projectWorkflows = [];
    const rows = columns.has("workflow_path")
      ? db.prepare("SELECT repo_path, workflow_path FROM projects").all()
      : db.prepare("SELECT repo_path FROM projects").all();
    for (const row of rows) {
      const repoPath = String(row.repo_path || "").trim();
      if (!repoPath) {
        continue;
      }
      const workflowPath = columns.has("workflow_path")
        ? String(row.workflow_path || "").trim() || pathModule.join(repoPath, "WORKFLOW.md")
        : pathModule.join(repoPath, "WORKFLOW.md");
      projectWorkflows.push({ repoPath, workflowPath });
    }
    return projectWorkflows;
  } catch (error) {
    if (shouldSkipRunDatabaseDiscovery(error)) {
      return [];
    }
    throw new Error(`failed to inspect Maestro database at ${dbPath}: ${error.message}`);
  } finally {
    if (db) {
      db.close();
    }
  }
}

function addWorkflowMounts(workflowPath, mountCollector, options = {}) {
  const fsModule = options.fs || fs;
  const pathModule = pathModuleForPlatform(options.platform || process.platform);
  const workflowDir = pathModule.dirname(workflowPath);
  mountCollector.add(workflowDir, { usage: "dir" });
  const root = resolveWorkflowWorkspaceRoot(workflowPath, {
    cwd: options.cwd,
    env: options.env,
    fs: fsModule,
    homeDir: options.homeDir,
    platform: options.platform,
  });
  mountCollector.add(root, { usage: "dir" });
}

function preflightRunMounts(argv, commandPath, mountCollector, options = {}) {
  if (commandPath[0] !== "run") {
    return;
  }

  const fsModule = options.fs || fs;
  const cwd = options.cwd || process.cwd();
  const env = options.env || process.env;
  const homeDir = options.homeDir || os.homedir();
  const platform = options.platform || process.platform;
  const positionals = collectCommandPositionals(argv, commandPath);
  const scopedRepoPath = positionals[0] ? normalizeHostPath(positionals[0].token, { cwd, env, homeDir, platform }) : "";
  const rawWorkflowPath = findFlagValue(argv, "--workflow").trim();
  const explicitWorkflowPath = rawWorkflowPath ? normalizeHostPath(rawWorkflowPath, { cwd, env, homeDir, platform }) : "";
  const pathModule = pathModuleForPlatform(platform);

  if (scopedRepoPath) {
    const workflowPath = explicitWorkflowPath || pathModule.join(scopedRepoPath, "WORKFLOW.md");
    mountCollector.add(scopedRepoPath, { usage: "dir" });
    addWorkflowMounts(workflowPath, mountCollector, { cwd, env, fs: fsModule, homeDir, platform });
    return;
  }

  if (explicitWorkflowPath) {
    addWorkflowMounts(explicitWorkflowPath, mountCollector, { cwd, env, fs: fsModule, homeDir, platform });
  }

  const dbPath = resolveDatabasePath(argv, { cwd, env, homeDir, platform });
  if (!fsModule.existsSync(dbPath)) {
    return;
  }

  for (const project of discoverProjectWorkflowsFromDatabase(dbPath, { platform })) {
    const repoPath = normalizeHostPath(project.repoPath, { cwd, env, homeDir, platform });
    if (!repoPath) {
      continue;
    }
    mountCollector.add(repoPath, { usage: "dir" });
    addWorkflowMounts(normalizeHostPath(project.workflowPath, { cwd, env, homeDir, platform }), mountCollector, {
      cwd,
      env,
      fs: fsModule,
      homeDir,
      platform,
    });
  }
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
  preflightRunMounts(argv, commandPath, mountCollector, {
    cwd,
    env,
    fs: fsModule,
    homeDir,
    platform,
  });
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
