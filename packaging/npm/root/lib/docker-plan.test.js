const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { DatabaseSync } = require("node:sqlite");
const test = require("node:test");

const {
  parsePublishedPort,
  planDockerInvocation,
  rewriteLocalAPIURL,
  toContainerPath,
} = require("./docker-plan");

function createProjectsDatabase(dbPath, projects, options = {}) {
  const includeWorkflowPath = options.includeWorkflowPath !== false;
  const db = new DatabaseSync(dbPath);
  if (includeWorkflowPath) {
    db.exec(`
      CREATE TABLE projects (
        repo_path TEXT NOT NULL DEFAULT '',
        workflow_path TEXT NOT NULL DEFAULT ''
      )
    `);
    const stmt = db.prepare("INSERT INTO projects (repo_path, workflow_path) VALUES (?, ?)");
    for (const project of projects) {
      stmt.run(project.repoPath, project.workflowPath || "");
    }
  } else {
    db.exec(`
      CREATE TABLE projects (
        repo_path TEXT NOT NULL DEFAULT ''
      )
    `);
    const stmt = db.prepare("INSERT INTO projects (repo_path) VALUES (?)");
    for (const project of projects) {
      stmt.run(project.repoPath);
    }
  }
  db.close();
}

function writeWorkflowFile(workflowPath, workspaceRoot, extraFrontMatter = "", lineEnding = "\n") {
  fs.mkdirSync(path.dirname(workflowPath), { recursive: true });
  const frontMatter = [
    "---",
    "tracker:",
    "  kind: kanban",
    "workspace:",
    `  root: ${JSON.stringify(workspaceRoot)}`,
  ];
  if (extraFrontMatter) {
    frontMatter.push(extraFrontMatter);
  }
  const content = `${frontMatter.join(lineEnding)}${lineEnding}---${lineEnding}Issue {{ issue.identifier }}${lineEnding}`;
  fs.writeFileSync(workflowPath, content);
}

function volumeSpecs(plan) {
  const specs = [];
  for (let i = 0; i < plan.dockerArgs.length; i += 1) {
    if (plan.dockerArgs[i] === "-v" && typeof plan.dockerArgs[i + 1] === "string") {
      specs.push(plan.dockerArgs[i + 1]);
    }
  }
  return specs;
}

function volumeSpecsContaining(plan, needle) {
  return volumeSpecs(plan).filter((spec) => spec.includes(needle));
}

test("planDockerInvocation rewrites run ports and mounts resolved paths", async () => {
  const homeDir = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-home-"));
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-cwd-"));
  const repoPath = path.join(cwd, "repo");
  fs.mkdirSync(repoPath);

  const plan = await planDockerInvocation(
    ["--db", "~/.maestro/custom.db", "run", "--port", "127.0.0.1:9000", repoPath],
    {
      cwd,
      env: {},
      fs,
      gid: 20,
      homeDir,
      imageRef: "ghcr.io/olhapi/maestro:1.2.3",
      platform: "linux",
      uid: 10,
    },
  );

  assert.equal(plan.commandPath[0], "run");
  assert.equal(plan.hostBaseURL, "http://127.0.0.1:9000");
  assert.deepEqual(plan.rewrittenArgv, [
    "--db",
    path.join(homeDir, ".maestro", "custom.db"),
    "run",
    "--port",
    "0.0.0.0:9000",
    repoPath,
  ]);
  assert.ok(plan.dockerArgs.includes("--user"));
  assert.ok(plan.dockerArgs.includes("10:20"));
  assert.ok(plan.dockerArgs.includes("-p"));
  assert.ok(plan.dockerArgs.includes("127.0.0.1:9000:9000"));
  assert.ok(plan.dockerArgs.some((arg) => /^127\.0\.0\.1:\d+:\d+$/.test(arg)));
  assert.ok(plan.dockerArgs.includes(`HOME=${homeDir}`));
  assert.ok(plan.dockerArgs.includes(`MAESTRO_DAEMON_REGISTRY_DIR=${path.join(homeDir, ".maestro", "launcher", "daemons")}`));
});

test("planDockerInvocation discovers workspace roots from workflow files", async (t) => {
  const cases = [
    {
      name: "relative",
      root: "./workspaces",
      env: {},
      expectDedicatedMount: false,
    },
    {
      name: "home",
      root: "~/workspaces",
      env: {},
      expectDedicatedMount: true,
    },
    {
      name: "env",
      root: "$WORKSPACE_BASE/workspaces",
      env: { WORKSPACE_BASE: "" },
      expectDedicatedMount: true,
    },
  ];

  for (const tc of cases) {
    await t.test(tc.name, async () => {
      const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), `maestro-root-${tc.name}-`));
      const homeDir = path.join(tempRoot, "home");
      const cwd = path.join(tempRoot, "cwd");
      const repoPath = path.join(tempRoot, `repo-${tc.name}`);
      const workflowPath = path.join(repoPath, "WORKFLOW.md");
      const dbRoot = fs.mkdtempSync(path.join(os.tmpdir(), `maestro-db-${tc.name}-`));
      const dbPath = path.join(dbRoot, "maestro.db");
      const envBase = path.join(tempRoot, "env-base");
      const env = { ...tc.env };

      fs.mkdirSync(homeDir, { recursive: true });
      fs.mkdirSync(cwd, { recursive: true });
      fs.mkdirSync(repoPath, { recursive: true });
      fs.mkdirSync(envBase, { recursive: true });
      if (tc.name === "env") {
        env.WORKSPACE_BASE = envBase;
      }

      writeWorkflowFile(workflowPath, tc.root);
      createProjectsDatabase(dbPath, [{ repoPath, workflowPath }]);

      const plan = await planDockerInvocation(["--db", dbPath, "run"], {
        cwd,
        env,
        fs,
        gid: 20,
        homeDir,
        imageRef: "ghcr.io/olhapi/maestro:1.2.3",
        platform: "linux",
        uid: 10,
      });

      const roots = volumeSpecsContaining(plan, "workspaces");
      if (tc.expectDedicatedMount) {
        assert.equal(roots.length, 1);
        const expectedRoot = tc.name === "home" ? path.join(homeDir, "workspaces") : path.join(envBase, "workspaces");
        assert.ok(roots[0].includes(expectedRoot));
      } else {
        assert.equal(roots.length, 0);
      }
    });
  }

  await t.test("absolute", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-root-absolute-"));
    const homeDir = path.join(tempRoot, "home");
    const cwd = path.join(tempRoot, "cwd");
    const repoPath = path.join(tempRoot, "repo-absolute");
    const workflowPath = path.join(repoPath, "WORKFLOW.md");
    const dbRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-db-absolute-"));
    const dbPath = path.join(dbRoot, "maestro.db");
    const absoluteRoot = path.join(tempRoot, "absolute-workspaces");

    fs.mkdirSync(homeDir, { recursive: true });
    fs.mkdirSync(cwd, { recursive: true });
    fs.mkdirSync(repoPath, { recursive: true });
    fs.mkdirSync(absoluteRoot, { recursive: true });

    writeWorkflowFile(workflowPath, absoluteRoot);
    createProjectsDatabase(dbPath, [{ repoPath, workflowPath }]);

    const plan = await planDockerInvocation(["--db", dbPath, "run"], {
      cwd,
      env: {},
      fs,
      gid: 20,
      homeDir,
      imageRef: "ghcr.io/olhapi/maestro:1.2.3",
      platform: "linux",
      uid: 10,
    });

    const roots = volumeSpecsContaining(plan, "absolute-workspaces");
    assert.equal(roots.length, 1);
    assert.ok(roots[0].includes(absoluteRoot));
  });

  await t.test("crlf", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-root-crlf-"));
    const homeDir = path.join(tempRoot, "home");
    const cwd = path.join(tempRoot, "cwd");
    const repoPath = path.join(tempRoot, "repo-crlf");
    const workflowPath = path.join(repoPath, "WORKFLOW.md");
    const dbRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-db-crlf-"));
    const dbPath = path.join(dbRoot, "maestro.db");
    const absoluteRoot = path.join(tempRoot, "crlf-workspaces");

    fs.mkdirSync(homeDir, { recursive: true });
    fs.mkdirSync(cwd, { recursive: true });
    fs.mkdirSync(repoPath, { recursive: true });
    fs.mkdirSync(absoluteRoot, { recursive: true });

    writeWorkflowFile(workflowPath, absoluteRoot, "", "\r\n");
    createProjectsDatabase(dbPath, [{ repoPath, workflowPath }]);

    const plan = await planDockerInvocation(["--db", dbPath, "run"], {
      cwd,
      env: {},
      fs,
      gid: 20,
      homeDir,
      imageRef: "ghcr.io/olhapi/maestro:1.2.3",
      platform: "linux",
      uid: 10,
    });

    const roots = volumeSpecsContaining(plan, "crlf-workspaces");
    assert.equal(roots.length, 1);
    assert.ok(roots[0].includes(absoluteRoot));
  });

  await t.test("rejects unresolved workflow root env vars", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-root-env-"));
    const homeDir = path.join(tempRoot, "home");
    const cwd = path.join(tempRoot, "cwd");
    const repoPath = path.join(tempRoot, "repo-env");
    const workflowPath = path.join(repoPath, "WORKFLOW.md");
    const dbRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-db-env-"));
    const dbPath = path.join(dbRoot, "maestro.db");

    fs.mkdirSync(homeDir, { recursive: true });
    fs.mkdirSync(cwd, { recursive: true });
    fs.mkdirSync(repoPath, { recursive: true });

    writeWorkflowFile(workflowPath, "$WORKSPACE_BASE/workspaces");
    createProjectsDatabase(dbPath, [{ repoPath, workflowPath }]);

    await assert.rejects(
      planDockerInvocation(["--db", dbPath, "run"], {
        cwd,
        env: {},
        fs,
        gid: 20,
        homeDir,
        imageRef: "ghcr.io/olhapi/maestro:1.2.3",
        platform: "linux",
        uid: 10,
      }),
      /failed to resolve workspace root/,
    );
  });
});

test("planDockerInvocation dedupes shared workspace roots across multiple projects", async () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-shared-root-"));
  const homeDir = path.join(tempRoot, "home");
  const cwd = path.join(tempRoot, "cwd");
  const repoA = path.join(tempRoot, "repo-a");
  const repoB = path.join(tempRoot, "repo-b");
  const sharedRoot = path.join(tempRoot, "shared-workspaces");
  const workflowA = path.join(repoA, "WORKFLOW.md");
  const workflowB = path.join(repoB, "WORKFLOW.md");
  const dbRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-db-shared-"));
  const dbPath = path.join(dbRoot, "maestro.db");

  fs.mkdirSync(homeDir, { recursive: true });
  fs.mkdirSync(cwd, { recursive: true });
  fs.mkdirSync(repoA, { recursive: true });
  fs.mkdirSync(repoB, { recursive: true });

  writeWorkflowFile(workflowA, sharedRoot);
  writeWorkflowFile(workflowB, sharedRoot);
  createProjectsDatabase(dbPath, [
    { repoPath: repoA, workflowPath: workflowA },
    { repoPath: repoB, workflowPath: workflowB },
  ]);

  const plan = await planDockerInvocation(["--db", dbPath, "run"], {
    cwd,
    env: {},
    fs,
    gid: 20,
    homeDir,
    imageRef: "ghcr.io/olhapi/maestro:1.2.3",
    platform: "linux",
    uid: 10,
  });

  assert.equal(volumeSpecsContaining(plan, "shared-workspaces").length, 1);
});

test("planDockerInvocation falls back to repo_path-only project schemas", async () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-legacy-db-"));
  const homeDir = path.join(tempRoot, "home");
  const cwd = path.join(tempRoot, "cwd");
  const repoPath = path.join(tempRoot, "legacy-repo");
  const workflowPath = path.join(repoPath, "WORKFLOW.md");
  const dbRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-db-legacy-"));
  const dbPath = path.join(dbRoot, "maestro.db");
  const legacyRoot = path.join(tempRoot, "legacy-workspaces");

  fs.mkdirSync(homeDir, { recursive: true });
  fs.mkdirSync(cwd, { recursive: true });
  fs.mkdirSync(repoPath, { recursive: true });
  fs.mkdirSync(legacyRoot, { recursive: true });

  writeWorkflowFile(workflowPath, legacyRoot);
  createProjectsDatabase(dbPath, [{ repoPath }], { includeWorkflowPath: false });

  const plan = await planDockerInvocation(["--db", dbPath, "run"], {
    cwd,
    env: {},
    fs,
    gid: 20,
    homeDir,
    imageRef: "ghcr.io/olhapi/maestro:1.2.3",
    platform: "linux",
    uid: 10,
  });

  const roots = volumeSpecsContaining(plan, "legacy-workspaces");
  assert.equal(roots.length, 1);
  assert.ok(roots[0].includes(legacyRoot));
});

test("planDockerInvocation tolerates missing workflow files during bootstrap", async () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-missing-workflow-"));
  const homeDir = path.join(tempRoot, "home");
  const cwd = path.join(tempRoot, "cwd");
  const repoPath = path.join(tempRoot, "repo");
  const workflowPath = path.join(repoPath, "WORKFLOW.md");
  const dbRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-db-missing-"));
  const dbPath = path.join(dbRoot, "maestro.db");

  fs.mkdirSync(homeDir, { recursive: true });
  fs.mkdirSync(cwd, { recursive: true });
  fs.mkdirSync(repoPath, { recursive: true });
  createProjectsDatabase(dbPath, [{ repoPath, workflowPath }]);

  const plan = await planDockerInvocation(["--db", dbPath, "run"], {
    cwd,
    env: {},
    fs,
    gid: 20,
    homeDir,
    imageRef: "ghcr.io/olhapi/maestro:1.2.3",
    platform: "linux",
    uid: 10,
  });

  assert.ok(volumeSpecsContaining(plan, repoPath).length >= 1);
});

test("planDockerInvocation skips database discovery when the database is absent", async () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-absent-db-"));
  const homeDir = path.join(tempRoot, "home");
  const cwd = path.join(tempRoot, "cwd");
  const dbPath = path.join(tempRoot, "missing.db");

  fs.mkdirSync(homeDir, { recursive: true });
  fs.mkdirSync(cwd, { recursive: true });

  const plan = await planDockerInvocation(["--db", dbPath, "run"], {
    cwd,
    env: {},
    fs,
    gid: 20,
    homeDir,
    imageRef: "ghcr.io/olhapi/maestro:1.2.3",
    platform: "linux",
    uid: 10,
  });

  assert.equal(volumeSpecsContaining(plan, "workspaces").length, 0);
});

test("planDockerInvocation rejects malformed workflow front matter", async () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-malformed-workflow-"));
  const homeDir = path.join(tempRoot, "home");
  const cwd = path.join(tempRoot, "cwd");
  const repoPath = path.join(tempRoot, "repo");
  const workflowPath = path.join(repoPath, "WORKFLOW.md");
  const dbRoot = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-db-malformed-"));
  const dbPath = path.join(dbRoot, "maestro.db");

  fs.mkdirSync(homeDir, { recursive: true });
  fs.mkdirSync(cwd, { recursive: true });
  fs.mkdirSync(repoPath, { recursive: true });
  fs.writeFileSync(
    workflowPath,
    `---
tracker:
  kind: kanban
workspace:
  root:
    - not-a-scalar
---
Issue {{ issue.identifier }}
`,
  );
  createProjectsDatabase(dbPath, [{ repoPath, workflowPath }]);

  await assert.rejects(
    planDockerInvocation(["--db", dbPath, "run"], {
      cwd,
      env: {},
      fs,
      gid: 20,
      homeDir,
      imageRef: "ghcr.io/olhapi/maestro:1.2.3",
      platform: "linux",
      uid: 10,
    }),
    /workspace\.root must be a string/,
  );
});

test("planDockerInvocation rewrites localhost api urls to host.docker.internal", async () => {
  const plan = await planDockerInvocation(
    ["status", "--api-url", "http://127.0.0.1:8787"],
    {
      cwd: process.cwd(),
      env: {},
      fs,
      gid: 20,
      homeDir: fs.mkdtempSync(path.join(os.tmpdir(), "maestro-home-")),
      imageRef: "ghcr.io/olhapi/maestro:1.2.3",
      platform: "linux",
      uid: 10,
    },
  );

  assert.deepEqual(plan.rewrittenArgv, ["status", "--api-url", "http://host.docker.internal:8787/"]);
  assert.ok(plan.dockerArgs.includes("--add-host"));
  assert.ok(plan.dockerArgs.includes("host.docker.internal:host-gateway"));
});

test("rewriteLocalAPIURL leaves non-local api urls unchanged", () => {
  assert.deepEqual(rewriteLocalAPIURL("https://maestro.example.com"), {
    usesHostGateway: false,
    value: "https://maestro.example.com",
  });
});

test("parsePublishedPort preserves bracketed IPv6 hosts", () => {
  assert.deepEqual(parsePublishedPort("[::1]:8787"), {
    hostBinding: "[::1]:8787:8787",
    containerPortFlag: "0.0.0.0:8787",
    hostBaseURL: "http://[::1]:8787",
  });
});

test("toContainerPath maps windows home paths into the launcher home mount", () => {
  assert.equal(
    toContainerPath("C:\\Users\\Alice\\repo", {
      homeDir: "C:\\Users\\Alice",
      platform: "win32",
    }),
    "/maestro-host/home/repo",
  );
});

test("toContainerPath maps non-home windows paths into drive mounts", () => {
  assert.equal(
    toContainerPath("D:\\work\\repo", {
      homeDir: "C:\\Users\\Alice",
      platform: "win32",
    }),
    "/maestro-host/d/work/repo",
  );
});
