import { describe, expect, it } from "vitest";

import { canEditIssueType } from "@/lib/issues";

describe("canEditIssueType", () => {
  it("allows editing local or new issues", () => {
    expect(canEditIssueType()).toBe(true);
    expect(canEditIssueType({ provider_kind: "kanban" })).toBe(true);
  });

  it("blocks editing provider-backed issues", () => {
    expect(canEditIssueType({ provider_kind: "stub" })).toBe(false);
    expect(canEditIssueType({ provider_kind: "github" })).toBe(false);
  });
});
