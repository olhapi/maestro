const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  bundledSkillPaths,
  installBundledSkills,
  readBundledSkillFile,
} = require("./install-skills");

test("installBundledSkills writes and restores the packaged skill bundle", () => {
  const homeDir = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-skills-home-"));
  const baseDir = path.join(__dirname, "..");

  const targets = installBundledSkills({ baseDir, homeDir });
  assert.deepEqual(targets, [
    path.join(homeDir, ".agents", "skills", "maestro"),
    path.join(homeDir, ".claude", "skills", "maestro"),
  ]);

  const bundledPaths = bundledSkillPaths({ baseDir });
  for (const target of targets) {
    for (const relativePath of bundledPaths) {
      const installedPath = path.join(target, relativePath);
      const expected = readBundledSkillFile(relativePath, { baseDir });
      const actual = fs.readFileSync(installedPath);
      assert.equal(String(actual), String(expected));
      assert.notEqual(fs.statSync(installedPath).mode & 0o200, 0);
    }
  }

  const staleFile = path.join(targets[0], "stale.txt");
  fs.writeFileSync(staleFile, "stale");
  const skillFile = path.join(targets[0], "SKILL.md");
  fs.writeFileSync(skillFile, "corrupted");

  installBundledSkills({ baseDir, homeDir });

  assert.equal(fs.existsSync(staleFile), false);
  assert.equal(
    String(fs.readFileSync(skillFile)),
    String(readBundledSkillFile("SKILL.md", { baseDir })),
  );
});
