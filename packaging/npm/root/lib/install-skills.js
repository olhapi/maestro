const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const maestroSkillName = "maestro";

function bundledSkillDir(baseDir = path.join(__dirname, "..")) {
  const packagedPath = path.join(baseDir, "share", "skills", maestroSkillName);
  if (fs.existsSync(packagedPath)) {
    return packagedPath;
  }
  return path.resolve(baseDir, "..", "..", "..", "skills", maestroSkillName);
}

function bundledSkillPaths(options = {}) {
  const fsModule = options.fs || fs;
  const root = bundledSkillDir(options.baseDir);
  const paths = [];

  walkFiles(root, root, fsModule, (relativePath) => {
    paths.push(relativePath);
  });
  return paths.sort();
}

function readBundledSkillFile(relativePath, options = {}) {
  const fsModule = options.fs || fs;
  return fsModule.readFileSync(path.join(bundledSkillDir(options.baseDir), relativePath));
}

function installBundledSkills(options = {}) {
  const homeDir = options.homeDir || os.homedir();
  const targets = options.targets || [
    path.join(homeDir, ".agents", "skills", maestroSkillName),
    path.join(homeDir, ".claude", "skills", maestroSkillName),
  ];
  const sourceRoot = bundledSkillDir(options.baseDir);
  const fsModule = options.fs || fs;

  for (const target of targets) {
    installTree(sourceRoot, target, fsModule);
  }

  return targets;
}

function installTree(sourceRoot, dest, fsModule) {
  const parent = path.dirname(dest);
  fsModule.mkdirSync(parent, { recursive: true });

  const tmpDir = fsModule.mkdtempSync(path.join(parent, `.${path.basename(dest)}.tmp-`));
  try {
    copyTree(sourceRoot, tmpDir, fsModule);

    const backupDir = `${dest}.bak`;
    fsModule.rmSync(backupDir, { force: true, recursive: true });
    let hadBackup = false;
    if (fsModule.existsSync(dest)) {
      fsModule.renameSync(dest, backupDir);
      hadBackup = true;
    }

    try {
      fsModule.renameSync(tmpDir, dest);
    } catch (error) {
      if (hadBackup) {
        try {
          fsModule.renameSync(backupDir, dest);
        } catch (restoreError) {
          throw new Error(`install bundled skill: ${error.message} (failed to restore previous install: ${restoreError.message})`);
        }
      }
      throw new Error(`install bundled skill: ${error.message}`);
    }

    fsModule.rmSync(backupDir, { force: true, recursive: true });
  } finally {
    fsModule.rmSync(tmpDir, { force: true, recursive: true });
  }
}

function copyTree(sourceRoot, destRoot, fsModule) {
  walkFiles(sourceRoot, sourceRoot, fsModule, (relativePath, sourcePath, stat) => {
    const targetPath = path.join(destRoot, relativePath);
    fsModule.mkdirSync(path.dirname(targetPath), { recursive: true });
    const mode = stat.mode & 0o111 ? 0o755 : 0o644;
    fsModule.writeFileSync(targetPath, fsModule.readFileSync(sourcePath), { mode });
    fsModule.chmodSync(targetPath, mode);
  });
}

function walkFiles(root, currentRoot, fsModule, visitor) {
  const entries = fsModule.readdirSync(currentRoot, { withFileTypes: true });
  for (const entry of entries) {
    const absolutePath = path.join(currentRoot, entry.name);
    if (entry.isDirectory()) {
      walkFiles(root, absolutePath, fsModule, visitor);
      continue;
    }
    const relativePath = path.relative(root, absolutePath);
    visitor(relativePath, absolutePath, fsModule.statSync(absolutePath));
  }
}

module.exports = {
  bundledSkillDir,
  bundledSkillPaths,
  installBundledSkills,
  readBundledSkillFile,
};
