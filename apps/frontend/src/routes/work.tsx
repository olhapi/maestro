import { useDeferredValue, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, Pencil, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { PageHeader } from "@/components/dashboard/page-header";
import { IssuePreviewBoundary } from "@/components/dashboard/issue-preview-boundary";
import { WorkIssueSurface } from "@/components/dashboard/work-issue-surface";
import { IssueDialog } from "@/components/forms";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { ConfirmationDialog } from "@/components/ui/confirmation-dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { api } from "@/lib/api";
import { useIsMobileLayout } from "@/hooks/use-is-mobile-layout";
import { getStateMeta, issueStatesFor } from "@/lib/dashboard";
import { useGlobalDashboardContext } from "@/lib/global-dashboard-context";
import {
  applyIssueAssetChanges,
  summarizeIssueAssetFailures,
} from "@/lib/issue-assets";
import type { IssueDetail, IssueState, IssueSummary } from "@/lib/types";

const allProjectsValue = "__all-projects__";
const allStatesValue = "__all-states__";

function StatCard({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <Card className="bg-white/[0.04]">
      <CardContent className="pt-[var(--panel-padding)]">
        <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{label}</p>
        <p className="mt-2.5 font-display text-[length:var(--metric-value-size)] font-semibold leading-none text-white">
          {value}
        </p>
        <p className="mt-2 text-sm text-[var(--muted-foreground)]">{detail}</p>
      </CardContent>
    </Card>
  );
}

export function WorkPage() {
  const queryClient = useQueryClient();
  const isMobileLayout = useIsMobileLayout();
  const { workOverviewFilters, setWorkOverviewFilters } = useGlobalDashboardContext();
  const [search, setSearch] = useState("");
  const deferredSearch = useDeferredValue(search);
  const { projectID, sort, state } = workOverviewFilters;
  const [view, setView] = useState<"board" | "list">("board");
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<IssueDetail | undefined>();
  const [composerDefaults, setComposerDefaults] = useState<Partial<IssueDetail>>({
    state: "backlog",
  });
  const [previewIssue, setPreviewIssue] = useState<IssueSummary>();
  const [issuePendingDelete, setIssuePendingDelete] = useState<IssueSummary | null>(null);

  const issuesKey = ["issues", deferredSearch, projectID, state, sort] as const;
  const bootstrap = useQuery({ queryKey: ["work-bootstrap"], queryFn: api.workBootstrap });
  const issues = useQuery({
    queryKey: issuesKey,
    queryFn: () =>
      api.listIssues({
        search: deferredSearch,
        project_id: projectID,
        state,
        issue_type: "standard",
        sort,
        limit: 200,
      }),
  });

  const updateWorkOverviewFilters = (updates: Partial<typeof workOverviewFilters>) => {
    setWorkOverviewFilters((current) => ({ ...current, ...updates }));
  };

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["issues"] }),
      queryClient.invalidateQueries({ queryKey: ["work-bootstrap"] }),
    ]);
  };

  const stateMutation = useMutation({
    mutationFn: ({ identifier, nextState }: { identifier: string; nextState: IssueState }) =>
      api.setIssueState(identifier, nextState),
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to move issue: ${error.message}` : "Unable to move issue");
    },
    onSuccess: async () => {
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
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to delete issue: ${error.message}` : "Unable to delete issue");
    },
  });

  const metrics = useMemo(() => {
    const data = bootstrap.data?.overview.board;
    return {
      active: (data?.ready ?? 0) + (data?.in_progress ?? 0) + (data?.in_review ?? 0),
      done: data?.done ?? 0,
      backlog: data?.backlog ?? 0,
      live: bootstrap.data?.overview.snapshot.running.length ?? 0,
    };
  }, [bootstrap.data]);

  if (!bootstrap.data || !issues.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />;
  }

  const availableStates = issueStatesFor(issues.data.items);

  return (
    <div className="grid gap-[var(--section-gap)]">
      <PageHeader
        title="Coordinate work on one board"
        titleClassName="w-full"
        description={
          isMobileLayout
            ? "Review work by state, inspect execution context in-place, and open full issue pages only when you need more detail."
            : "This surface is now optimized for live triage: drag work between lanes, inspect execution context in-place, and dive into full issue pages only when needed."
        }
        descriptionClassName="max-w-none"
        stats={
          !isMobileLayout ? (
            <>
              <StatCard
                label="Active work"
                value={String(metrics.active)}
                detail="Ready, in progress, and in review across the portfolio."
              />
              <StatCard
                label="Backlog"
                value={String(metrics.backlog)}
                detail="Planned work not yet routed into execution."
              />
              <StatCard label="Completed" value={String(metrics.done)} detail="Issues already closed out successfully." />
              <StatCard
                label="Live sessions"
                value={String(metrics.live)}
                detail="Issues currently attached to a running workspace."
              />
            </>
          ) : undefined
        }
      />

      <Card>
        <CardHeader className="flex-col gap-3 lg:flex-row lg:items-center">
          <div className="grid w-full gap-2.5 lg:grid-cols-[minmax(0,1.4fr)_repeat(3,minmax(0,210px))]">
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search by identifier, title, or description"
            />
            <Select
              value={projectID || allProjectsValue}
              onValueChange={(value) => updateWorkOverviewFilters({ projectID: value === allProjectsValue ? "" : value })}
            >
              <SelectTrigger aria-label="Filter by project">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={allProjectsValue}>All projects</SelectItem>
                {bootstrap.data.projects.map((project) => (
                  <SelectItem key={project.id} value={project.id}>
                    {project.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={state || allStatesValue}
              onValueChange={(value) => updateWorkOverviewFilters({ state: value === allStatesValue ? "" : value })}
            >
              <SelectTrigger aria-label="Filter by state">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={allStatesValue}>All states</SelectItem>
                {availableStates.map((value) => (
                  <SelectItem key={value} value={value}>
                    {getStateMeta(value).label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
      </Card>

      <WorkIssueSurface
        title={isMobileLayout ? "Review work state by state" : "Triage, route, and monitor work in one surface"}
        items={issues.data.items}
        bootstrap={bootstrap.data}
        stateCounts={issues.data.counts}
        sort={sort}
        view={view}
        onSortChange={(value) => updateWorkOverviewFilters({ sort: value })}
        onViewChange={setView}
        onOpenIssue={setPreviewIssue}
        onMoveIssue={(issue, nextState) => stateMutation.mutate({ identifier: issue.identifier, nextState })}
        onCreateIssue={(nextState) => {
          setEditing(undefined);
          setComposerDefaults({
            state: nextState ?? "backlog",
            project_id: bootstrap.data?.projects[0]?.id,
          });
          setDialogOpen(true);
        }}
        renderListActions={(issue) => (
          <>
            <Button
              aria-label="Open issue preview"
              title="Open issue preview"
              type="button"
              variant="ghost"
              size="icon"
              onClick={() => setPreviewIssue(issue)}
            >
              <Eye className="size-4" />
            </Button>
            <Button
              aria-label="Edit issue"
              title="Edit issue"
              type="button"
              variant="ghost"
              size="icon"
              onClick={async () => {
                const detail = await api.getIssue(issue.identifier);
                setEditing(detail);
                setDialogOpen(true);
              }}
            >
              <Pencil className="size-4" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              type="button"
              aria-label="Delete issue"
              title="Delete issue"
              disabled={deleteMutation.isPending}
              onClick={() => setIssuePendingDelete(issue)}
            >
              <Trash2 className="size-4" />
            </Button>
          </>
        )}
      />

      <ConfirmationDialog
        open={issuePendingDelete !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) {
            setIssuePendingDelete(null);
          }
        }}
        title={issuePendingDelete ? `Delete ${issuePendingDelete.identifier}?` : "Delete issue?"}
        description="This removes the issue from Maestro, including its local workspace, activity history, and attached assets. Linked issues may also be removed."
        confirmLabel="Delete issue"
        pendingLabel="Deleting issue..."
        isPending={deleteMutation.isPending}
        onConfirm={async () => {
          if (!issuePendingDelete) {
            return;
          }
          await deleteMutation.mutateAsync(issuePendingDelete.identifier);
        }}
      />

      <IssueDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        initial={editing ?? composerDefaults}
        projects={bootstrap.data.projects}
        epics={bootstrap.data.epics}
        availableIssues={[...bootstrap.data.issues.items, ...issues.data.items]}
        onSubmit={async (body, imageChanges) => {
          if (editing) {
            const issue = await api.updateIssue(editing.identifier, body);
            const result = await applyIssueAssetChanges(issue.identifier, imageChanges);
            if (result.failures.length > 0) {
              toast.error(`Issue updated, but ${summarizeIssueAssetFailures(result)}`);
            } else {
              toast.success("Issue updated");
            }
          } else {
            const issue = await api.createIssue(body);
            const result = await applyIssueAssetChanges(issue.identifier, imageChanges);
            if (result.failures.length > 0) {
              toast.error(`Issue created, but ${summarizeIssueAssetFailures(result)}`);
            } else {
              toast.success("Issue created");
            }
          }
          await invalidate();
        }}
      />

      <IssuePreviewBoundary
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
