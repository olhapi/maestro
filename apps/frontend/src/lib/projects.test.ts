import { describe, expect, it } from "vitest";

import { summaryProgress } from "@/lib/projects";
import type { EpicSummary } from "@/lib/types";

function makeEpicSummary(overrides: Partial<EpicSummary> = {}): EpicSummary {
  return {
    id: "epic-1",
    project_id: "project-1",
    project_name: "Platform",
    name: "Observability",
    description: "Observability improvements",
    created_at: "2026-03-09T10:00:00Z",
    updated_at: "2026-03-09T10:00:00Z",
    counts: {
      backlog: 0,
      ready: 0,
      in_progress: 0,
      in_review: 0,
      done: 0,
      cancelled: 0,
    },
    ...overrides,
  };
}

describe("summaryProgress", () => {
  it("counts cancelled issues as closed work", () => {
    const epic = makeEpicSummary({
      counts: {
        backlog: 1,
        ready: 1,
        in_progress: 0,
        in_review: 0,
        done: 2,
        cancelled: 1,
      },
    });

    expect(summaryProgress(epic)).toEqual({
      closed: 3,
      total: 5,
      percent: 60,
    });
  });

  it("returns zero percent when there is no work in the summary", () => {
    expect(summaryProgress(makeEpicSummary())).toEqual({
      closed: 0,
      total: 0,
      percent: 0,
    });
  });
});
