const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  imageRefForVersion,
  readRuntimeState,
  resolveImageRef,
  runtimeStatePath,
  writeRuntimeState,
} = require("./runtime-state");

test("resolveImageRef prefers the explicit env override", () => {
  const image = resolveImageRef({
    env: { MAESTRO_IMAGE: "ghcr.io/example/maestro:dev" },
    packageVersion: "1.2.3",
    runtimeState: { image: "ghcr.io/ignored:1" },
  });

  assert.equal(image, "ghcr.io/example/maestro:dev");
});

test("resolveImageRef falls back to the pinned runtime image", () => {
  const image = resolveImageRef({
    env: {},
    packageVersion: "1.2.3",
    runtimeState: { image: "ghcr.io/olhapi/maestro:latest" },
  });

  assert.equal(image, "ghcr.io/olhapi/maestro:latest");
});

test("resolveImageRef falls back to the package version image", () => {
  const image = resolveImageRef({
    env: {},
    packageVersion: "1.2.3",
    runtimeState: null,
  });

  assert.equal(image, imageRefForVersion("1.2.3"));
});

test("writeRuntimeState persists the pinned image", () => {
  const homeDir = fs.mkdtempSync(path.join(os.tmpdir(), "maestro-runtime-state-"));
  const expectedImage = "ghcr.io/olhapi/maestro:latest";

  writeRuntimeState(expectedImage, { homeDir });
  const state = readRuntimeState({ homeDir });

  assert.equal(runtimeStatePath(homeDir), path.join(homeDir, ".maestro", "launcher", "runtime.json"));
  assert.equal(state.image, expectedImage);
  assert.match(state.updated_at, /^\d{4}-\d{2}-\d{2}T/);
});
