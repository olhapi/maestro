import { useRef, useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, RotateCcw, Send, Trash2, Upload } from "lucide-react";
import { toast } from "sonner";

import { PageHeader } from "@/components/dashboard/page-header";
import { SessionExecutionCard } from "@/components/dashboard/session-execution-card";
import { IssueDialog } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ConfirmationDialog } from "@/components/ui/confirmation-dialog";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import { appRoutes } from "@/lib/routes";
import {
  applyIssueImageChanges,
  formatIssueImageSize,
  issueImageContentURL,
  issueImageInputAccept,
  summarizeIssueImageFailures,
} from "@/lib/issue-images";
import { getStateMeta, issueStatesFor } from "@/lib/dashboard";
import type { AgentCommand, IssueState } from "@/lib/types";
import { formatDateTime, formatNumber, formatRelativeTime } from "@/lib/utils";

function commandStatusMeta(status: AgentCommand["status"]) {
  switch (status) {
    case "delivered":
      return {
        label: "Delivered",
        className: "border-emerald-400/20 bg-emerald-400/10 text-emerald-100",
      };
    case "waiting_for_unblock":
      return {
        label: "Waiting for unblock",
        className: "border-amber-400/20 bg-amber-400/10 text-amber-100",
      };
    default:
      return {
        label: "Pending",
        className: "border-white/10 bg-white/5 text-white",
      };
  }
}

function AgentCommandEntry({ command }: { command: AgentCommand }) {
  const status = commandStatusMeta(command.status);

  return (
    <div className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/8 bg-black/20 p-3">
      <div className="flex items-start justify-between gap-3">
        <Badge className={status.className}>{status.label}</Badge>
        <span className="text-xs text-[var(--muted-foreground)]">{formatRelativeTime(command.created_at)}</span>
      </div>
      <p className="mt-3 whitespace-pre-wrap break-words text-sm text-white">{command.command}</p>
      <p className="mt-3 text-xs text-[var(--muted-foreground)]">Sent {formatDateTime(command.created_at)}</p>
      {command.delivered_at ? (
        <p className="mt-1 text-xs text-[var(--muted-foreground)]">
          Delivered {formatDateTime(command.delivered_at)}
          {command.delivery_mode ? ` via ${command.delivery_mode}` : ""}
          {command.delivery_thread_id ? ` on ${command.delivery_thread_id}` : ""}
        </p>
      ) : null}
    </div>
  );
}

export function IssueDetailPage() {
  const { identifier } = useParams({ from: "/issues/$identifier" });
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [editOpen, setEditOpen] = useState(false);
  const [commandDraft, setCommandDraft] = useState("");
  const [previewImageID, setPreviewImageID] = useState<string | null>(null);
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [deleteImageConfirmOpen, setDeleteImageConfirmOpen] = useState(false);
  const imageInputRef = useRef<HTMLInputElement>(null);

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
  const runNowMutation = useMutation({
    mutationFn: () => api.runIssueNow(identifier),
    onSuccess: async () => {
      toast.success("Recurring issue queued");
      await invalidate();
    },
  });

  const commandMutation = useMutation({
    mutationFn: () => api.sendIssueCommand(identifier, commandDraft.trim()),
    onSuccess: async () => {
      toast.success("Command queued for agent");
      setCommandDraft("");
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
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to delete issue: ${error.message}` : "Unable to delete issue");
    },
  });
  const uploadImagesMutation = useMutation({
    mutationFn: (files: File[]) =>
      applyIssueImageChanges(identifier, {
        newImages: files,
        removeImageIDs: [],
      }),
    onSuccess: async (result, files) => {
      if (result.failures.length > 0) {
        toast.error(`Upload finished with errors: ${summarizeIssueImageFailures(result)}`);
      } else {
        toast.success(files.length === 1 ? "Image attached" : `${files.length} images attached`);
      }
      await invalidate();
    },
  });
  const deleteImageMutation = useMutation({
    mutationFn: (imageID: string) =>
      applyIssueImageChanges(identifier, {
        newImages: [],
        removeImageIDs: [imageID],
      }),
    onSuccess: async (result) => {
      if (result.failures.length > 0) {
        toast.error(`Unable to remove image: ${summarizeIssueImageFailures(result)}`);
      } else {
        setPreviewImageID(null);
        toast.success("Image removed");
      }
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to remove image: ${error.message}` : "Unable to remove image");
    },
  });

  if (!bootstrap.data || !issue.data || !execution.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />;
  }

  const availableStates = issueStatesFor(bootstrap.data.issues.items, [issue.data.state]);
  const previewImage = issue.data.images.find((image) => image.id === previewImageID) ?? null;
  const crumbs: Array<{
    label: string;
    to?: string;
    params?: Record<string, string>;
  }> = [
    { label: "Work", to: appRoutes.work },
    issue.data.project_id && issue.data.project_name
      ? {
          label: issue.data.project_name,
          to: appRoutes.projectDetail,
          params: { projectId: issue.data.project_id },
        }
      : { label: "Issue" },
    ...(issue.data.epic_id && issue.data.epic_name
      ? [
          {
            label: issue.data.epic_name,
            to: appRoutes.epicDetail,
            params: { epicId: issue.data.epic_id },
          },
        ]
      : []),
    { label: issue.data.identifier },
  ];

  return (
    <div className="grid gap-[var(--section-gap)]">
      <PageHeader
        title={issue.data.title}
        crumbs={crumbs}
      />

      <div className="flex flex-wrap gap-2">
        <Badge>{issue.data.identifier}</Badge>
        <Badge className="border-white/10 bg-white/5 text-white">{getStateMeta(issue.data.state).label}</Badge>
        {issue.data.issue_type === "recurring" ? (
          <Badge className="border-cyan-400/20 bg-cyan-400/10 text-cyan-100">Recurring</Badge>
        ) : null}
        {issue.data.project_name ? (
          <Badge className="border-white/10 bg-white/5 text-white">
            <Link params={{ projectId: issue.data.project_id! }} to={appRoutes.projectDetail}>
              {issue.data.project_name}
            </Link>
          </Badge>
        ) : null}
        {issue.data.epic_name ? (
          <Badge className="border-white/10 bg-white/5 text-white">
            <Link params={{ epicId: issue.data.epic_id! }} to={appRoutes.epicDetail}>
              {issue.data.epic_name}
            </Link>
          </Badge>
        ) : null}
      </div>

      <div className="grid items-start gap-[var(--section-gap)] xl:grid-cols-[minmax(0,1fr)_360px]">
        <div className="grid min-w-0 gap-[var(--section-gap)]" data-testid="issue-main-column">
          <Card>
            <CardContent className="grid gap-3 pt-[var(--panel-padding)] sm:grid-cols-2 xl:grid-cols-3">
              <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 px-3.5 py-3">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Updated</p>
                <p className="mt-3 text-white">{formatRelativeTime(issue.data.updated_at)}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">{formatDateTime(issue.data.updated_at)}</p>
              </div>
              <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 px-3.5 py-3">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Workspace</p>
                <p className="mt-3 break-all text-white">{issue.data.workspace_path || "Not created yet"}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                  Runs: {formatNumber(issue.data.workspace_run_count)}
                </p>
              </div>
              <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 px-3.5 py-3">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Branch / PR</p>
                <p className="mt-3 text-white">{issue.data.branch_name || "No branch linked"}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                  {issue.data.pr_url || "No pull request linked"}
                </p>
              </div>
              {issue.data.issue_type === "recurring" ? (
                <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-cyan-400/10 bg-cyan-400/[0.04] px-3.5 py-3">
                  <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Schedule</p>
                  <p className="mt-3 text-white">
                    {issue.data.enabled === false ? "Disabled" : issue.data.cron || "Recurring"}
                  </p>
                  <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                    {issue.data.next_run_at ? formatDateTime(issue.data.next_run_at) : "No next run scheduled"}
                  </p>
                </div>
              ) : null}
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-2.5">
              <CardTitle>Description</CardTitle>
            </CardHeader>
            <CardContent className="pt-0 pb-3.5">
              <p className="whitespace-pre-wrap text-sm leading-6 text-[var(--muted-foreground)]">
                {issue.data.description || "No description provided."}
              </p>
            </CardContent>
          </Card>

          <Card data-testid="issue-images-card">
            <CardHeader className="flex-col gap-3 pb-2.5 sm:flex-row sm:items-start sm:justify-between">
              <div className="min-w-0">
                <CardTitle>Images</CardTitle>
                <p className="mt-1 text-sm text-[var(--muted-foreground)]">
                  Screenshots stay local to this Maestro database and are served through the issue API.
                </p>
              </div>
              <div className="flex w-full shrink-0 sm:w-auto sm:justify-end">
                <input
                  ref={imageInputRef}
                  aria-label="Attach images"
                  className="sr-only"
                  accept={issueImageInputAccept}
                  disabled={uploadImagesMutation.isPending}
                  multiple
                  type="file"
                  onChange={(event) => {
                    const files = Array.from(event.currentTarget.files ?? []);
                    if (files.length > 0) {
                      uploadImagesMutation.mutate(files);
                    }
                    event.currentTarget.value = "";
                  }}
                />
                <Button
                  type="button"
                  className="w-full sm:w-auto"
                  aria-label="Attach images"
                  disabled={uploadImagesMutation.isPending}
                  onClick={() => imageInputRef.current?.click()}
                >
                  <Upload className="size-4" />
                  {uploadImagesMutation.isPending ? "Uploading..." : "Attach"}
                </Button>
              </div>
            </CardHeader>
            <CardContent className="grid gap-4 pt-0">
              {issue.data.images.length === 0 ? (
                <div className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 px-4 py-4">
                  <p className="text-sm font-medium text-white">No images attached yet</p>
                  <p className="mt-1 text-sm text-[var(--muted-foreground)]">
                    Add screenshots, mocks, or bug captures here without storing them outside the local Maestro asset
                    root.
                  </p>
                </div>
              ) : (
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                  {issue.data.images.map((image) => (
                    <button
                      key={image.id}
                      type="button"
                      aria-label={`Open ${image.filename}`}
                      className="group overflow-hidden rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 text-left transition hover:border-white/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)]"
                      onClick={() => setPreviewImageID(image.id)}
                    >
                      <img
                        alt={image.filename}
                        className="aspect-square w-full bg-black object-cover transition duration-300 group-hover:scale-[1.02]"
                        loading="lazy"
                        src={issueImageContentURL(identifier, image.id)}
                      />
                    </button>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>

          <SessionExecutionCard execution={execution.data} issueTotalTokens={issue.data.total_tokens_spent} />
        </div>

        <div className="grid content-start gap-[var(--section-gap)]" data-testid="issue-control-rail">
          <Card>
            <CardHeader>
              <CardTitle>Issue actions</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-2.5">
              <Select
                value={issue.data.state}
                onValueChange={async (value) => {
                  await api.setIssueState(identifier, value as IssueState);
                  toast.success("State updated");
                  await invalidate();
                }}
              >
                <SelectTrigger aria-label="Issue state">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {availableStates.map((value) => (
                    <SelectItem key={value} value={value}>
                      {getStateMeta(value).label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <div className="grid grid-cols-3 gap-2.5" data-testid="issue-actions-row">
                <Button
                  variant="secondary"
                  size="icon"
                  className="h-12 w-full"
                  onClick={() => setEditOpen(true)}
                  aria-label="Edit issue"
                  title="Edit issue"
                >
                  <Pencil className="size-4" />
                </Button>
                <Button
                  variant="secondary"
                  size="icon"
                  className="h-12 w-full"
                  onClick={() => retryMutation.mutate()}
                  disabled={retryMutation.isPending}
                  aria-label="Retry now"
                  title="Retry now"
                >
                  <RotateCcw className="size-4" />
                </Button>
                <Button
                  variant="destructive"
                  size="icon"
                  className="h-12 w-full"
                  onClick={() => setDeleteConfirmOpen(true)}
                  disabled={deleteMutation.isPending}
                  aria-label="Delete"
                  title="Delete"
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
              {issue.data.issue_type === "recurring" ? (
                <Button variant="secondary" onClick={() => runNowMutation.mutate()} disabled={runNowMutation.isPending}>
                  <RotateCcw className="size-4" />
                  Run now
                </Button>
              ) : null}
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-2.5">
              <div className="flex items-center justify-between gap-3">
                <CardTitle>Agent commands</CardTitle>
                <span className="text-xs text-[var(--muted-foreground)]">
                  {execution.data.agent_commands.length} total
                </span>
              </div>
            </CardHeader>
            <CardContent className="space-y-4 pt-0">
              <div className="relative">
                <Textarea
                  value={commandDraft}
                  onChange={(event) => setCommandDraft(event.target.value)}
                  className="min-h-[140px] resize-none pb-16 pr-16"
                  placeholder="Tell the agent what it missed or what it should do next."
                />
                <Button
                  type="button"
                  size="icon"
                  className="absolute bottom-4 right-3"
                  onClick={() => commandMutation.mutate()}
                  disabled={!commandDraft.trim() || commandMutation.isPending}
                  aria-label="Send to agent"
                  title="Send to agent"
                >
                  <Send className="size-4 shrink-0" />
                </Button>
              </div>
              <div className="space-y-2.5 border-t border-white/8 pt-4">
                {execution.data.agent_commands.length === 0 ? (
                  <p className="text-sm text-[var(--muted-foreground)]">No follow-up commands sent yet.</p>
                ) : (
                  execution.data.agent_commands.map((command) => (
                    <AgentCommandEntry key={command.id} command={command} />
                  ))
                )}
              </div>
            </CardContent>
          </Card>
        </div>
      </div>

      <IssueDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        initial={issue.data}
        projects={bootstrap.data.projects}
        epics={bootstrap.data.epics}
        availableIssues={bootstrap.data.issues.items}
        onSubmit={async (body, imageChanges) => {
          const updated = await api.updateIssue(identifier, body);
          const result = await applyIssueImageChanges(updated.identifier, imageChanges);
          if (result.failures.length > 0) {
            toast.error(`Issue updated, but ${summarizeIssueImageFailures(result)}`);
          } else {
            toast.success("Issue updated");
          }
          await invalidate();
        }}
      />

      <ConfirmationDialog
        open={deleteConfirmOpen}
        onOpenChange={setDeleteConfirmOpen}
        title={`Delete ${issue.data.identifier}?`}
        description="This removes the issue from Maestro, including its local workspace, activity history, and attached images. Linked provider items may also be removed."
        confirmLabel="Delete issue"
        pendingLabel="Deleting issue..."
        isPending={deleteMutation.isPending}
        onConfirm={async () => {
          await deleteMutation.mutateAsync();
        }}
      />

      <Dialog
        open={previewImage !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) {
            setPreviewImageID(null);
            setDeleteImageConfirmOpen(false);
          }
        }}
      >
        {previewImage ? (
          <DialogContent className="max-h-[calc(100vh-2rem)] w-[min(96vw,1100px)] overflow-y-auto p-0">
            <div className="grid lg:grid-cols-[minmax(0,1fr)_320px]">
              <div className="flex min-h-[320px] items-center justify-center bg-black p-4">
                <img
                  alt={previewImage.filename}
                  className="max-h-[78vh] w-full rounded-[calc(var(--panel-radius)-0.25rem)] object-contain"
                  src={issueImageContentURL(identifier, previewImage.id)}
                />
              </div>
              <div className="grid content-start gap-5 p-6">
                <div>
                  <DialogTitle className="pr-10 text-xl font-semibold text-white">{previewImage.filename}</DialogTitle>
                  <DialogDescription className="mt-2 pr-10 text-sm text-[var(--muted-foreground)]">
                    Stored locally for this issue and served by the Maestro dashboard API.
                  </DialogDescription>
                </div>
                <div className="grid gap-3 text-sm">
                  <div className="rounded-xl border border-white/10 bg-black/20 px-4 py-3">
                    <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Size</p>
                    <p className="mt-2 text-white">{formatIssueImageSize(previewImage.byte_size)}</p>
                  </div>
                  <div className="rounded-xl border border-white/10 bg-black/20 px-4 py-3">
                    <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Type</p>
                    <p className="mt-2 text-white">{previewImage.content_type}</p>
                  </div>
                  <div className="rounded-xl border border-white/10 bg-black/20 px-4 py-3">
                    <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Uploaded</p>
                    <p className="mt-2 text-white">{formatRelativeTime(previewImage.created_at)}</p>
                    <p className="mt-1 text-xs text-[var(--muted-foreground)]">
                      {formatDateTime(previewImage.created_at)}
                    </p>
                  </div>
                </div>
                <div className="flex justify-end">
                  <Button
                    variant="destructive"
                    disabled={deleteImageMutation.isPending}
                    onClick={() => setDeleteImageConfirmOpen(true)}
                  >
                    <Trash2 className="size-4" />
                    Remove image
                  </Button>
                </div>
              </div>
            </div>
          </DialogContent>
        ) : null}
      </Dialog>

      <ConfirmationDialog
        open={deleteImageConfirmOpen && previewImage !== null}
        onOpenChange={setDeleteImageConfirmOpen}
        title={previewImage ? `Delete ${previewImage.filename}?` : "Delete image?"}
        description="This permanently deletes the image attachment from the issue."
        confirmLabel="Delete image"
        pendingLabel="Deleting image..."
        isPending={deleteImageMutation.isPending}
        onConfirm={async () => {
          if (!previewImage) {
            return;
          }
          await deleteImageMutation.mutateAsync(previewImage.id);
        }}
      />
    </div>
  );
}
