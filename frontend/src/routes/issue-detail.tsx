import { useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RotateCcw, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { PageHeader } from "@/components/dashboard/page-header";
import { SessionExecutionCard } from "@/components/dashboard/session-execution-card";
import { IssueDialog } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import { appRoutes } from "@/lib/routes";
import { getStateMeta, issueStatesFor } from "@/lib/dashboard";
import type { IssueState } from "@/lib/types";
import {
  formatDateTime,
  formatNumber,
  formatRelativeTime,
} from "@/lib/utils";

export function IssueDetailPage() {
  const { identifier } = useParams({ from: "/issues/$identifier" });
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [editOpen, setEditOpen] = useState(false);
  const [blockersDraft, setBlockersDraft] = useState<string | null>(null);

  const bootstrap = useQuery({
    queryKey: ["bootstrap"],
    queryFn: api.bootstrap,
  });
  const issue = useQuery({
    queryKey: ["issue", identifier],
    queryFn: () => api.getIssue(identifier),
  });
  const execution = useQuery({
    queryKey: ["issue-execution", identifier],
    queryFn: () => api.getIssueExecution(identifier),
    refetchInterval: (query) => {
      if (query.state.data?.active) {
        return 1500;
      }
      if (query.state.data?.retry_state === "scheduled") {
        return 5000;
      }
      return false;
    },
    refetchIntervalInBackground: true,
  });

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["bootstrap"] }),
      queryClient.invalidateQueries({ queryKey: ["issues"] }),
      queryClient.invalidateQueries({ queryKey: ["issue", identifier] }),
      queryClient.invalidateQueries({
        queryKey: ["issue-execution", identifier],
      }),
      queryClient.invalidateQueries({ queryKey: ["project"] }),
      queryClient.invalidateQueries({ queryKey: ["epic"] }),
    ]);
  };

  const retryMutation = useMutation({
    mutationFn: () => api.retryIssue(identifier),
    onSuccess: async () => {
      toast.success("Retry requested");
      await invalidate();
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => api.deleteIssue(identifier),
    onSuccess: async () => {
      toast.success("Issue deleted");
      await invalidate();
      void navigate({ to: appRoutes.work });
    },
  });

  if (!bootstrap.data || !issue.data || !execution.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />;
  }

  const blockersValue =
    blockersDraft ?? issue.data.blocked_by?.join(", ") ?? "";
  const availableStates = issueStatesFor(bootstrap.data.issues.items, [
    issue.data.state,
  ]);

  return (
    <div className="grid gap-5">
      <PageHeader
        eyebrow="Issue detail"
        title={issue.data.title}
        description={issue.data.description || "No description provided."}
        crumbs={[
          { label: "Work", to: appRoutes.work },
          issue.data.project_id && issue.data.project_name
            ? {
                label: issue.data.project_name,
                to: appRoutes.projectDetail,
                params: { projectId: issue.data.project_id },
              }
            : { label: "Issue" },
          issue.data.epic_id && issue.data.epic_name
            ? {
                label: issue.data.epic_name,
                to: appRoutes.epicDetail,
                params: { epicId: issue.data.epic_id },
              }
            : { label: issue.data.identifier },
          { label: issue.data.identifier },
        ]}
        actions={
          <>
            <Button variant="secondary" onClick={() => setEditOpen(true)}>
              Edit issue
            </Button>
            <Button variant="secondary" onClick={() => retryMutation.mutate()}>
              <RotateCcw className="size-4" />
              Retry now
            </Button>
            <Button
              variant="destructive"
              onClick={() => deleteMutation.mutate()}
            >
              <Trash2 className="size-4" />
              Delete
            </Button>
          </>
        }
      />

      <div className="flex flex-wrap gap-2">
        <Badge>{issue.data.identifier}</Badge>
        <Badge className="border-white/10 bg-white/5 text-white">
          {getStateMeta(issue.data.state).label}
        </Badge>
        {issue.data.project_name ? (
          <Badge className="border-white/10 bg-white/5 text-white">
            <Link
              params={{ projectId: issue.data.project_id! }}
              to={appRoutes.projectDetail}
            >
              {issue.data.project_name}
            </Link>
          </Badge>
        ) : null}
        {issue.data.epic_name ? (
          <Badge className="border-white/10 bg-white/5 text-white">
            <Link
              params={{ epicId: issue.data.epic_id! }}
              to={appRoutes.epicDetail}
            >
              {issue.data.epic_name}
            </Link>
          </Badge>
        ) : null}
      </div>

      <div className="grid gap-5 xl:grid-cols-[1.2fr_.8fr]">
        <div className="grid gap-5">
          <Card>
            <CardContent className="grid gap-4 p-5 md:grid-cols-3">
              <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  Updated
                </p>
                <p className="mt-3 text-white">
                  {formatRelativeTime(issue.data.updated_at)}
                </p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                  {formatDateTime(issue.data.updated_at)}
                </p>
              </div>
              <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  Workspace
                </p>
                <p className="mt-3 break-all text-white">
                  {issue.data.workspace_path || "Not created yet"}
                </p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                  Runs: {formatNumber(issue.data.workspace_run_count)}
                </p>
              </div>
              <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  Branch / PR
                </p>
                <p className="mt-3 text-white">
                  {issue.data.branch_name || "No branch linked"}
                </p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                  {issue.data.pr_url || "No pull request linked"}
                </p>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Description</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="whitespace-pre-wrap text-sm leading-7 text-[var(--muted-foreground)]">
                {issue.data.description || "No description provided."}
              </p>
            </CardContent>
          </Card>
        </div>

        <div className="grid gap-5">
          <Card>
            <CardHeader>
              <CardTitle>Live adjustments</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="grid gap-2">
                <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  State
                </span>
                <Select
                  value={issue.data.state}
                  onChange={async (event) => {
                    await api.setIssueState(
                      identifier,
                      event.target.value as IssueState,
                    );
                    toast.success("State updated");
                    await invalidate();
                  }}
                >
                  {availableStates.map((value) => (
                    <option key={value} value={value}>
                      {getStateMeta(value).label}
                    </option>
                  ))}
                </Select>
              </div>

              <div className="grid gap-2">
                <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  Blockers
                </span>
                <Textarea
                  value={blockersValue}
                  onChange={(event) => setBlockersDraft(event.target.value)}
                  className="min-h-[120px]"
                />
                <Button
                  variant="secondary"
                  onClick={async () => {
                    await api.setIssueBlockers(
                      identifier,
                      blockersValue
                        .split(",")
                        .map((value) => value.trim())
                        .filter(Boolean),
                    );
                    toast.success("Blockers updated");
                    setBlockersDraft(null);
                    await invalidate();
                  }}
                >
                  <Save className="size-4" />
                  Save blockers
                </Button>
              </div>
            </CardContent>
          </Card>

          <SessionExecutionCard
            execution={execution.data}
            issueTotalTokens={issue.data.total_tokens_spent}
          />
        </div>
      </div>

      <IssueDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        initial={issue.data}
        projects={bootstrap.data.projects}
        epics={bootstrap.data.epics}
        onSubmit={async (body) => {
          await api.updateIssue(identifier, body);
          toast.success("Issue updated");
          await invalidate();
        }}
      />
    </div>
  );
}
