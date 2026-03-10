import { useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Play, Plus, Square } from "lucide-react";
import { toast } from "sonner";

import { KanbanBoard } from "@/components/dashboard/kanban-board";
import { PageHeader } from "@/components/dashboard/page-header";
import { IssuePreviewSheet } from "@/components/dashboard/issue-preview-sheet";
import { EpicDialog, IssueDialog } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { api } from "@/lib/api";
import { getStateMeta } from "@/lib/dashboard";
import {
  isProjectDispatchReady,
  projectDispatchBadgeClass,
  projectDispatchLabel,
  projectRunningCount,
  summaryActiveCount,
  summaryDoneCount,
  summaryTokenSpend,
} from "@/lib/projects";
import { appRoutes } from "@/lib/routes";
import type { IssueDetail, IssueState, IssueSummary } from "@/lib/types";
import { formatCompactNumber, formatRelativeTime } from "@/lib/utils";

function ProjectStat({
  label,
  value,
  detail,
}: {
  label: string;
  value: string;
  detail: string;
}) {
  return (
    <div className="min-w-0 border-r border-white/8 px-3 py-2.5 last:border-r-0">
      <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
        {label}
      </p>
      <p className="mt-1.5 font-display text-2xl text-white">{value}</p>
      <p className="mt-1.5 text-xs leading-4 text-[var(--muted-foreground)] md:line-clamp-2">
        {detail}
      </p>
    </div>
  );
}

export function ProjectDetailPage() {
  const { projectId } = useParams({ from: "/projects/$projectId" });
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [epicDialogOpen, setEpicDialogOpen] = useState(false);
  const [issueDialogInitial, setIssueDialogInitial] = useState<
    Partial<IssueDetail>
  >({ project_id: projectId, state: "backlog" });
  const [issueDialogOpen, setIssueDialogOpen] = useState(false);
  const [previewIssue, setPreviewIssue] = useState<IssueSummary>();

  const bootstrap = useQuery({
    queryKey: ["bootstrap"],
    queryFn: api.bootstrap,
  });
  const project = useQuery({
    queryKey: ["project", projectId],
    queryFn: () => api.getProject(projectId),
  });

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["bootstrap"] }),
      queryClient.invalidateQueries({ queryKey: ["project", projectId] }),
      queryClient.invalidateQueries({ queryKey: ["issues"] }),
      queryClient.invalidateQueries({ queryKey: ["projects"] }),
      queryClient.invalidateQueries({ queryKey: ["epics"] }),
    ]);
  };

  const stateMutation = useMutation({
    mutationFn: ({
      identifier,
      nextState,
    }: {
      identifier: string;
      nextState: IssueState;
    }) => api.setIssueState(identifier, nextState),
    onSuccess: async () => {
      toast.success("Issue moved");
      await invalidate();
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (identifier: string) => api.deleteIssue(identifier),
    onSuccess: async () => {
      toast.success("Issue deleted");
      setPreviewIssue(undefined);
      await invalidate();
    },
  });

  const runProject = useMutation({
    mutationFn: () => api.runProject(projectId),
    onSuccess: async () => {
      toast.success("Project run requested");
      await invalidate();
    },
  });

  const stopProject = useMutation({
    mutationFn: () => api.stopProject(projectId),
    onSuccess: async () => {
      toast.success("Project runs stopped");
      await invalidate();
    },
  });

  if (!bootstrap.data || !project.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />;
  }

  const totalIssues = project.data.issues.items.length;
  const runningCount = projectRunningCount(projectId, bootstrap.data);
  const dispatchReady = isProjectDispatchReady(project.data.project);
  const isRunning = runningCount > 0;
  const togglePending = runProject.isPending || stopProject.isPending;

  return (
    <div className="grid gap-[var(--section-gap)]">
      <PageHeader
        title={project.data.project.name}
        description={
          project.data.project.description || "No project description yet."
        }
        crumbs={[
          { label: "Projects", to: appRoutes.projects },
          { label: project.data.project.name },
        ]}
        actions={
          <>
            <Button
              variant="secondary"
              disabled={!dispatchReady || togglePending}
              onClick={() => (isRunning ? stopProject.mutate() : runProject.mutate())}
            >
              {isRunning ? <Square className="size-4" /> : <Play className="size-4" />}
              {isRunning ? "Stop project" : "Run project"}
            </Button>
            <Button
              variant="secondary"
              onClick={() => {
                setIssueDialogInitial({
                  project_id: projectId,
                  state: "backlog",
                });
                setIssueDialogOpen(true);
              }}
            >
              <Plus className="size-4" />
              New issue
            </Button>
            <Button onClick={() => void navigate({ to: appRoutes.work })}>
              Open work board
            </Button>
          </>
        }
        stats={
          <>
            <ProjectStat
              label="Issues"
              value={String(totalIssues)}
              detail="All work currently attached to this project."
            />
            <ProjectStat
              label="Active"
              value={String(summaryActiveCount(project.data.project))}
              detail="Issues currently in an execution state."
            />
            <ProjectStat
              label="Epics"
              value={String(project.data.epics.length)}
              detail="Delivery arcs scoped to this project."
            />
            <ProjectStat
              label="Completed"
              value={String(summaryDoneCount(project.data.project))}
              detail="Closed out work items."
            />
            <ProjectStat
              label="Tokens"
              value={formatCompactNumber(summaryTokenSpend(project.data.project))}
              detail="Lifetime tokens spent across all project issues."
            />
          </>
        }
        statsClassName="overflow-hidden rounded-[var(--panel-radius)] border border-white/10 bg-white/[0.04] sm:grid-cols-2 lg:grid-cols-5 lg:gap-0"
      />

      <Card>
        <CardContent className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
              Repo binding
            </p>
            <p className="mt-2 truncate text-sm text-white">
              {project.data.project.repo_path || "No repo path configured yet."}
            </p>
            <p className="mt-1 text-xs text-[var(--muted-foreground)]">
              {project.data.project.workflow_path ||
                "Workflow defaults to <repo>/WORKFLOW.md."}
            </p>
            <p className="mt-1 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
              Provider {project.data.project.provider_kind || "kanban"}
              {project.data.project.provider_project_ref
                ? ` · ${project.data.project.provider_project_ref}`
                : ""}
            </p>
            {project.data.project.dispatch_error ? (
              <p className="mt-2 text-xs text-rose-200">
                {project.data.project.dispatch_error}
              </p>
            ) : null}
          </div>
          {!dispatchReady ? (
            <Badge className={projectDispatchBadgeClass(project.data.project)}>
              {projectDispatchLabel(project.data.project)}
            </Badge>
          ) : null}
        </CardContent>
      </Card>

      <div className="grid gap-[var(--section-gap)] lg:grid-cols-[1.1fr_.9fr]">
        <Card>
          <CardContent>
            <div className="flex items-center justify-between gap-3">
              <div>
                <h2 className="text-2xl font-semibold text-white">
                  Epics driving this project
                </h2>
              </div>
              <Button
                variant="secondary"
                size="icon"
                className="border-white/12 bg-white/6 text-white hover:bg-white/10"
                aria-label="Create epic"
                title="Create epic"
                disabled={!project.data.project.capabilities?.epics}
                onClick={() => setEpicDialogOpen(true)}
              >
                <Plus className="size-4 shrink-0 text-[var(--accent)]" />
              </Button>
            </div>
            <div className="mt-4 grid gap-2.5">
              {project.data.epics.map((epic) => (
                <div
                  key={epic.id}
                  className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.04] p-3.5"
                >
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div>
                      <p className="text-lg font-semibold text-white">
                        <Link
                          params={{ epicId: epic.id }}
                          to={appRoutes.epicDetail}
                        >
                          {epic.name}
                        </Link>
                      </p>
                      <p className="mt-2 text-sm leading-5 text-[var(--muted-foreground)]">
                        {epic.description || "No epic description yet."}
                      </p>
                    </div>
                    <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-[var(--muted-foreground)]">
                      {summaryActiveCount(epic)} active
                    </span>
                  </div>
                  <div className="mt-3.5 grid gap-2 text-xs text-[var(--muted-foreground)] sm:grid-cols-2 xl:grid-cols-4">
                    <div className="rounded-xl border border-white/8 bg-black/20 p-3">
                      Backlog {epic.counts.backlog}
                    </div>
                    <div className="rounded-xl border border-white/8 bg-black/20 p-3">
                      Ready {epic.counts.ready}
                    </div>
                    <div className="rounded-xl border border-white/8 bg-black/20 p-3">
                      In progress {epic.counts.in_progress}
                    </div>
                    <div className="rounded-xl border border-white/8 bg-black/20 p-3">
                      Review {epic.counts.in_review}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardContent>
            <h2 className="text-2xl font-semibold text-white">
              What changed most recently
            </h2>
            <div className="mt-4 grid gap-2.5">
              {project.data.issues.items.slice(0, 5).map((issue) => (
                <button
                  key={issue.id}
                  className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.04] p-3.5 text-left transition hover:bg-white/[0.07]"
                  onClick={() => setPreviewIssue(issue)}
                >
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <p className="font-medium text-white">
                        {issue.identifier}
                      </p>
                      <p className="mt-1 text-sm text-[var(--muted-foreground)]">
                        {issue.title}
                      </p>
                    </div>
                    <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-[var(--muted-foreground)]">
                      {getStateMeta(issue.state).label}
                    </span>
                  </div>
                  <p className="mt-3 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                    {formatRelativeTime(issue.updated_at)}
                  </p>
                </button>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>

      <KanbanBoard
        items={project.data.issues.items}
        bootstrap={bootstrap.data}
        onOpenIssue={setPreviewIssue}
        onMoveIssue={(issue, nextState) =>
          stateMutation.mutate({ identifier: issue.identifier, nextState })
        }
        onCreateIssue={(nextState) => {
          setIssueDialogInitial({
            project_id: projectId,
            state: nextState ?? "backlog",
          });
          setIssueDialogOpen(true);
        }}
      />

      <IssueDialog
        open={issueDialogOpen}
        onOpenChange={setIssueDialogOpen}
        initial={issueDialogInitial}
        projects={bootstrap.data.projects}
        epics={bootstrap.data.epics.filter(
          (epic) => epic.project_id === projectId,
        )}
        onSubmit={async (body) => {
          await api.createIssue(body);
          toast.success("Issue created");
          await invalidate();
        }}
      />

      <EpicDialog
        open={epicDialogOpen}
        onOpenChange={setEpicDialogOpen}
        initial={{ project_id: projectId }}
        projects={[project.data.project]}
        onSubmit={async (body) => {
          await api.createEpic(body);
          toast.success("Epic created");
          await invalidate();
        }}
      />

      <IssuePreviewSheet
        issue={previewIssue}
        bootstrap={bootstrap.data}
        open={Boolean(previewIssue)}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setPreviewIssue(undefined);
        }}
        onInvalidate={invalidate}
        onDelete={async (identifier) => {
          await deleteMutation.mutateAsync(identifier);
        }}
        onStateChange={async (identifier, nextState) => {
          await stateMutation.mutateAsync({ identifier, nextState });
        }}
      />
    </div>
  );
}
