const fs = require("node:fs");
const path = require("node:path");

const supportedTargets = Object.freeze({
  "darwin:arm64": {
    packageName: "@olhapi/maestro-darwin-arm64",
    label: "darwin/arm64",
  },
  "darwin:x64": {
    packageName: "@olhapi/maestro-darwin-x64",
    label: "darwin/x64",
  },
  "linux:x64": {
    packageName: "@olhapi/maestro-linux-x64-gnu",
    label: "linux/x64 (glibc)",
  },
  "linux:arm64": {
    packageName: "@olhapi/maestro-linux-arm64-gnu",
    label: "linux/arm64 (glibc)",
  },
  "win32:x64": {
    packageName: "@olhapi/maestro-win32-x64",
    label: "win32/x64",
  },
});

function resolveTarget(platform, arch) {
  return supportedTargets[`${platform}:${arch}`] ?? null;
}

function supportedTargetSummary() {
  return Object.values(supportedTargets)
    .map((target) => target.label)
    .join(", ");
}

function readProcessReport() {
  if (!process.report || typeof process.report.getReport !== "function") {
    return null;
  }
  try {
    return process.report.getReport();
  } catch {
    return null;
  }
}

function hasGlibcRuntime(report) {
  const version = report && report.header && report.header.glibcVersionRuntime;
  return typeof version === "string" && version.trim() !== "";
}

function buildInstallError(platform, arch, expectedPackageName, report) {
  const parts = [
    `Maestro npm install supports ${supportedTargetSummary()}.`,
    `Could not resolve a packaged binary for ${platform}/${arch}.`,
  ];
  if (expectedPackageName) {
    parts.push(`Expected package: ${expectedPackageName}.`);
  }
  if (platform === "linux") {
    if (!hasGlibcRuntime(report)) {
      parts.push("Linux npm packages currently target glibc only. Alpine and other musl-based distros should build from source or use Docker.");
    } else {
      parts.push("If your install is on glibc Linux, reinstalling can restore a missing optional dependency. Alpine and other musl-based distros should build from source or use Docker.");
    }
  } else {
    parts.push("If your platform is unsupported, build from source or use Docker.");
  }
  return parts.join(" ");
}

function resolveExePath(options = {}) {
  const platform = options.platform ?? process.platform;
  const arch = options.arch ?? process.arch;
  const pathModule = options.pathModule ?? path;
  const resolvePackageJson =
    options.resolvePackageJson ??
    ((packageName) => require.resolve(`${packageName}/package.json`));
  const existsSync = options.existsSync ?? fs.existsSync;
  const report = options.report ?? readProcessReport();

  const target = resolveTarget(platform, arch);
  if (!target) {
    throw new Error(buildInstallError(platform, arch, null, report));
  }

  let packageJsonPath;
  try {
    packageJsonPath = resolvePackageJson(target.packageName);
  } catch {
    throw new Error(buildInstallError(platform, arch, target.packageName, report));
  }

  let exePath = pathModule.join(
    pathModule.dirname(packageJsonPath),
    "lib",
    platform === "win32" ? "maestro.exe" : "maestro",
  );
  if (platform === "win32" && exePath.length >= 248 && !exePath.startsWith("\\\\?\\")) {
    exePath = `\\\\?\\${exePath}`;
  }
  if (!existsSync(exePath)) {
    throw new Error(`Executable not found in ${target.packageName}: ${exePath}`);
  }
  return exePath;
}

function getExePath() {
  return resolveExePath();
}

module.exports = {
  buildInstallError,
  getExePath,
  resolveExePath,
  resolveTarget,
};
