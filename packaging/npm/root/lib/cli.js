const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawn, spawnSync } = require("node:child_process");

const { browserOpenDisabled, openDashboardWhenReady } = require("./browser");
const { planDockerInvocation } = require("./docker-plan");
const { installBundledSkills } = require("./install-skills");
const {
  DEFAULT_IMAGE_REPOSITORY,
  imageRefForVersion,
  resolveImageRef,
  sanitizeVersion,
  writeRuntimeState,
} = require("./runtime-state");

const MINIMUM_LAUNCHER_NODE_MAJOR = 24;

function packageVersion() {
  const packageJSON = JSON.parse(fs.readFileSync(path.join(__dirname, "..", "package.json"), "utf8"));
  return packageJSON.version;
}

function ensureSupportedNodeVersion(nodeVersion = process.versions.node) {
  const major = Number.parseInt(String(nodeVersion || "").split(".")[0], 10);
  if (!Number.isInteger(major) || major < MINIMUM_LAUNCHER_NODE_MAJOR) {
    throw new Error(`Maestro's npm launcher requires Node ${MINIMUM_LAUNCHER_NODE_MAJOR} or newer; found ${nodeVersion}`);
  }
}

async function main(argv = process.argv.slice(2), options = {}) {
  ensureSupportedNodeVersion();
  const deps = createDeps(options);
  if (argv[0] === "self-update") {
    return handleSelfUpdate(argv.slice(1), deps);
  }
  if (argv[0] === "doctor" && argv[1] === "install") {
    return handleDoctorInstall(argv.slice(2), deps);
  }
  if (argv[0] === "install" && argv.includes("--skills") && !argv.includes("--help") && !argv.includes("-h")) {
    return handleInstallSkills(deps);
  }
  return runContainerized(argv, deps);
}

function createDeps(options) {
  return {
    cwd: options.cwd || process.cwd(),
    env: options.env || process.env,
    fs: options.fs || fs,
    gid: options.gid ?? (typeof process.getgid === "function" ? process.getgid() : undefined),
    baseDir: options.baseDir || path.join(__dirname, ".."),
    homeDir: options.homeDir || os.homedir(),
    packageVersion: options.packageVersion || packageVersion(),
    platform: options.platform || process.platform,
    spawn: options.spawn || spawn,
    spawnSync: options.spawnSync || spawnSync,
    stdout: options.stdout || process.stdout,
    stderr: options.stderr || process.stderr,
    uid: options.uid ?? (typeof process.getuid === "function" ? process.getuid() : undefined),
    exit: options.exit || ((code) => process.exit(code)),
    openDashboardWhenReady: options.openDashboardWhenReady || openDashboardWhenReady,
    planDockerInvocation: options.planDockerInvocation || planDockerInvocation,
  };
}

function dockerBinary(env) {
  if (typeof env.MAESTRO_DOCKER_BIN === "string" && env.MAESTRO_DOCKER_BIN.trim() !== "") {
    return env.MAESTRO_DOCKER_BIN.trim();
  }
  return "docker";
}

function ensureDockerAvailable(deps) {
  const result = deps.spawnSync(dockerBinary(deps.env), ["version", "--format", "{{.Server.Version}}"], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  if (result.error) {
    throw new Error(`failed to start docker: ${result.error.message}`);
  }
  if (result.status !== 0) {
    const stderr = String(result.stderr || "").trim();
    throw new Error(stderr || "docker is required to run the Maestro launcher");
  }
}

function ensureImageAvailable(imageRef, deps) {
  const inspect = deps.spawnSync(dockerBinary(deps.env), ["image", "inspect", imageRef], {
    encoding: "utf8",
    stdio: ["ignore", "ignore", "pipe"],
  });
  if (inspect.error) {
    throw new Error(`failed to inspect docker image ${imageRef}: ${inspect.error.message}`);
  }
  if (inspect.status === 0) {
    return;
  }

  const pull = deps.spawnSync(dockerBinary(deps.env), ["pull", imageRef], {
    encoding: "utf8",
    stdio: "inherit",
  });
  if (pull.error) {
    throw new Error(`failed to pull docker image ${imageRef}: ${pull.error.message}`);
  }
  if (pull.status !== 0) {
    throw new Error(`docker pull ${imageRef} failed`);
  }
}

async function runContainerized(argv, deps) {
  ensureDockerAvailable(deps);
  const imageRef = resolveImageRef({
    env: deps.env,
    homeDir: deps.homeDir,
    packageVersion: deps.packageVersion,
  });
  ensureImageAvailable(imageRef, deps);

  const plan = await deps.planDockerInvocation(argv, {
    cwd: deps.cwd,
    env: deps.env,
    fs: deps.fs,
    gid: deps.gid,
    homeDir: deps.homeDir,
    imageRef,
    platform: deps.platform,
    uid: deps.uid,
  });

  const child = deps.spawn(dockerBinary(deps.env), plan.dockerArgs, {
    cwd: deps.cwd,
    stdio: "inherit",
  });

  let browserPromise = Promise.resolve();
  if (plan.commandPath[0] === "run" && plan.hostBaseURL && !browserOpenDisabled(deps.env)) {
    browserPromise = deps.openDashboardWhenReady(plan.hostBaseURL, {
      env: deps.env,
      streams: { stdout: deps.stdout, stderr: deps.stderr },
      platform: deps.platform,
    }).catch(() => {});
  }

  const signalHandlers = [];
  for (const signal of ["SIGINT", "SIGTERM"]) {
    const handler = () => {
      if (!child.killed) {
        child.kill(signal);
      }
    };
    process.on(signal, handler);
    signalHandlers.push([signal, handler]);
  }

  const exitCode = await new Promise((resolve, reject) => {
    child.on("error", reject);
    child.on("exit", (code, signal) => {
      if (signal) {
        deps.stderr.write(`maestro terminated with signal ${signal}\n`);
        resolve(1);
        return;
      }
      resolve(typeof code === "number" ? code : 1);
    });
  });

  for (const [signal, handler] of signalHandlers) {
    process.off(signal, handler);
  }
  await browserPromise;
  deps.exit(exitCode);
}

function printInstalledTargets(targets, stdout) {
  stdout.write("Installed Maestro skill bundle:\n");
  for (const target of targets) {
    stdout.write(` - ${target}\n`);
  }
}

function handleInstallSkills(deps) {
  const targets = installBundledSkills({
    baseDir: deps.baseDir,
    fs: deps.fs,
    homeDir: deps.homeDir,
  });
  printInstalledTargets(targets, deps.stdout);
}

function parseSelfUpdateArgs(argv) {
  let version = "";
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i];
    if (token === "--help" || token === "-h") {
      return { help: true, version: "" };
    }
    if (token === "--version" && typeof argv[i + 1] === "string") {
      version = argv[i + 1];
      i += 1;
      continue;
    }
    if (token.startsWith("--version=")) {
      version = token.slice("--version=".length);
      continue;
    }
    throw new Error(`unknown argument for self-update: ${token}`);
  }
  return { help: false, version };
}

function handleSelfUpdate(argv, deps) {
  const parsed = parseSelfUpdateArgs(argv);
  if (parsed.help) {
    deps.stdout.write("Usage: maestro self-update [--version <tag>]\n");
    return;
  }

  ensureDockerAvailable(deps);
  const selectedRef = parsed.version
    ? imageRefForVersion(parsed.version, DEFAULT_IMAGE_REPOSITORY)
    : `${DEFAULT_IMAGE_REPOSITORY}:latest`;
  const pull = deps.spawnSync(dockerBinary(deps.env), ["pull", selectedRef], {
    encoding: "utf8",
    stdio: "inherit",
  });
  if (pull.error) {
    throw new Error(`failed to pull docker image ${selectedRef}: ${pull.error.message}`);
  }
  if (pull.status !== 0) {
    throw new Error(`docker pull ${selectedRef} failed`);
  }
  writeRuntimeState(selectedRef, {
    fs: deps.fs,
    homeDir: deps.homeDir,
  });
  deps.stdout.write(`Pinned Maestro runtime image to ${selectedRef}\n`);
}

function handleDoctorInstall(argv, deps) {
  if (argv.some((arg) => arg === "--help" || arg === "-h")) {
    deps.stdout.write("Usage: maestro doctor install [--json]\n");
    return;
  }
  const jsonMode = argv.includes("--json");
  const checks = [];

  try {
    ensureDockerAvailable(deps);
    checks.push({ name: "docker", status: "ok" });
  } catch (error) {
    checks.push({ name: "docker", status: "fail", detail: error.message });
  }

  const homeDir = deps.homeDir;
  const runtimeImage = resolveImageRef({
    env: deps.env,
    homeDir,
    packageVersion: deps.packageVersion,
  });
  checks.push({ name: "runtime_image", status: "ok", detail: runtimeImage });

  const codexConfigDir = path.join(homeDir, ".codex");
  checks.push({
    name: "codex_config",
    status: deps.fs.existsSync(codexConfigDir) ? "ok" : "warn",
    detail: codexConfigDir,
  });

  const daemonRegistryDir = path.join(homeDir, ".maestro", "launcher", "daemons");
  checks.push({
    name: "daemon_registry",
    status: "ok",
    detail: daemonRegistryDir,
  });

  if (jsonMode) {
    deps.stdout.write(`${JSON.stringify({ checks }, null, 2)}\n`);
    return;
  }

  deps.stdout.write("INSTALL DOCTOR\n");
  for (const check of checks) {
    deps.stdout.write(`${check.name}:\t${check.status}`);
    if (check.detail) {
      deps.stdout.write(`\t${check.detail}`);
    }
    deps.stdout.write("\n");
  }
}

module.exports = {
  createDeps,
  dockerBinary,
  ensureDockerAvailable,
  ensureImageAvailable,
  ensureSupportedNodeVersion,
  handleDoctorInstall,
  handleInstallSkills,
  handleSelfUpdate,
  main,
  parseSelfUpdateArgs,
  printInstalledTargets,
  runContainerized,
};
