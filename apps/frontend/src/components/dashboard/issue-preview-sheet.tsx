import { useEffect, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import {
  AlertTriangle,
  ExternalLink,
  GitBranch,
  Pencil,
  RotateCcw,
  Save,
  Trash2,
  Workflow,
} from "lucide-react";
import { toast } from "sonner";

import { IssueDialog } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmationDialog } from "@/components/ui/confirmation-dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import {
  getPausedForIssue,
  getRetryForIssue,
  getSessionForIssue,
  getStateMeta,
  issueStatesFor,
} from "@/lib/dashboard";
import { describeFailureRuns } from "@/lib/execution";
import {
  applyIssueImageChanges,
  summarizeIssueImageFailures,
} from "@/lib/issue-images";
import { appRoutes } from "@/lib/routes";
import type {
  BootstrapResponse,
  IssueDetail,
  IssueState,
  IssueSummary,
} from "@/lib/types";
import { formatCompactNumber, formatDateTime, formatNumber, formatRelativeTime } from "@/lib/utils";

export function IssuePreviewSheet({
  issue,
  bootstrap,
  open,
  onOpenChange,
  onInvalidate,
  onDelete,
  onStateChange,
}: {
  issue?: IssueSummary;
  bootstrap?: BootstrapResponse;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onInvalidate: () => Promise<void>;
  onDelete?: (identifier: string) => Promise<void>;
  onStateChange?: (identifier: string, state: IssueState) => Promise<void>;
}) {
  const [detail, setDetail] = useState<IssueDetail>();
  const [blockersDraft, setBlockersDraft] = useState<{
    identifier?: string;
    value: string;
  }>({ value: "" });
  const [editOpen, setEditOpen] = useState(false);
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const navigate = useNavigate();
  const issueIdentifier = issue?.identifier;
  const currentIssueIdentifierRef = useRef<string | undefined>(issueIdentifier);

  useEffect(() => {
    currentIssueIdentifierRef.current = issueIdentifier;
  }, [issueIdentifier]);

  useEffect(() => {
    if (!issueIdentifier || !open) {
      return;
    }

    let active = true;
    void api.getIssue(issueIdentifier)
      .then((next) => {
        if (!active || currentIssueIdentifierRef.current !== next.identifier) {
          return;
        }
        setDetail(next);
        setBlockersDraft({
          identifier: next.identifier,
          value: next.blocked_by?.join(", ") ?? "",
        });
      })
      .catch(() => undefined);

    return () => {
      active = false;
    };
  }, [issueIdentifier, open]);

  const activeDetail = detail && detail.identifier === issueIdentifier ? detail : undefined;
  const activeIssue = activeDetail ?? issue;
  const blockersValue =
    blockersDraft.identifier === activeIssue?.identifier
      ? blockersDraft.value
      : activeIssue?.blocked_by?.join(", ") ?? "";
  const session = activeIssue
    ? getSessionForIssue(bootstrap, activeIssue.id, activeIssue.identifier)
    : undefined;
  const retry = activeIssue
    ? getRetryForIssue(bootstrap, activeIssue.id, activeIssue.identifier)
    : undefined;
  const paused = activeIssue
    ? getPausedForIssue(bootstrap, activeIssue.id, activeIssue.identifier)
    : undefined;
  const availableStates = activeIssue
    ? issueStatesFor(bootstrap?.issues.items ?? [activeIssue], [
        activeIssue.state,
      ])
    : [];

  if (!activeIssue) return null;

  return (
    <>
      <Sheet
        open={open}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) {
            setDeleteDialogOpen(false);
          }
          onOpenChange(nextOpen);
        }}
      >
        <SheetContent className="w-[min(580px,calc(100vw-24px))]">
          <SheetHeader>
            <div className="flex items-start justify-between gap-4 pr-10">
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <Badge>{activeIssue.identifier}</Badge>
                  <Badge className="border-white/12 bg-white/5 text-white">
                    {getStateMeta(activeIssue.state).label}
                  </Badge>
                  {paused ? (
                    <Badge className="border-rose-400/20 bg-rose-400/10 text-rose-100">
                      Paused
                    </Badge>
                  ) : null}
                  {activeIssue.issue_type === "recurring" ? (
                    <Badge className="border-cyan-400/20 bg-cyan-400/10 text-cyan-100">
                      Recurring
                    </Badge>
                  ) : null}
                  {activeIssue.project_name ? (
                    <Badge className="border-white/12 bg-white/5 text-white">
                      {activeIssue.project_name}
                    </Badge>
                  ) : null}
                  {activeIssue.epic_name ? (
                    <Badge className="border-white/12 bg-white/5 text-white">
                      {activeIssue.epic_name}
                    </Badge>
                  ) : null}
                </div>
                <SheetTitle className="mt-4 text-2xl">
                  {activeIssue.title}
                </SheetTitle>
                <SheetDescription>
                  Updated {formatRelativeTime(activeIssue.updated_at)} ·
                  Priority {activeIssue.priority}
                </SheetDescription>
              </div>
            </div>
          </SheetHeader>

          <div className="flex-1 space-y-4 overflow-y-auto px-5 py-4">
            <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.04] p-3.5">
              <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                Description
              </p>
              <p className="mt-3 whitespace-pre-wrap text-sm leading-6 text-[var(--muted-foreground)]">
                {activeIssue.description || "No description provided."}
              </p>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.04] p-3.5">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  Workspace
                </p>
                <p className="mt-3 break-all text-sm text-white">
                  {activeIssue.workspace_path || "Not created yet"}
                </p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                  {formatNumber(activeIssue.workspace_run_count)} runs
                </p>
              </div>
              <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.04] p-3.5">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  Execution
                </p>
                <div className="mt-3 grid gap-2 text-sm text-[var(--muted-foreground)]">
                  <span className="inline-flex items-center gap-2 text-white">
                    <Workflow className="size-4 text-lime-300" />
                    {session
                      ? session.last_event || "Live session"
                      : "No live session"}
                  </span>
                  <span>
                    {formatCompactNumber(activeIssue.total_tokens_spent)} lifetime
                    tokens
                  </span>
                  {retry ? (
                    <span>Retry at {formatDateTime(retry.due_at)}</span>
                  ) : (
                    <span>No retry scheduled</span>
                  )}
                  {paused ? (
                    <span className="inline-flex items-center gap-2 text-rose-100">
                      <AlertTriangle className="size-4 text-rose-300" />
                      Auto-retries paused after {describeFailureRuns(paused.consecutive_failures, paused.error)}
                    </span>
                  ) : null}
                  {activeIssue.issue_type === "recurring" ? (
                    <span>
                      {activeIssue.next_run_at
                        ? `Next scheduled run ${formatDateTime(activeIssue.next_run_at)}`
                        : activeIssue.enabled === false
                          ? "Recurring schedule disabled"
                          : "Recurring schedule ready"}
                    </span>
                  ) : null}
                </div>
              </div>
            </div>

            <div className="grid gap-3.5 rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.04] p-3.5">
              <div className="grid gap-2">
                <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  State
                </span>
                <Select
                  value={activeIssue.state}
                  onValueChange={async (value) => {
                    if (!onStateChange) return;
                    await onStateChange(
                      activeIssue.identifier,
                      value as IssueState,
                    );
                    const next = await api.getIssue(activeIssue.identifier);
                    if (currentIssueIdentifierRef.current === next.identifier) {
                      setDetail(next);
                      setBlockersDraft({
                        identifier: next.identifier,
                        value: next.blocked_by?.join(", ") ?? "",
                      });
                    }
                  }}
                >
                  <SelectTrigger aria-label="State">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {availableStates.map((state) => (
                      <SelectItem key={state} value={state}>
                        {getStateMeta(state).label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>

              <div className="grid gap-2">
                <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  Blockers
                </span>
                <Textarea
                  value={blockersValue}
                  onChange={(event) =>
                    setBlockersDraft({
                      identifier: activeIssue.identifier,
                      value: event.target.value,
                    })
                  }
                  className="min-h-[96px]"
                />
                <Button
                  variant="secondary"
                  className="justify-center"
                  onClick={async () => {
                    await api.setIssueBlockers(
                      activeIssue.identifier,
                      blockersValue
                        .split(",")
                        .map((value) => value.trim())
                        .filter(Boolean),
                    );
                    toast.success("Blockers updated");
                    await onInvalidate();
                    const next = await api.getIssue(activeIssue.identifier);
                    if (currentIssueIdentifierRef.current === next.identifier) {
                      setDetail(next);
                      setBlockersDraft({
                        identifier: next.identifier,
                        value: next.blocked_by?.join(", ") ?? "",
                      });
                    }
                  }}
                >
                  <Save className="size-4" />
                  Save blockers
                </Button>
              </div>
            </div>

            <div className="grid gap-3 rounded-[1.5rem] border border-white/8 bg-white/[0.04] p-4 text-sm">
              <div className="flex items-center gap-2 text-white">
                <GitBranch className="size-4 text-[var(--accent)]" />
                {activeIssue.branch_name || "No branch linked"}
              </div>
              <div className="text-[var(--muted-foreground)]">
                {activeIssue.pr_url || "No pull request linked"}
              </div>
              {activeIssue.pr_url ? (
                <a
                  className="inline-flex items-center gap-2 text-sm text-[var(--accent)]"
                  href={activeIssue.pr_url}
                  rel="noreferrer"
                  target="_blank"
                >
                  Open PR
                  <ExternalLink className="size-4" />
                </a>
              ) : null}
            </div>
          </div>

          <SheetFooter className="grid gap-3">
            <div
              className={onDelete ? "grid grid-cols-3 gap-3" : "grid grid-cols-2 gap-3"}
              data-testid="issue-preview-actions-row"
            >
              <Button
                variant="secondary"
                className="h-auto min-h-10 px-2 py-2 text-xs leading-tight sm:px-3 sm:text-sm"
                onClick={() => setEditOpen(true)}
              >
                <Pencil className="size-4" />
                Edit issue
              </Button>
              <Button
                variant="secondary"
                className="h-auto min-h-10 px-2 py-2 text-xs leading-tight sm:px-3 sm:text-sm"
                onClick={() =>
                  void api.retryIssue(activeIssue.identifier).then(onInvalidate)
                }
              >
                <RotateCcw className="size-4" />
                Retry now
              </Button>
              {onDelete ? (
                <Button
                  variant="destructive"
                  className="h-auto min-h-10 px-2 py-2 text-xs leading-tight sm:px-3 sm:text-sm"
                  onClick={() => setDeleteDialogOpen(true)}
                >
                  <Trash2 className="size-4" />
                  Delete
                </Button>
              ) : null}
            </div>
            <div className="flex flex-wrap justify-end gap-3">
              {activeIssue.issue_type === "recurring" ? (
                <Button
                  variant="secondary"
                  onClick={() =>
                    void api.runIssueNow(activeIssue.identifier).then(onInvalidate)
                  }
                >
                  <RotateCcw className="size-4" />
                  Run now
                </Button>
              ) : null}
              <Button
                variant="secondary"
                onClick={() => {
                  onOpenChange(false);
                  void navigate({
                    to: appRoutes.issueDetail,
                    params: { identifier: activeIssue.identifier },
                  });
                }}
              >
                Full page
              </Button>
            </div>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {bootstrap ? (
        <IssueDialog
          open={editOpen}
          onOpenChange={setEditOpen}
          initial={activeDetail ?? activeIssue}
          projects={bootstrap.projects}
          epics={bootstrap.epics}
          availableIssues={bootstrap.issues.items}
          onSubmit={async (body, imageChanges) => {
            const issue = await api.updateIssue(activeIssue.identifier, body);
            const result = await applyIssueImageChanges(
              issue.identifier,
              imageChanges,
            );
            if (result.failures.length > 0) {
              toast.error(
                `Issue updated, but ${summarizeIssueImageFailures(result)}`,
              );
            } else {
              toast.success("Issue updated");
            }
            await onInvalidate();
            const next = await api.getIssue(activeIssue.identifier);
            if (currentIssueIdentifierRef.current === next.identifier) {
              setDetail(next);
              setBlockersDraft({
                identifier: next.identifier,
                value: next.blocked_by?.join(", ") ?? "",
              });
            }
          }}
        />
      ) : null}

      {onDelete ? (
        <ConfirmationDialog
          open={deleteDialogOpen}
          onOpenChange={setDeleteDialogOpen}
          title={`Delete ${activeIssue.identifier}?`}
          description="This removes the issue from Maestro, including its local workspace, activity history, and attached images. Linked provider items may also be removed."
          confirmLabel="Delete issue"
          pendingLabel="Deleting issue..."
          onConfirm={async () => {
            await onDelete(activeIssue.identifier);
            onOpenChange(false);
          }}
        />
      ) : null}
    </>
  );
}
