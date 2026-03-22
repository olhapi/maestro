import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Play, Plus, Square, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { ProjectPermissionProfileButton } from "@/components/dashboard/project-permission-profile-button";
import { ProjectDispatchBadge } from "@/components/dashboard/project-dispatch-badge";
import { PageHeader } from "@/components/dashboard/page-header";
import { EpicDialog, IssueDialog, ProjectDialog } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ButtonGroup } from "@/components/ui/button-group";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ConfirmationDialog } from "@/components/ui/confirmation-dialog";
import { api } from "@/lib/api";
import { applyIssueAssetChanges, summarizeIssueAssetFailures } from "@/lib/issue-assets";
import {
  isProjectDispatchReady,
  isProjectRunning,
  summaryActiveCount,
  summaryDoneCount,
  summaryTokenSpend,
  summaryTotalCount,
} from "@/lib/projects";
import { appRoutes } from "@/lib/routes";
import type { EpicSummary, ProjectSummary } from "@/lib/types";
import { formatCompactNumber } from "@/lib/utils";

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3">
      <p className="text-[10px] uppercase tracking-[0.16em] text-[var(--muted-foreground)]">{label}</p>
      <p className="mt-1.5 font-display text-[calc(var(--metric-value-size)-0.375rem)] leading-none text-white">
        {value}
      </p>
    </div>
  );
}

export function ProjectsPage() {
  const queryClient = useQueryClient();
  const bootstrap = useQuery({
    queryKey: ["bootstrap"],
    queryFn: api.bootstrap,
  });
  const projects = useQuery({
    queryKey: ["projects"],
    queryFn: api.listProjects,
  });
  const epics = useQuery({
    queryKey: ["epics"],
    queryFn: () => api.listEpics(),
  });
  const [projectDialogOpen, setProjectDialogOpen] = useState(false);
  const [epicDialogOpen, setEpicDialogOpen] = useState(false);
  const [issueDialogOpen, setIssueDialogOpen] = useState(false);
  const [editingProject, setEditingProject] = useState<ProjectSummary | undefined>();
  const [editingEpic, setEditingEpic] = useState<EpicSummary | undefined>();
  const [projectPendingDelete, setProjectPendingDelete] = useState<ProjectSummary | null>(null);

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["bootstrap"] }),
      queryClient.invalidateQueries({ queryKey: ["projects"] }),
      queryClient.invalidateQueries({ queryKey: ["epics"] }),
      queryClient.invalidateQueries({ queryKey: ["issues"] }),
    ]);
  };

  const deleteProject = useMutation({
    mutationFn: (id: string) => api.deleteProject(id),
    onSuccess: async () => {
      toast.success("Project deleted");
      await invalidate();
    },
    onError: (error) => {
      toast.error(
        error instanceof Error ? `Unable to delete project: ${error.message}` : "Unable to delete project",
      );
    },
  });

  const runProject = useMutation({
    mutationFn: (id: string) => api.runProject(id),
    onSuccess: async () => {
      toast.success("Project run requested");
      await invalidate();
    },
  });

  const stopProject = useMutation({
    mutationFn: (id: string) => api.stopProject(id),
    onSuccess: async () => {
      toast.success("Project runs stopped");
      await invalidate();
    },
  });

  if (!projects.data || !epics.data || !bootstrap.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />;
  }

  const epicCapableProjects = projects.data.items.filter((project) => project.capabilities?.epics);

  return (
    <div className="grid gap-[var(--section-gap)]">
      <PageHeader
        title="Projects"
        description="Open a project or epic to see execution health, linked work, and recent movement. This page stays focused on choosing the right delivery stream."
        actions={
          <ButtonGroup
            aria-label="Create work items"
            className="w-full sm:w-fit [&>button]:flex-1 sm:[&>button]:flex-none"
          >
            <Button
              variant="secondary"
              onClick={() => {
                setEditingProject(undefined);
                setProjectDialogOpen(true);
              }}
            >
              <Plus className="size-4" />
              Project
            </Button>
            <Button
              variant="secondary"
              disabled={epicCapableProjects.length === 0}
              onClick={() => {
                setEditingEpic(undefined);
                setEpicDialogOpen(true);
              }}
            >
              <Plus className="size-4" />
              Epic
            </Button>
            <Button onClick={() => setIssueDialogOpen(true)}>
              <Plus className="size-4" />
              Issue
            </Button>
          </ButtonGroup>
        }
      />

      <div className="grid gap-[var(--section-gap)] lg:grid-cols-2">
        {projects.data.items.map((project) => {
          const dispatchReady = isProjectDispatchReady(project);
          const isRunning = isProjectRunning(project);
          const togglePending = runProject.isPending || stopProject.isPending;
          return (
            <Card key={project.id} className="overflow-hidden">
              <CardHeader className="flex-col gap-4 justify-start">
                <div
                  data-testid="project-card-utility-row"
                  className="flex w-full flex-wrap items-center gap-3 lg:flex-nowrap"
                >
                  <Badge className="shrink-0">{summaryActiveCount(project)} active</Badge>
                  <div className="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-1.5 lg:flex-nowrap">
                    {!dispatchReady ? <ProjectDispatchBadge className="shrink-0" project={project} /> : null}
                    <ProjectPermissionProfileButton
                      projectId={project.id}
                      permissionProfile={project.permission_profile}
                      scopeLabel="Project access"
                      onSuccess={invalidate}
                    />
                    <Button
                      aria-label={isRunning ? "Stop" : "Run"}
                      size="icon"
                      title={isRunning ? "Stop" : "Run"}
                      variant="ghost"
                      disabled={!dispatchReady || togglePending}
                      onClick={() => (isRunning ? stopProject.mutate(project.id) : runProject.mutate(project.id))}
                    >
                      {isRunning ? <Square className="size-4" /> : <Play className="size-4" />}
                    </Button>
                    <Button
                      aria-label="Edit"
                      size="icon"
                      title="Edit"
                      variant="ghost"
                      onClick={() => {
                        setEditingProject(project);
                        setProjectDialogOpen(true);
                      }}
                    >
                      <Pencil className="size-4" />
                    </Button>
                    <Button
                      aria-label="Delete"
                      size="icon"
                      title="Delete"
                      variant="ghost"
                      disabled={deleteProject.isPending}
                      onClick={() => setProjectPendingDelete(project)}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                </div>
                <div className="grid w-full gap-2">
                  <CardTitle className="w-full text-2xl">
                    <Link params={{ projectId: project.id }} to={appRoutes.projectDetail}>
                      {project.name}
                    </Link>
                  </CardTitle>
                  <p className="w-full line-clamp-2 text-sm leading-6 text-[var(--muted-foreground)]">
                    {project.description || "No description yet."}
                  </p>
                  {project.dispatch_error ? (
                    <p className="w-full text-xs text-rose-200">{project.dispatch_error}</p>
                  ) : null}
                </div>
              </CardHeader>

              <CardContent className="grid gap-3">
                <div
                  data-testid="project-card-stats"
                  className="grid grid-cols-1 gap-2.5 sm:grid-cols-2 lg:grid-cols-4"
                >
                  <StatCard label="Total" value={String(summaryTotalCount(project))} />
                  <StatCard label="Done" value={String(summaryDoneCount(project))} />
                  <StatCard label="Blocked/active" value={String(summaryActiveCount(project))} />
                  <StatCard label="Tokens" value={formatCompactNumber(summaryTokenSpend(project))} />
                </div>
              </CardContent>
            </Card>
          );
        })}
      </div>

      <ConfirmationDialog
        open={projectPendingDelete !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) {
            setProjectPendingDelete(null);
          }
        }}
        title={projectPendingDelete ? `Delete ${projectPendingDelete.name}?` : "Delete project?"}
        description="This permanently removes the project from Maestro, including its epics, issues, workspaces, assets, and activity history."
        confirmLabel="Delete project"
        pendingLabel="Deleting project..."
        isPending={deleteProject.isPending}
        onConfirm={async () => {
          if (!projectPendingDelete) {
            return;
          }
          await deleteProject.mutateAsync(projectPendingDelete.id);
        }}
      />

      <ProjectDialog
        open={projectDialogOpen}
        onOpenChange={setProjectDialogOpen}
        initial={editingProject}
        onSubmit={async (body) => {
          if (editingProject) {
            await api.updateProject(editingProject.id, body);
            toast.success("Project updated");
          } else {
            await api.createProject(body);
            toast.success("Project created");
          }
          await invalidate();
        }}
      />

      <EpicDialog
        open={epicDialogOpen}
        onOpenChange={setEpicDialogOpen}
        initial={editingEpic}
        projects={epicCapableProjects}
        onSubmit={async (body) => {
          if (editingEpic) {
            await api.updateEpic(editingEpic.id, body);
            toast.success("Epic updated");
          } else {
            await api.createEpic(body);
            toast.success("Epic created");
          }
          await invalidate();
        }}
      />

      <IssueDialog
        open={issueDialogOpen}
        onOpenChange={setIssueDialogOpen}
        projects={projects.data.items}
        epics={epics.data.items}
        availableIssues={bootstrap.data.issues.items}
        onSubmit={async (body, imageChanges) => {
          const issue = await api.createIssue(body);
          const result = await applyIssueAssetChanges(issue.identifier, imageChanges);
          if (result.failures.length > 0) {
            toast.error(`Issue created, but ${summarizeIssueAssetFailures(result)}`);
          } else {
            toast.success("Issue created");
          }
          await invalidate();
        }}
      />
    </div>
  );
}
