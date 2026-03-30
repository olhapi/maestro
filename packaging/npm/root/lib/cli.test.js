const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const { parseSelfUpdateArgs, runContainerized } = require("./cli");

test("parseSelfUpdateArgs accepts an explicit version", () => {
  assert.deepEqual(parseSelfUpdateArgs(["--version", "1.2.3"]), {
    help: false,
    version: "1.2.3",
  });
});

test("parseSelfUpdateArgs reports help", () => {
  assert.deepEqual(parseSelfUpdateArgs(["--help"]), {
    help: true,
    version: "",
  });
});

test("runContainerized skips browser open when MAESTRO_DISABLE_BROWSER_OPEN is set", async () => {
  const child = new EventEmitter();
  child.killed = false;
  child.kill = () => {};

  let exitCode = null;
  let openCalls = 0;

  const runPromise = runContainerized(["run"], {
    cwd: process.cwd(),
    env: { MAESTRO_DISABLE_BROWSER_OPEN: "1" },
    fs,
    gid: 20,
    homeDir: fs.mkdtempSync(path.join(os.tmpdir(), "maestro-home-")),
    packageVersion: "1.2.3",
    platform: process.platform,
    planDockerInvocation: async () => ({
      commandPath: ["run"],
      dockerArgs: ["run", "ghcr.io/olhapi/maestro:1.2.3", "run"],
      hostBaseURL: "http://127.0.0.1:8787",
      rewrittenArgv: ["run"],
    }),
    spawn: () => child,
    spawnSync: () => ({ status: 0, stderr: "" }),
    stderr: { write() {} },
    stdout: { write() {} },
    uid: 10,
    openDashboardWhenReady: async () => {
      openCalls += 1;
    },
    exit: (code) => {
      exitCode = code;
    },
  });

  setImmediate(() => {
    child.emit("exit", 0, null);
  });

  await runPromise;

  assert.equal(openCalls, 0);
  assert.equal(exitCode, 0);
});
