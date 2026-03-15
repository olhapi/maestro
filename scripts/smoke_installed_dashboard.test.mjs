import test from "node:test";
import assert from "node:assert/strict";

import { collectDashboardAssets, extractViteMappedAssets } from "./smoke_installed_dashboard.mjs";

test("extractViteMappedAssets returns no extra assets when Vite omits __vite__mapDeps", () => {
  assert.deepEqual(
    extractViteMappedAssets(`console.log("static entry bundle with no dynamic imports");`),
    [],
  );
});

test("extractViteMappedAssets parses js and css dependencies when the Vite helper is present", () => {
  const entryBody = `
    const __vite__mapDeps = (indexes) => indexes;
    const m = {};
    m.f=["assets/routes.js","assets/index.css","assets/logo.svg"];
  `;

  assert.deepEqual(extractViteMappedAssets(entryBody), [
    "assets/routes.js",
    "assets/index.css",
  ]);
});

test("collectDashboardAssets records entry scripts, modulepreloads, and stylesheets", () => {
  const assets = collectDashboardAssets(
    "http://127.0.0.1:9999",
    `
      <html>
        <head>
          <script type="module" src="/assets/index.js"></script>
          <link rel="modulepreload" href="/assets/routes.js">
          <link rel="stylesheet" href="/assets/index.css">
        </head>
      </html>
    `,
  );

  assert.deepEqual(assets.entryScripts, ["http://127.0.0.1:9999/assets/index.js"]);
  assert.deepEqual(
    Array.from(assets.byURL.keys()).sort(),
    [
      "http://127.0.0.1:9999/assets/index.css",
      "http://127.0.0.1:9999/assets/index.js",
      "http://127.0.0.1:9999/assets/routes.js",
    ],
  );
});
