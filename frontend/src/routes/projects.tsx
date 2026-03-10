import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Play, Plus, Square, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { PageHeader } from "@/components/dashboard/page-header";
import { EpicDialog, IssueDialog, ProjectDialog } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import {
  projectDispatchBadgeClass,
  projectDispatchLabel,
  projectRunningCount,
  summaryActiveCount,
  summaryDoneCount,
  summaryTokenSpend,
  summaryTotalCount,
} from "@/lib/projects";
import { appRoutes } from "@/lib/routes";
import type { EpicSummary, ProjectSummary } from "@/lib/types";

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-[1.25rem] border border-white/8 bg-black/20 p-4">
      <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
        {label}
      </p>
      <p className="mt-3 font-display text-3xl text-white">{value}</p>
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
  const [editingProject, setEditingProject] = useState<
    ProjectSummary | undefined
  >();
  const [editingEpic, setEditingEpic] = useState<EpicSummary | undefined>();

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

  const epicCapableProjects = projects.data.items.filter(
    (project) => project.capabilities?.epics,
  );

  return (
    <div className="grid gap-5">
      <PageHeader
        eyebrow="Portfolio surface"
        title="Projects are now entry points, not dead-end rollups"
        description="Open a project or epic to see execution health, linked work, and recent movement. This page stays focused on choosing the right delivery stream."
        actions={
          <>
            <Button
              variant="secondary"
              onClick={() => {
                setEditingProject(undefined);
                setProjectDialogOpen(true);
              }}
            >
              <Plus className="size-4" />
              New project
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
              New epic
            </Button>
            <Button onClick={() => setIssueDialogOpen(true)}>
              <Plus className="size-4" />
              New issue
            </Button>
          </>
        }
      />

      <div className="grid gap-4 xl:grid-cols-2">
        {projects.data.items.map((project) => {
          const runningCount = projectRunningCount(project.id, bootstrap.data);
          return (
            <Card key={project.id} className="overflow-hidden">
              <CardHeader className="items-start">
                <div className="space-y-3">
                  <Badge>{summaryActiveCount(project)} active</Badge>
                  <div>
                    <CardTitle className="text-2xl">
                      <Link
                        params={{ projectId: project.id }}
                        to={appRoutes.projectDetail}
                      >
                        {project.name}
                      </Link>
                    </CardTitle>
                    <p className="mt-3 max-w-xl text-sm leading-7 text-[var(--muted-foreground)]">
                      {project.description || "No description yet."}
                    </p>
                    <p className="mt-2 text-xs text-[var(--muted-foreground)]">
                      {project.repo_path || "No repo path configured yet."}
                    </p>
                    <p className="mt-2 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                      {project.provider_kind || "kanban"}
                      {project.provider_project_ref
                        ? ` · ${project.provider_project_ref}`
                        : ""}
                    </p>
                    {project.dispatch_error ? (
                      <p className="mt-2 text-xs text-rose-200">
                        {project.dispatch_error}
                      </p>
                    ) : null}
                  </div>
                </div>
                <div className="flex gap-2">
                  <Badge className={projectDispatchBadgeClass(project)}>
                    {projectDispatchLabel(project)}
                  </Badge>
                  <Button
                    variant="ghost"
                    disabled={!project.dispatch_ready || runProject.isPending}
                    onClick={() => runProject.mutate(project.id)}
                  >
                    <Play className="size-4" />
                    Run
                  </Button>
                  <Button
                    variant="ghost"
                    disabled={runningCount === 0 || stopProject.isPending}
                    onClick={() => stopProject.mutate(project.id)}
                  >
                    <Square className="size-4" />
                    Stop
                  </Button>
                  <Button
                    variant="ghost"
                    onClick={() => {
                      setEditingProject(project);
                      setProjectDialogOpen(true);
                    }}
                  >
                    Edit
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => deleteProject.mutate(project.id)}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
              </CardHeader>

              <CardContent className="grid gap-4">
                <div className="grid grid-cols-4 gap-3">
                  <StatCard
                    label="Total"
                    value={String(summaryTotalCount(project))}
                  />
                  <StatCard
                    label="Done"
                    value={String(summaryDoneCount(project))}
                  />
                  <StatCard
                    label="Blocked/active"
                    value={String(summaryActiveCount(project))}
                  />
                  <StatCard
                    label="Tokens"
                    value={String(summaryTokenSpend(project))}
                  />
                </div>

              </CardContent>
            </Card>
          );
        })}
      </div>

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
        onSubmit={async (body) => {
          await api.createIssue(body);
          toast.success("Issue created");
          await invalidate();
        }}
      />
    </div>
  );
}
