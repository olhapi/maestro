import { useState } from "react";
import { useNavigate, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Play, Plus, Pause, Square, Workflow } from "lucide-react";
import { toast } from "sonner";

import { PageHeader } from "@/components/dashboard/page-header";
import { AutomationDialog } from "@/components/forms";
import { ConfirmationDialog } from "@/components/ui/confirmation-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { api } from "@/lib/api";
import { appRoutes } from "@/lib/routes";
import { summaryTokenSpend, summaryTotalCount } from "@/lib/projects";
import type { IssueSummary } from "@/lib/types";
import { formatCompactNumber, formatDateTime, formatRelativeTime } from "@/lib/utils";

function StatCard({
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
      <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{label}</p>
      <p className="mt-1.5 font-display text-2xl text-white">{value}</p>
      <p className="mt-1.5 text-xs leading-4 text-[var(--muted-foreground)] md:line-clamp-2">
        {detail}
      </p>
    </div>
  );
}

function automationStatusLabel(issue: IssueSummary) {
  if (issue.enabled === false) {
    return "Paused";
  }
  return issue.next_run_at ? "Scheduled" : "Ready";
}

export function ProjectAutomationsPage() {
  const { projectId } = useParams({ from: "/projects/$projectId/automations" });
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [selectedAutomationID, setSelectedAutomationID] = useState<string>();
  const [automationDialogOpen, setAutomationDialogOpen] = useState(false);
  const [automationDialogInitial, setAutomationDialogInitial] = useState<Partial<IssueSummary>>();
  const [deleteTarget, setDeleteTarget] = useState<IssueSummary | null>(null);

  const project = useQuery({
    queryKey: ["project", projectId],
    queryFn: () => api.getProject(projectId),
  });
  const automations = useQuery({
    queryKey: ["project-automations", projectId],
    queryFn: () =>
      api.listIssues({
        project_id: projectId,
        issue_type: "recurring",
        sort: "updated_desc",
        limit: 200,
      }),
  });

  const selectedAutomation = automations.data?.items.find((item) => item.identifier === selectedAutomationID) ??
    automations.data?.items[0];

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["project-automations", projectId] }),
      queryClient.invalidateQueries({ queryKey: ["project", projectId] }),
    ]);
  };

  const runNowMutation = useMutation({
    mutationFn: (identifier: string) => api.runIssueNow(identifier),
    onSuccess: async () => {
      toast.success("Automation queued");
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to queue automation: ${error.message}` : "Unable to queue automation");
    },
  });

  const toggleEnabledMutation = useMutation({
    mutationFn: ({
      identifier,
      enabled,
    }: {
      identifier: string;
      enabled: boolean;
    }) => api.updateIssue(identifier, { enabled }),
    onSuccess: async () => {
      toast.success("Automation updated");
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to update automation: ${error.message}` : "Unable to update automation");
    },
  });

  const saveAutomationMutation = useMutation({
    mutationFn: async ({
      identifier,
      body,
    }: {
      identifier?: string;
      body: Record<string, unknown>;
    }): Promise<IssueSummary> => {
      if (identifier) {
        return api.updateIssue(identifier, body);
      }
      return api.createIssue(body);
    },
    onSuccess: async (_issue, variables) => {
      toast.success(variables.identifier ? "Automation updated" : "Automation created");
      setAutomationDialogOpen(false);
      setAutomationDialogInitial(undefined);
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to save automation: ${error.message}` : "Unable to save automation");
    },
  });

  const deleteAutomationMutation = useMutation({
    mutationFn: (identifier: string) => api.deleteIssue(identifier),
    onSuccess: async () => {
      toast.success("Automation deleted");
      setDeleteTarget(null);
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to delete automation: ${error.message}` : "Unable to delete automation");
    },
  });

  if (!project.data || !automations.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />;
  }

  const automationCount = automations.data.total ?? automations.data.items.length;
  const enabledCount = automations.data.items.filter((item) => item.enabled !== false).length;
  const pausedCount = automations.data.items.filter((item) => item.enabled === false).length;
  const selected = selectedAutomation ?? automations.data.items[0];

  return (
    <div className="grid gap-[var(--section-gap)]">
      <PageHeader
        title={
          <span className="inline-flex flex-wrap items-center gap-2">
            <span>{project.data.project.name}</span>
            <Badge className="border-cyan-400/20 bg-cyan-400/10 text-cyan-100">Automations</Badge>
          </span>
        }
        description="Project-owned recurring templates live here. Standard issue work stays on the project page and the work board."
        crumbs={[
          { label: "Projects", to: appRoutes.projects },
          { label: project.data.project.name, to: appRoutes.projectDetail, params: { projectId } },
          { label: "Automations" },
        ]}
        actions={
          <>
            <Button
              variant="secondary"
              onClick={() =>
                void navigate({
                  to: appRoutes.projectDetail,
                  params: { projectId },
                })
              }
            >
              Back to project
            </Button>
            <Button
              onClick={() => {
                setAutomationDialogInitial({ project_id: projectId });
                setAutomationDialogOpen(true);
              }}
            >
              <Plus className="size-4" />
              New automation
            </Button>
          </>
        }
        statsClassName="overflow-hidden rounded-[var(--panel-radius)] border border-white/10 bg-white/[0.04] sm:grid-cols-2 lg:grid-cols-5 lg:gap-0"
        stats={
          <>
            <StatCard
              label="Work items"
              value={String(summaryTotalCount(project.data.project))}
              detail="Standard issues currently attached to the project."
            />
            <StatCard
              label="Automations"
              value={String(automationCount)}
              detail="Recurring templates managed from this project."
            />
            <StatCard
              label="Enabled"
              value={String(enabledCount)}
              detail="Automations ready to enqueue on schedule."
            />
            <StatCard
              label="Paused"
              value={String(pausedCount)}
              detail="Automations that are still visible but inactive."
            />
            <StatCard
              label="Tokens"
              value={formatCompactNumber(summaryTokenSpend(project.data.project))}
              detail="Lifetime tokens spent across the project."
            />
          </>
        }
      />

      <div className="grid gap-[var(--section-gap)] lg:grid-cols-[minmax(0,0.95fr)_minmax(0,1.05fr)]">
        <Card className="min-h-[36rem]">
          <CardContent className="grid gap-3 pt-[var(--panel-padding)]">
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0">
                <h2 className="text-2xl font-semibold text-white">Automation list</h2>
                <p className="mt-1 text-sm text-[var(--muted-foreground)]">
                  Select an automation to inspect its schedule and template details.
                </p>
              </div>
              <Badge className="shrink-0 border-white/10 bg-white/5 text-white">{automationCount} total</Badge>
            </div>

            {automations.data.items.length === 0 ? (
              <div className="grid gap-3 rounded-[calc(var(--panel-radius)-0.125rem)] border border-dashed border-white/12 bg-black/20 px-4 py-6">
                <p className="text-sm font-medium text-white">No automations yet</p>
                <p className="text-sm text-[var(--muted-foreground)]">
                  Create the first recurring template for this project.
                </p>
                <div>
                  <Button
                    onClick={() => {
                      setAutomationDialogInitial({ project_id: projectId });
                      setAutomationDialogOpen(true);
                    }}
                  >
                    <Plus className="size-4" />
                    New automation
                  </Button>
                </div>
              </div>
            ) : (
              <div className="grid gap-2.5">
                {automations.data.items.map((automation) => {
                  const isSelected = automation.identifier === selected?.identifier;
                  return (
                    <button
                      key={automation.id}
                      className={`grid gap-2 rounded-[calc(var(--panel-radius)-0.125rem)] border p-4 text-left transition ${
                        isSelected
                          ? "border-[var(--accent)]/35 bg-[linear-gradient(135deg,rgba(196,255,87,.08),rgba(255,255,255,.04))]"
                          : "border-white/10 bg-white/[0.04] hover:border-white/16 hover:bg-white/[0.06]"
                      }`}
                      type="button"
                      onClick={() => setSelectedAutomationID(automation.identifier)}
                    >
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                          <div className="flex flex-wrap items-center gap-2">
                            <span className="font-mono text-xs uppercase tracking-[0.22em] text-[var(--muted-foreground)]">
                              {automation.identifier}
                            </span>
                            <Badge className="border-cyan-400/20 bg-cyan-400/10 text-cyan-100">
                              Automation
                            </Badge>
                            <Badge className="border-white/10 bg-white/5 text-white">
                              {automationStatusLabel(automation)}
                            </Badge>
                          </div>
                          <p className="mt-2 text-sm font-semibold leading-5 text-white">
                            {automation.title}
                          </p>
                        </div>
                        <Workflow className="size-4 shrink-0 text-[var(--muted-foreground)]" />
                      </div>
                      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-[var(--muted-foreground)]">
                        <span>{automation.cron || "No schedule"}</span>
                        <span>Priority {automation.priority}</span>
                        <span>{automation.enabled === false ? "Paused" : "Enabled"}</span>
                        {automation.next_run_at ? <span>Next {formatRelativeTime(automation.next_run_at)}</span> : null}
                      </div>
                    </button>
                  );
                })}
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="min-h-[36rem]">
          <CardContent className="grid gap-4 pt-[var(--panel-padding)]">
            {selected ? (
              <>
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge>{selected.identifier}</Badge>
                      <Badge className="border-cyan-400/20 bg-cyan-400/10 text-cyan-100">Automation</Badge>
                      <Badge className="border-white/10 bg-white/5 text-white">
                        {automationStatusLabel(selected)}
                      </Badge>
                      {selected.project_name ? <Badge className="border-white/10 bg-white/5 text-white">{selected.project_name}</Badge> : null}
                      {selected.epic_name ? <Badge className="border-white/10 bg-white/5 text-white">{selected.epic_name}</Badge> : null}
                    </div>
                    <h2 className="mt-4 text-2xl font-semibold text-white">{selected.title}</h2>
                    <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                      Updated {formatRelativeTime(selected.updated_at)} · {selected.enabled === false ? "Paused" : "Enabled"}
                    </p>
                  </div>
                </div>

                <div className="grid gap-3 sm:grid-cols-2">
                  <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.04] p-3.5">
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Schedule</p>
                    <p className="mt-3 text-white">{selected.cron || "No schedule configured"}</p>
                    <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                      {selected.next_run_at ? `Next run ${formatDateTime(selected.next_run_at)}` : selected.enabled === false ? "Paused" : "Ready to run"}
                    </p>
                  </div>
                  <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.04] p-3.5">
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Execution</p>
                    <div className="mt-3 grid gap-2 text-sm text-[var(--muted-foreground)]">
                      <span>Priority {selected.priority}</span>
                      <span>{selected.permission_profile ?? "default"} access</span>
                      <span>{selected.agent_name || "No assigned agent"}</span>
                    </div>
                  </div>
                </div>

                <div className="grid gap-3 rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.04] p-3.5">
                  <div>
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Template</p>
                    <p className="mt-3 whitespace-pre-wrap text-sm leading-6 text-white">
                      {selected.description?.trim() || "No template description provided."}
                    </p>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {(selected.labels ?? []).length > 0 ? (
                      (selected.labels ?? []).map((label) => (
                        <Badge key={label} className="border-white/12 bg-white/5 text-white">
                          {label}
                        </Badge>
                      ))
                    ) : (
                      <span className="text-sm text-[var(--muted-foreground)]">No labels</span>
                    )}
                  </div>
                  <div className="grid gap-1 text-sm text-[var(--muted-foreground)]">
                    <span>Agent prompt</span>
                    <p className="whitespace-pre-wrap text-white">
                      {selected.agent_prompt?.trim() || "No agent prompt provided."}
                    </p>
                  </div>
                </div>

                <div className="grid gap-3 sm:grid-cols-3">
                  <Button
                    variant="secondary"
                    onClick={() => {
                      setAutomationDialogInitial(selected);
                      setAutomationDialogOpen(true);
                    }}
                  >
                    <Pencil className="size-4" />
                    Edit automation
                  </Button>
                  <Button
                    variant="secondary"
                    disabled={runNowMutation.isPending}
                    onClick={() => runNowMutation.mutate(selected.identifier)}
                  >
                    <Play className="size-4" />
                    {runNowMutation.isPending ? "Queueing..." : "Run now"}
                  </Button>
                  <Button
                    variant="secondary"
                    disabled={toggleEnabledMutation.isPending}
                    onClick={() =>
                      toggleEnabledMutation.mutate({
                        identifier: selected.identifier,
                        enabled: selected.enabled === false,
                      })
                    }
                  >
                    {selected.enabled === false ? <Square className="size-4" /> : <Pause className="size-4" />}
                    {selected.enabled === false ? "Resume" : "Pause"}
                  </Button>
                  <Button
                    variant="secondary"
                    onClick={() =>
                      void navigate({
                        to: appRoutes.issueDetail,
                        params: { identifier: selected.identifier },
                      })
                    }
                  >
                    Open legacy detail
                  </Button>
                  <Button
                    variant="destructive"
                    onClick={() => setDeleteTarget(selected)}
                  >
                    Delete automation
                  </Button>
                </div>
              </>
            ) : (
              <div className="grid h-full place-items-center rounded-[calc(var(--panel-radius)-0.125rem)] border border-dashed border-white/12 bg-black/20 px-6 py-10 text-center">
                <div className="grid gap-3">
                  <Workflow className="mx-auto size-7 text-[var(--accent)]" />
                  <div>
                    <p className="text-lg font-medium text-white">No automation selected</p>
                    <p className="mt-1 text-sm text-[var(--muted-foreground)]">
                      Create one or select an existing automation to inspect its schedule.
                    </p>
                  </div>
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <AutomationDialog
        open={automationDialogOpen}
        onOpenChange={setAutomationDialogOpen}
        initial={automationDialogInitial}
        projectID={projectId}
        epics={project.data.epics}
        availableIssues={project.data.issues.items}
        onSubmit={async (body) => {
          const identifier = automationDialogInitial?.identifier;
          const issue = await saveAutomationMutation.mutateAsync({ identifier, body });
          setSelectedAutomationID(issue.identifier);
        }}
      />

      <ConfirmationDialog
        open={deleteTarget !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) {
            setDeleteTarget(null);
          }
        }}
        title={deleteTarget ? `Delete ${deleteTarget.identifier}?` : "Delete automation?"}
        description="This permanently removes the automation and its schedule from Maestro."
        confirmLabel="Delete automation"
        pendingLabel="Deleting automation..."
        isPending={deleteAutomationMutation.isPending}
        onConfirm={async () => {
          if (!deleteTarget) {
            return;
          }
          await deleteAutomationMutation.mutateAsync(deleteTarget.identifier);
          if (selectedAutomationID === deleteTarget.identifier) {
            const remaining = automations.data.items.find((item) => item.identifier !== deleteTarget.identifier);
            setSelectedAutomationID(remaining?.identifier);
          }
        }}
      />
    </div>
  );
}
