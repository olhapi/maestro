import type {
  EpicSummary,
  Project,
  ProjectSummary,
  StateBucket,
} from "@/lib/types";

type DispatchProject = Pick<
  Project,
  "dispatch_error" | "dispatch_ready" | "orchestration_ready" | "repo_path" | "workflow_path"
>;

export type ProjectDispatchStatus =
  | "runnable"
  | "needs_repo_setup"
  | "out_of_scope"
  | "dispatch_blocked";

export interface ProjectDispatchGuidance {
  kind: Exclude<ProjectDispatchStatus, "runnable">;
  title: string;
  summary: string;
  mobileTip: string;
  steps: string[];
  context: string[];
}

function defaultWorkflowPath(repoPath?: string) {
  if (!repoPath) {
    return "<repo>/WORKFLOW.md";
  }

  return `${repoPath.replace(/\/+$/, "")}/WORKFLOW.md`;
}

function extractScopePath(dispatchError?: string) {
  if (!dispatchError) {
    return undefined;
  }

  return dispatchError.match(/\(([^()]+)\)\s*$/)?.[1];
}

export function isProjectDispatchReady(project: Pick<Project, "orchestration_ready" | "dispatch_ready">) {
  return project.dispatch_ready ?? project.orchestration_ready;
}

export function isProjectRunning(project: Pick<Project, "state">) {
  return project.state === "running";
}

export function projectDispatchStatus(project: DispatchProject): ProjectDispatchStatus {
  if (isProjectDispatchReady(project)) {
    return "runnable";
  }

  if (!project.dispatch_error) {
    return "needs_repo_setup";
  }

  if (project.dispatch_error.includes("outside the current server scope")) {
    return "out_of_scope";
  }

  return "dispatch_blocked";
}

export function projectDispatchLabel(project: DispatchProject) {
  switch (projectDispatchStatus(project)) {
    case "runnable":
      return "Runnable";
    case "needs_repo_setup":
      return "Needs repo setup";
    case "out_of_scope":
      return "Out of scope";
    case "dispatch_blocked":
      return "Dispatch blocked";
  }
}

export function projectDispatchBadgeClass(project: DispatchProject) {
  switch (projectDispatchStatus(project)) {
    case "runnable":
      return "border-lime-400/30 bg-lime-400/10 text-lime-200";
    case "needs_repo_setup":
      return "border-amber-400/30 bg-amber-400/10 text-amber-200";
    case "out_of_scope":
    case "dispatch_blocked":
      return "border-rose-400/30 bg-rose-400/10 text-rose-200";
  }
}

export function projectDispatchGuidance(project: DispatchProject): ProjectDispatchGuidance | null {
  const status = projectDispatchStatus(project);

  if (status === "runnable") {
    return null;
  }

  const workflowPath = project.workflow_path || defaultWorkflowPath(project.repo_path);

  if (status === "needs_repo_setup") {
    if (!project.repo_path) {
      return {
        kind: status,
        title: "Attach this project to a local repository",
        summary: "Maestro needs a checked-out repo before it can create workspaces, branches, or run the workflow.",
        mobileTip: "Tip: open project settings and set Repo path to the local checkout.",
        steps: [
          "Open the project settings and set Repo path to the local checkout for this project.",
          "Leave Workflow path empty to use WORKFLOW.md at the repo root, or set an explicit workflow file.",
          "Run the project again after the repo binding is saved.",
        ],
        context: [],
      };
    }

    return {
      kind: status,
      title: "Finish the repo binding",
      summary: "The repository is set, but dispatch still is not ready.",
      mobileTip: "Tip: verify the repo path and workflow file are both readable.",
      steps: [
        `Verify the server can read ${project.repo_path}.`,
        `Ensure workflow instructions exist at ${workflowPath}.`,
        "Run the project again after the repo and workflow paths are valid.",
      ],
      context: [
        `Repo path: ${project.repo_path}`,
        `Workflow path: ${workflowPath}`,
      ],
    };
  }

  if (status === "out_of_scope") {
    const scopePath = extractScopePath(project.dispatch_error);

    return {
      kind: status,
      title: "Bring the repo into this server scope",
      summary: "The current Maestro server can only dispatch work inside the repo scope it was started with.",
      mobileTip: "Tip: move the repo under the current server scope or restart Maestro for that repo.",
      steps: [
        scopePath
          ? `Move the project's repo path under ${scopePath}, or restart Maestro scoped to ${project.repo_path ?? "this repository"}.`
          : "Restart Maestro scoped to this project repo, or change Repo path to one inside the current server scope.",
        "Save the project once the repo binding matches the running server scope.",
        "Run the project again after the scope mismatch is resolved.",
      ],
      context: [
        project.repo_path ? `Project repo: ${project.repo_path}` : "Project repo: not configured",
        scopePath ? `Server scope: ${scopePath}` : "",
      ].filter(Boolean),
    };
  }

  return {
    kind: status,
    title: "Clear the dispatch error",
    summary: "A project configuration issue is blocking dispatch.",
    mobileTip: "Tip: review the dispatch error and fix the project settings before running again.",
    steps: [
      "Fix the repo binding or provider settings reported by the project error.",
      `Confirm the workflow instructions are readable at ${workflowPath}.`,
      "Run the project again after the configuration error is resolved.",
    ],
    context: [
      project.repo_path ? `Repo path: ${project.repo_path}` : "Repo path: not configured",
      `Workflow path: ${workflowPath}`,
      project.dispatch_error ? `Current error: ${project.dispatch_error}` : "",
    ].filter(Boolean),
  };
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
