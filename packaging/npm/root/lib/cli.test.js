const assert = require("node:assert/strict");
const test = require("node:test");

const { parseSelfUpdateArgs } = require("./cli");

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
