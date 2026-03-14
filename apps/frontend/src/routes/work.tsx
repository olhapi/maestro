import { useDeferredValue, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { LayoutGrid, List, Pencil, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { KanbanBoard } from "@/components/dashboard/kanban-board";
import { PageHeader } from "@/components/dashboard/page-header";
import { IssuePreviewSheet } from "@/components/dashboard/issue-preview-sheet";
import { IssueDialog } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { api } from "@/lib/api";
import { useIsMobileLayout } from "@/hooks/use-is-mobile-layout";
import { getStateMeta, issueStatesFor } from "@/lib/dashboard";
import {
  applyIssueImageChanges,
  summarizeIssueImageFailures,
} from "@/lib/issue-images";
import { appRoutes } from "@/lib/routes";
import type { BootstrapResponse, IssueDetail, IssueState, IssueSummary } from "@/lib/types";
import { formatRelativeTime } from "@/lib/utils";

const allProjectsValue = "__all-projects__";
const allStatesValue = "__all-states__";
const allTypesValue = "__all-types__";

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
  const [search, setSearch] = useState("");
  const deferredSearch = useDeferredValue(search);
  const [projectID, setProjectID] = useState("");
  const [state, setState] = useState("");
  const [issueType, setIssueType] = useState("");
  const [sort, setSort] = useState("priority_asc");
  const [view, setView] = useState<"board" | "list">("board");
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<IssueDetail | undefined>();
  const [composerDefaults, setComposerDefaults] = useState<Partial<IssueDetail>>({
    state: "backlog",
  });
  const [previewIssue, setPreviewIssue] = useState<IssueSummary>();

  const issuesKey = ["issues", deferredSearch, projectID, state, issueType, sort] as const;
  const bootstrap = useQuery({ queryKey: ["bootstrap"], queryFn: api.bootstrap });
  const issues = useQuery({
    queryKey: issuesKey,
    queryFn: () => api.listIssues({ search: deferredSearch, project_id: projectID, state, issue_type: issueType, sort, limit: 200 }),
  });

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["issues"] }),
      queryClient.invalidateQueries({ queryKey: ["bootstrap"] }),
    ]);
  };

  const patchIssueState = (payload: { identifier: string; nextState: IssueState }) => {
    const cached = queryClient.getQueryData<{ items: IssueSummary[]; total: number; limit: number; offset: number }>(
      issuesKey,
    );
    const nextItems = cached?.items.map((item) =>
      item.identifier === payload.identifier
        ? { ...item, state: payload.nextState, updated_at: new Date().toISOString() }
        : item,
    );
    if (cached && nextItems) {
      queryClient.setQueryData(issuesKey, { ...cached, items: nextItems });
    }
    const cachedBootstrap = queryClient.getQueryData<BootstrapResponse>(["bootstrap"]);
    if (cachedBootstrap) {
      queryClient.setQueryData(["bootstrap"], {
        ...cachedBootstrap,
        issues: {
          ...cachedBootstrap.issues,
          items: cachedBootstrap.issues.items.map((item) =>
            item.identifier === payload.identifier
              ? { ...item, state: payload.nextState, updated_at: new Date().toISOString() }
              : item,
          ),
        },
      });
    }
    return { cached, cachedBootstrap };
  };

  const stateMutation = useMutation({
    mutationFn: ({ identifier, nextState }: { identifier: string; nextState: IssueState }) =>
      api.setIssueState(identifier, nextState),
    onMutate: async (payload) => patchIssueState(payload),
    onError: (_error, _vars, context) => {
      if (context?.cached) queryClient.setQueryData(issuesKey, context.cached);
      if (context?.cachedBootstrap) queryClient.setQueryData(["bootstrap"], context.cachedBootstrap);
      toast.error("Unable to move issue");
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
  const showBoardView = isMobileLayout || view === "board";

  return (
    <div className="grid gap-[var(--section-gap)]">
      <PageHeader
        title="Coordinate work on one board"
        description={
          isMobileLayout
            ? "Review work by state, inspect execution context in-place, and open full issue pages only when you need more detail."
            : "This surface is now optimized for live triage: drag work between lanes, inspect execution context in-place, and dive into full issue pages only when needed."
        }
        stats={
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
            <Select value={projectID || allProjectsValue} onValueChange={(value) => setProjectID(value === allProjectsValue ? "" : value)}>
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
            <Select value={state || allStatesValue} onValueChange={(value) => setState(value === allStatesValue ? "" : value)}>
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
            <Select value={issueType || allTypesValue} onValueChange={(value) => setIssueType(value === allTypesValue ? "" : value)}>
              <SelectTrigger aria-label="Filter by issue type">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={allTypesValue}>All types</SelectItem>
                <SelectItem value="standard">Standard</SelectItem>
                <SelectItem value="recurring">Recurring</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
      </Card>

      <Card className="bg-white/[0.04]">
        <CardHeader className="flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <h2 className="text-lg font-semibold text-white">
            {isMobileLayout ? "Review work state by state" : "Triage, route, and monitor work in one surface"}
          </h2>
          <div className="flex w-full flex-col gap-2 sm:w-auto sm:flex-row sm:items-center">
            <Select value={sort} onValueChange={setSort}>
              <SelectTrigger
                aria-label="Sort issues"
                className={isMobileLayout ? "h-9 w-full text-xs" : "h-9 w-[176px] text-xs"}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="updated_desc">Recently updated</SelectItem>
                <SelectItem value="priority_asc">Highest priority</SelectItem>
                <SelectItem value="identifier_asc">Identifier A-Z</SelectItem>
                <SelectItem value="state_asc">State grouping</SelectItem>
              </SelectContent>
            </Select>
            {!isMobileLayout ? (
              <ToggleGroup
                aria-label="Switch work view"
                className="inline-flex rounded-[var(--panel-radius)] border border-white/10 bg-black/20 p-0.75"
                type="single"
                value={view}
                onValueChange={(next) => {
                  if (next === "board" || next === "list") {
                    setView(next);
                  }
                }}
              >
                <ToggleGroupItem aria-label="Board view" className="px-2.5 py-1.5" title="Board view" value="board">
                  <LayoutGrid className="size-4" />
                </ToggleGroupItem>
                <ToggleGroupItem aria-label="List view" className="px-2.5 py-1.5" title="List view" value="list">
                  <List className="size-4" />
                </ToggleGroupItem>
              </ToggleGroup>
            ) : null}
          </div>
        </CardHeader>
      </Card>

      {showBoardView ? (
        <div className="m-0">
          <KanbanBoard
            items={issues.data.items}
            bootstrap={bootstrap.data}
            mode={isMobileLayout ? "grouped" : "board"}
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
          />
        </div>
      ) : (
        <div className="m-0">
          <Card>
            <CardContent className="overflow-x-auto pt-[var(--panel-padding)]">
              <table className="w-full min-w-[960px] text-left text-sm">
                <thead className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  <tr>
                    <th className="pb-4">Issue</th>
                    <th className="pb-4">State</th>
                    <th className="pb-4">Project</th>
                    <th className="pb-4">Epic</th>
                    <th className="pb-4">Updated</th>
                    <th className="pb-4 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {issues.data.items.map((issue) => (
                    <tr key={issue.id} className="border-t border-white/6">
                      <td className="py-4">
                        <button className="text-left" onClick={() => setPreviewIssue(issue)}>
                          <p className="font-medium text-white">{issue.identifier}</p>
                          <p className="max-w-[420px] text-sm text-[var(--muted-foreground)]">{issue.title}</p>
                        </button>
                      </td>
                      <td className="py-4">
                        <Badge className="border-white/10 bg-white/5 text-white">
                          {getStateMeta(issue.state).label}
                        </Badge>
                      </td>
                      <td className="py-4 text-[var(--muted-foreground)]">
                        {issue.project_id ? (
                          <Link params={{ projectId: issue.project_id }} to={appRoutes.projectDetail}>
                            {issue.project_name || "Unassigned"}
                          </Link>
                        ) : (
                          "Unassigned"
                        )}
                      </td>
                      <td className="py-4 text-[var(--muted-foreground)]">
                        {issue.epic_id ? (
                          <Link params={{ epicId: issue.epic_id }} to={appRoutes.epicDetail}>
                            {issue.epic_name || "None"}
                          </Link>
                        ) : (
                          "None"
                        )}
                      </td>
                      <td className="py-4 text-[var(--muted-foreground)]">{formatRelativeTime(issue.updated_at)}</td>
                      <td className="py-4">
                        <div className="flex justify-end gap-2">
                          <Button variant="ghost" size="icon" onClick={() => setPreviewIssue(issue)}>
                            <List className="size-4" />
                          </Button>
                          <Button
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
                          <Button variant="ghost" size="icon" onClick={() => deleteMutation.mutate(issue.identifier)}>
                            <Trash2 className="size-4" />
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </CardContent>
          </Card>
        </div>
      )}

      <IssueDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        initial={editing ?? composerDefaults}
        projects={bootstrap.data.projects}
        epics={bootstrap.data.epics}
        onSubmit={async (body, imageChanges) => {
          if (editing) {
            const issue = await api.updateIssue(editing.identifier, body);
            const result = await applyIssueImageChanges(issue.identifier, imageChanges);
            if (result.failures.length > 0) {
              toast.error(`Issue updated, but ${summarizeIssueImageFailures(result)}`);
            } else {
              toast.success("Issue updated");
            }
          } else {
            const issue = await api.createIssue(body);
            const result = await applyIssueImageChanges(issue.identifier, imageChanges);
            if (result.failures.length > 0) {
              toast.error(`Issue created, but ${summarizeIssueImageFailures(result)}`);
            } else {
              toast.success("Issue created");
            }
          }
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
