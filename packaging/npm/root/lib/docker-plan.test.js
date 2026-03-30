const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  planDockerInvocation,
  rewriteLocalAPIURL,
  toContainerPath,
} = require("./docker-plan");

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
