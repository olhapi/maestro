import { describe, expect, it } from "vitest";

import { summaryProgress, summaryStateSegments } from "@/lib/projects";
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

describe("summaryStateSegments", () => {
  it("orders state buckets by lifecycle and preserves the dashboard palette", () => {
    const epic = makeEpicSummary({
      state_buckets: [
        { state: "done", count: 3, is_terminal: true },
        { state: "ready", count: 0, is_active: true },
        { state: "backlog", count: 1 },
        { state: "in_progress", count: 2, is_active: true },
      ],
      counts: {
        backlog: 1,
        ready: 0,
        in_progress: 2,
        in_review: 0,
        done: 3,
        cancelled: 0,
      },
    });

    expect(summaryStateSegments(epic)).toEqual([
      {
        state: "backlog",
        label: "Backlog",
        count: 1,
        percent: 16.67,
        fillClass: "bg-slate-400/90",
      },
      {
        state: "in_progress",
        label: "In Progress",
        count: 2,
        percent: 33.33,
        fillClass: "bg-lime-400/90",
      },
      {
        state: "done",
        label: "Done",
        count: 3,
        percent: 50,
        fillClass: "bg-emerald-400/90",
      },
    ]);
  });

  it("falls back to summary counts when state buckets are missing", () => {
    const epic = makeEpicSummary({
      counts: {
        backlog: 2,
        ready: 1,
        in_progress: 1,
        in_review: 0,
        done: 0,
        cancelled: 0,
      },
    });

    expect(summaryStateSegments(epic)).toEqual([
      {
        state: "backlog",
        label: "Backlog",
        count: 2,
        percent: 50,
        fillClass: "bg-slate-400/90",
      },
      {
        state: "ready",
        label: "Ready",
        count: 1,
        percent: 25,
        fillClass: "bg-cyan-400/90",
      },
      {
        state: "in_progress",
        label: "In Progress",
        count: 1,
        percent: 25,
        fillClass: "bg-lime-400/90",
      },
    ]);
  });
});
