import { describe, expect, it } from "vitest";

import {
  extractSearchTextFromBody,
  getDocHref,
  getDocSlug,
  getSearchEntries,
  groupDocs,
  sectionMeta,
  sectionOrder,
  sortDocs,
} from "./docs";

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

  it("normalizes markdown headings, slugs, and search entries", () => {
    expect(
      extractSearchTextFromBody(`
## Control* Center

### Workflow_config
      `),
    ).toBe("Control Center Workflow config");

    const entry = {
      id: "workflow-config.mdx",
      body: "",
      data: {
        title: "Workflow config",
        description: "Tune the daemon.",
        section: "reference",
        order: 2,
        navLabel: "Workflow config",
      },
    } as never;

    expect(getDocSlug(entry)).toBe("workflow-config");
    expect(getDocHref(entry)).toBe("/docs/workflow-config");

    const searchEntries = getSearchEntries([]);
    expect(searchEntries.find((item) => item.title === "Home")?.searchText).toContain("Product overview");
    expect(searchEntries.find((item) => item.title === "Docs")?.searchText).toContain("Docs for starting");
  });

  it("returns an empty search string when a doc body has no headings", () => {
    expect(extractSearchTextFromBody("Just prose and no markdown headings.")).toBe("");
  });

  it("sorts and groups docs by section and order", () => {
    const entries = [
      {
        id: "draft-entry.mdx",
        body: "",
        data: {
          title: "Draft entry",
          description: "Skip this",
          section: "advanced",
          order: 3,
          navLabel: "Draft entry",
          draft: true,
        },
      },
      {
        id: "alpha-entry.mdx",
        body: "",
        data: {
          title: "Alpha entry",
          description: "First entry",
          section: "getting-started",
          order: 1,
          navLabel: "Alpha entry",
        },
      },
      {
        id: "beta-entry.mdx",
        body: "",
        data: {
          title: "Beta entry",
          description: "Second entry",
          section: "getting-started",
          order: 1,
          navLabel: "Beta entry",
        },
      },
      {
        id: "gamma-entry.mdx",
        body: "",
        data: {
          title: "Gamma entry",
          description: "Third entry",
          section: "getting-started",
          order: 2,
          navLabel: "Gamma entry",
        },
      },
      {
        id: "concept-entry.mdx",
        body: "",
        data: {
          title: "Concept entry",
          description: "Concepts",
          section: "concepts",
          order: 2,
          navLabel: "Concept entry",
        },
      },
      {
        id: "reference-entry.mdx",
        body: "",
        data: {
          title: "Reference entry",
          description: "Reference",
          section: "reference",
          order: 1,
          navLabel: "Reference entry",
        },
      },
      {
        id: "advanced-entry.mdx",
        body: "",
        data: {
          title: "Advanced entry",
          description: "Advanced",
          section: "advanced",
          order: 1,
          navLabel: "Advanced entry",
        },
      },
    ] as never;

    expect(sortDocs(entries).map((entry) => entry.id)).toEqual([
      "alpha-entry.mdx",
      "beta-entry.mdx",
      "gamma-entry.mdx",
      "concept-entry.mdx",
      "reference-entry.mdx",
      "advanced-entry.mdx",
    ]);

    const grouped = groupDocs(entries);
    expect(grouped.map((group) => group.key)).toEqual(sectionOrder);
    expect(grouped[0]).toMatchObject(sectionMeta["getting-started"]);
    expect(grouped[0].items.map((entry) => entry.id)).toEqual([
      "alpha-entry.mdx",
      "beta-entry.mdx",
      "gamma-entry.mdx",
    ]);
    expect(grouped[3].items.map((entry) => entry.id)).toEqual(["advanced-entry.mdx"]);
  });
});
