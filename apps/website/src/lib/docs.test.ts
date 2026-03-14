import { describe, expect, it } from "vitest";

import { extractSearchTextFromBody, getSearchEntries } from "./docs";

describe("docs search entries", () => {
  it("extracts markdown headings from doc bodies for search", () => {
    expect(
      extractSearchTextFromBody(`
## Control center

### Work board

Text here.

### Issue detail
      `),
    ).toBe("Control center Work board Issue detail");
  });

  it("includes doc headings in hidden search text", () => {
    const entries = getSearchEntries([
      {
        id: "control-center.mdx",
        body: `
## What the dashboard is for

### Work board

### Sessions
        `,
        data: {
          title: "Control center",
          description: "Read the embedded dashboard as the live supervision surface.",
          section: "concepts",
          order: 1,
          navLabel: "Control center",
        },
      } as never,
    ]);

    const controlCenter = entries.find((entry) => entry.href === "/docs/control-center");
    expect(controlCenter?.searchText).toContain("Work board");
    expect(controlCenter?.searchText).toContain("Sessions");
  });
});
