const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const DEFAULT_IMAGE_REPOSITORY = "ghcr.io/olhapi/maestro";

function sanitizeVersion(version) {
  return String(version || "").trim().replace(/^v/, "");
}

function launcherDir(homeDir = os.homedir()) {
  return path.join(homeDir, ".maestro", "launcher");
}

function runtimeStatePath(homeDir = os.homedir()) {
  return path.join(launcherDir(homeDir), "runtime.json");
}

function imageRefForVersion(version, imageRepository = DEFAULT_IMAGE_REPOSITORY) {
  const tag = sanitizeVersion(version);
  if (!tag) {
    throw new Error("image version is required");
  }
  return `${imageRepository}:${tag}`;
}

function readRuntimeState(options = {}) {
  const fsModule = options.fs || fs;
  const homeDir = options.homeDir || os.homedir();
  const statePath = runtimeStatePath(homeDir);
  if (!fsModule.existsSync(statePath)) {
    return null;
  }
  const raw = fsModule.readFileSync(statePath, "utf8");
  const parsed = JSON.parse(raw);
  if (!parsed || typeof parsed !== "object" || typeof parsed.image !== "string") {
    throw new Error(`invalid runtime state at ${statePath}`);
  }
  return parsed;
}

function writeRuntimeState(image, options = {}) {
  const fsModule = options.fs || fs;
  const homeDir = options.homeDir || os.homedir();
  const statePath = runtimeStatePath(homeDir);
  fsModule.mkdirSync(path.dirname(statePath), { recursive: true });
  const payload = {
    image,
    updated_at: new Date().toISOString(),
  };
  fsModule.writeFileSync(statePath, `${JSON.stringify(payload, null, 2)}\n`);
  return payload;
}

function resolveImageRef(options = {}) {
  const env = options.env || process.env;
  const runtimeState = options.runtimeState === undefined ? readRuntimeState(options) : options.runtimeState;
  const packageVersion = sanitizeVersion(options.packageVersion);
  const imageRepository = options.imageRepository || DEFAULT_IMAGE_REPOSITORY;

  if (typeof env.MAESTRO_IMAGE === "string" && env.MAESTRO_IMAGE.trim() !== "") {
    return env.MAESTRO_IMAGE.trim();
  }
  if (runtimeState && typeof runtimeState.image === "string" && runtimeState.image.trim() !== "") {
    return runtimeState.image.trim();
  }
  if (!packageVersion) {
    throw new Error("package version is required to resolve the default runtime image");
  }
  return imageRefForVersion(packageVersion, imageRepository);
}

module.exports = {
  DEFAULT_IMAGE_REPOSITORY,
  imageRefForVersion,
  launcherDir,
  readRuntimeState,
  resolveImageRef,
  runtimeStatePath,
  sanitizeVersion,
  writeRuntimeState,
};
