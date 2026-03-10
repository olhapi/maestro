import type {
  BootstrapResponse,
  EpicSummary,
  Project,
  ProjectSummary,
  StateBucket,
} from "@/lib/types";

export function isProjectDispatchReady(
  project: Pick<Project, "orchestration_ready" | "dispatch_ready">,
) {
  return project.dispatch_ready ?? project.orchestration_ready;
}

export function projectDispatchLabel(
  project: Pick<
    Project,
    "orchestration_ready" | "dispatch_ready" | "dispatch_error"
  >,
) {
  if (isProjectDispatchReady(project)) {
    return "Runnable";
  }
  if (project.dispatch_error) {
    return "Out of scope";
  }
  return "Needs repo setup";
}

export function projectDispatchBadgeClass(
  project: Pick<
    Project,
    "orchestration_ready" | "dispatch_ready" | "dispatch_error"
  >,
) {
  if (isProjectDispatchReady(project)) {
    return "border-lime-400/30 bg-lime-400/10 text-lime-200";
  }
  if (project.dispatch_error) {
    return "border-rose-400/30 bg-rose-400/10 text-rose-200";
  }
  return "border-amber-400/30 bg-amber-400/10 text-amber-200";
}

function countBuckets(buckets?: StateBucket[]) {
  return (buckets ?? []).reduce(
    (totals, bucket) => {
      totals.total += bucket.count;
      if (bucket.is_active) totals.active += bucket.count;
      if (bucket.is_terminal) totals.terminal += bucket.count;
      return totals;
    },
    { total: 0, active: 0, terminal: 0 },
  );
}

function fallbackCounts(summary: Pick<ProjectSummary | EpicSummary, "counts">) {
  return {
    total:
      summary.counts.backlog +
      summary.counts.ready +
      summary.counts.in_progress +
      summary.counts.in_review +
      summary.counts.done +
      summary.counts.cancelled,
    active:
      summary.counts.ready +
      summary.counts.in_progress +
      summary.counts.in_review,
    terminal: summary.counts.done + summary.counts.cancelled,
    done: summary.counts.done,
  };
}

export function summaryActiveCount(summary: ProjectSummary | EpicSummary) {
  const bucketTotals = countBuckets(summary.state_buckets);
  return (
    summary.active_count ??
    (bucketTotals.active || fallbackCounts(summary).active)
  );
}

export function summaryTotalCount(summary: ProjectSummary | EpicSummary) {
  const bucketTotals = countBuckets(summary.state_buckets);
  return (
    summary.total_count ?? (bucketTotals.total || fallbackCounts(summary).total)
  );
}

export function summaryTerminalCount(summary: ProjectSummary | EpicSummary) {
  const bucketTotals = countBuckets(summary.state_buckets);
  return (
    summary.terminal_count ??
    (bucketTotals.terminal || fallbackCounts(summary).terminal)
  );
}

export function summaryDoneCount(summary: ProjectSummary | EpicSummary) {
  return (
    summary.state_buckets?.find((bucket) => bucket.state === "done")?.count ??
    fallbackCounts(summary).done
  );
}

export function summaryTokenSpend(summary: ProjectSummary) {
  return summary.total_tokens_spent ?? 0;
}

export function projectRunningCount(
  projectID: string,
  bootstrap?: Pick<BootstrapResponse, "overview" | "issues">,
) {
  if (!bootstrap) return 0;
  const issueProjectIDs = new Map(
    bootstrap.issues.items.map((issue) => [issue.id, issue.project_id ?? ""]),
  );
  return bootstrap.overview.snapshot.running.reduce((count, entry) => {
    return count + (issueProjectIDs.get(entry.issue_id) === projectID ? 1 : 0);
  }, 0);
}
