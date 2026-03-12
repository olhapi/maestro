const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");

const { buildInstallError, resolveExePath } = require("./get-exe-path");

test("resolveExePath finds the darwin arm64 leaf package", () => {
  const packageJsonPath = path.join(
    "/tmp",
    "node_modules",
    "@olhapi",
    "maestro-darwin-arm64",
    "package.json",
  );

  const exePath = resolveExePath({
    platform: "darwin",
    arch: "arm64",
    resolvePackageJson(packageName) {
      assert.equal(packageName, "@olhapi/maestro-darwin-arm64");
      return packageJsonPath;
    },
    existsSync(candidate) {
      assert.equal(
        candidate,
        path.join("/tmp", "node_modules", "@olhapi", "maestro-darwin-arm64", "lib", "maestro"),
      );
      return true;
    },
  });

  assert.equal(
    exePath,
    path.join("/tmp", "node_modules", "@olhapi", "maestro-darwin-arm64", "lib", "maestro"),
  );
});

test("resolveExePath appends .exe for win32", () => {
  const packageJsonPath = path.win32.join(
    "C:\\temp",
    "node_modules",
    "@olhapi",
    "maestro-win32-x64",
    "package.json",
  );

  const exePath = resolveExePath({
    platform: "win32",
    arch: "x64",
    pathModule: path.win32,
    resolvePackageJson(packageName) {
      assert.equal(packageName, "@olhapi/maestro-win32-x64");
      return packageJsonPath;
    },
    existsSync(candidate) {
      assert.equal(
        candidate,
        path.win32.join("C:\\temp", "node_modules", "@olhapi", "maestro-win32-x64", "lib", "maestro.exe"),
      );
      return true;
    },
  });

  assert.equal(
    exePath,
    path.win32.join("C:\\temp", "node_modules", "@olhapi", "maestro-win32-x64", "lib", "maestro.exe"),
  );
});

test("resolveExePath explains glibc-only linux support when the leaf package is missing", () => {
  assert.throws(
    () =>
      resolveExePath({
        platform: "linux",
        arch: "x64",
        resolvePackageJson() {
          throw new Error("not installed");
        },
        report: { header: {} },
      }),
    /glibc only/,
  );
});

test("buildInstallError lists the supported matrix for unsupported hosts", () => {
  const message = buildInstallError("linux", "arm", null, { header: { glibcVersionRuntime: "2.39" } });

  assert.match(message, /darwin\/arm64/);
  assert.match(message, /darwin\/x64/);
  assert.match(message, /linux\/x64 \(glibc\)/);
  assert.match(message, /linux\/arm64 \(glibc\)/);
  assert.match(message, /win32\/x64/);
});
