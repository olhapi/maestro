import { type ComponentType, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Paperclip, Pencil, Reply, RotateCcw, Send, Trash2, Upload, X } from "lucide-react";
import { toast } from "sonner";

import { PageHeader } from "@/components/dashboard/page-header";
import { SessionExecutionCard } from "@/components/dashboard/session-execution-card";
import { IssueDialog } from "@/components/forms";
import { Badge } from "@/components/ui/badge";
import { Button, type ButtonProps } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ConfirmationDialog } from "@/components/ui/confirmation-dialog";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { FilePicker } from "@/components/ui/file-picker";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api } from "@/lib/api";
import { extractClipboardFiles } from "@/lib/clipboard-files";
import { appRoutes } from "@/lib/routes";
import {
  applyIssueAssetChanges,
  formatIssueAssetSize,
  isIssueAssetImage,
  issueAssetContentURL,
  summarizeIssueAssetFailures,
} from "@/lib/issue-assets";
import { getStateMeta, issueStatesFor } from "@/lib/dashboard";
import type { AgentCommand, IssueAsset, IssueComment, IssueCommentAttachment, IssueState, PermissionProfile } from "@/lib/types";
import { cn, formatDateTime, formatNumber, formatRelativeTime } from "@/lib/utils";

const permissionProfileLabels: Record<PermissionProfile, string> = {
  default: "Default permissions",
  "full-access": "Full access",
  "plan-then-full-access": "Plan, then full access",
};

function permissionProfileHelpText(issuePermissionProfile: PermissionProfile | undefined, projectPermissionProfile: PermissionProfile | undefined) {
  if ((issuePermissionProfile ?? "default") === "default" && (projectPermissionProfile ?? "default") === "full-access") {
    return "Default currently inherits this project's full-access profile. Switching to full access keeps the issue pinned there even if the project default changes later."
  }
  if ((issuePermissionProfile ?? "default") === "default" && (projectPermissionProfile ?? "default") === "plan-then-full-access") {
    return "Default currently inherits this project's plan-first profile. The agent can inspect the repo and research during planning, but full-access execution only starts after you approve the final plan."
  }
  if ((issuePermissionProfile ?? "default") === "plan-then-full-access") {
    return "Planning runs stay in plan mode with workspace-scoped permissions until you approve the final plan. Approval then promotes this issue to normal execution with full access."
  }
  return "Default inherits the project permission profile. Full access applies on the next turn in the active run without restarting the thread."
}

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

function issueCommentAttachmentContentURL(identifier: string, commentID: string, attachmentID: string) {
  return `/api/v1/app/issues/${encodeURIComponent(identifier)}/comments/${encodeURIComponent(commentID)}/attachments/${encodeURIComponent(attachmentID)}/content`;
}

function attachmentChipClassName(variant: "default" | "destructive" = "default") {
  return cn(
    "inline-flex max-w-full items-center gap-2 rounded-full border px-2.5 py-1 text-xs font-medium transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)]",
    variant === "destructive"
      ? "border-red-500/30 bg-red-500/15 text-red-100 hover:bg-red-500/20"
      : "border-white/10 bg-white/5 text-white hover:bg-white/10",
  );
}

function compactAttachmentMedia({
  contentType,
  label,
  src,
}: {
  contentType?: string;
  label: string;
  src?: string | null;
}) {
  if (!contentType || !isIssueAssetImage(contentType) || !src) {
    return <Paperclip className="size-3.5 shrink-0 text-[var(--muted-foreground)]" aria-hidden="true" />;
  }

  return (
    <span className="flex size-6 shrink-0 overflow-hidden rounded-md border border-white/10 bg-black/20">
      <img alt={label} className="size-full object-cover" loading="lazy" src={src} />
    </span>
  );
}

function IssueCommentAttachmentLink({
  identifier,
  commentID,
  attachment,
}: {
  identifier: string;
  commentID: string;
  attachment: IssueCommentAttachment;
}) {
  const src = issueCommentAttachmentContentURL(identifier, commentID, attachment.id);
  return (
    <a
      aria-label={`Open ${attachment.filename}`}
      className={attachmentChipClassName()}
      href={src}
      rel="noreferrer"
      target="_blank"
      title={`${attachment.filename} (${formatIssueAssetSize(attachment.byte_size)})`}
    >
      {compactAttachmentMedia({
        contentType: attachment.content_type,
        label: attachment.filename,
        src: isIssueAssetImage(attachment.content_type) ? src : null,
      })}
      <span className="min-w-0 truncate">{attachment.filename}</span>
    </a>
  );
}

function QueuedCommentAttachmentPreview({
  file,
  previewURL,
  onRemove,
}: {
  file: File;
  previewURL: string | null;
  onRemove: () => void;
}) {
  return (
    <button
      aria-label={`Remove ${file.name}`}
      className={attachmentChipClassName()}
      title={file.name}
      type="button"
      onClick={onRemove}
    >
      {compactAttachmentMedia({
        contentType: file.type,
        label: file.name,
        src: previewURL,
      })}
      <span className="min-w-0 truncate">{file.name}</span>
      <X className="size-3 shrink-0 text-[var(--muted-foreground)]" aria-hidden="true" />
    </button>
  );
}

function EditableCommentAttachmentChip({
  attachment,
  removed,
  onToggle,
  identifier,
  commentID,
}: {
  attachment: IssueCommentAttachment;
  removed: boolean;
  onToggle: () => void;
  identifier: string;
  commentID: string;
}) {
  const src = issueCommentAttachmentContentURL(identifier, commentID, attachment.id);

  return (
    <button
      aria-label={`${removed ? "Restore" : "Remove"} ${attachment.filename}`}
      className={attachmentChipClassName(removed ? "destructive" : "default")}
      title={`${attachment.filename} (${formatIssueAssetSize(attachment.byte_size)})`}
      type="button"
      onClick={onToggle}
    >
      {compactAttachmentMedia({
        contentType: attachment.content_type,
        label: attachment.filename,
        src: isIssueAssetImage(attachment.content_type) ? src : null,
      })}
      <span className="min-w-0 truncate">{attachment.filename}</span>
      {removed ? (
        <RotateCcw className="size-3 shrink-0" aria-hidden="true" />
      ) : (
        <X className="size-3 shrink-0" aria-hidden="true" />
      )}
    </button>
  );
}

function isCommentAttachmentImage(file: File) {
  return isIssueAssetImage(file.type) || /\.(avif|bmp|gif|heic|heif|jpe?g|png|svg|webp)$/i.test(file.name);
}

type QueuedCommentAttachment = {
  file: File;
  previewURL: string | null;
  isImage: boolean;
};

function createQueuedCommentAttachment(file: File): QueuedCommentAttachment {
  const isImage = isCommentAttachmentImage(file);

  return {
    file,
    isImage,
    previewURL: isImage && typeof URL.createObjectURL === "function" ? URL.createObjectURL(file) : null,
  };
}

function revokeQueuedCommentAttachment(attachment: QueuedCommentAttachment | undefined) {
  if (attachment?.previewURL && typeof URL.revokeObjectURL === "function") {
    URL.revokeObjectURL(attachment.previewURL);
  }
}

function CommentActionButton({
  icon: Icon,
  label,
  onClick,
  variant,
}: {
  icon: ComponentType<{ className?: string }>;
  label: string;
  onClick: () => void;
  variant: ButtonProps["variant"];
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          aria-label={label}
          className={cn("shrink-0 p-0")}
          onClick={onClick}
          size="icon"
          type="button"
          variant={variant}
        >
          <Icon className="size-4" />
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

function IssueAssetLink({
  identifier,
  asset,
}: {
  identifier: string;
  asset: IssueAsset;
}) {
  return (
    <a
      className="text-sm text-white underline-offset-4 hover:underline"
      href={issueAssetContentURL(identifier, asset.id)}
      rel="noreferrer"
      target="_blank"
    >
      {asset.filename}
    </a>
  );
}

function normalizeIssueComment(comment: IssueComment): IssueComment {
  return {
    ...comment,
    attachments: Array.isArray(comment.attachments) ? comment.attachments : [],
    replies: Array.isArray(comment.replies) ? comment.replies.map(normalizeIssueComment) : [],
  };
}

function CommentComposer({
  submitLabel,
  pendingLabel,
  isPending,
  placeholder,
  initialBody = "",
  onSubmit,
}: {
  submitLabel: string;
  pendingLabel: string;
  isPending: boolean;
  placeholder: string;
  initialBody?: string;
  onSubmit: (body: string, files: File[]) => Promise<void>;
}) {
  const [body, setBody] = useState(initialBody);
  const [queuedAttachments, setQueuedAttachments] = useState<QueuedCommentAttachment[]>([]);
  const [dragActive, setDragActive] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const queuedAttachmentsRef = useRef<QueuedCommentAttachment[]>([]);

  useEffect(() => {
    return () => {
      for (const attachment of queuedAttachmentsRef.current) {
        revokeQueuedCommentAttachment(attachment);
      }
    };
  }, []);

  const updateQueuedAttachments = (nextAttachments: QueuedCommentAttachment[]) => {
    queuedAttachmentsRef.current = nextAttachments;
    setQueuedAttachments(nextAttachments);
  };

  const addFiles = (nextFiles: File[]) => {
    if (nextFiles.length === 0) {
      return;
    }

    updateQueuedAttachments([...queuedAttachmentsRef.current, ...nextFiles.map(createQueuedCommentAttachment)]);
  };

  const removeQueuedAttachment = (index: number) => {
    const currentAttachments = queuedAttachmentsRef.current;
    const removedAttachment = currentAttachments[index];
    if (!removedAttachment) {
      return;
    }

    revokeQueuedCommentAttachment(removedAttachment);
    updateQueuedAttachments(currentAttachments.filter((_, currentIndex) => currentIndex !== index));
  };

  const clearQueuedAttachments = () => {
    for (const attachment of queuedAttachmentsRef.current) {
      revokeQueuedCommentAttachment(attachment);
    }
    updateQueuedAttachments([]);
  };

  return (
    <div
      className={cn(
        "grid gap-3 rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 p-3 transition",
        dragActive && "border-[var(--accent)] bg-[rgba(196,255,87,0.08)]",
      )}
      onDragEnter={(event) => {
        event.preventDefault();
        setDragActive(true);
      }}
      onDragLeave={(event) => {
        event.preventDefault();
        setDragActive(false);
      }}
      onDragOver={(event) => {
        event.preventDefault();
      }}
      onDrop={(event) => {
        event.preventDefault();
        setDragActive(false);
        addFiles(Array.from(event.dataTransfer.files ?? []));
      }}
    >
      <div className="relative">
        <Textarea
          value={body}
          className="min-h-[110px] resize-y pb-20 pr-24"
          placeholder={placeholder}
          onChange={(event) => setBody(event.target.value)}
          onPaste={(event) => {
            const pastedFiles = extractClipboardFiles(event.clipboardData);
            if (pastedFiles.length === 0) {
              return;
            }
            event.preventDefault();
            addFiles(pastedFiles);
          }}
        />
        <div className="absolute inset-x-3 bottom-3 flex flex-wrap items-end justify-end gap-2">
          {queuedAttachments.map((preview, index) => (
            <QueuedCommentAttachmentPreview
              key={`${preview.file.name}-${preview.file.size}-${preview.file.lastModified}-${index}`}
              file={preview.file}
              previewURL={preview.previewURL}
              onRemove={() => removeQueuedAttachment(index)}
            />
          ))}
          <input
            ref={inputRef}
            aria-label={`${submitLabel} attachments`}
            className="sr-only"
            multiple
            type="file"
            onChange={(event) => {
              addFiles(Array.from(event.currentTarget.files ?? []));
              event.currentTarget.value = "";
            }}
          />
          <Button
            aria-label="Attach files"
            className="size-8 rounded-full p-0"
            onClick={() => inputRef.current?.click()}
            type="button"
            variant="secondary"
          >
            <Upload className="size-4" aria-hidden="true" />
          </Button>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          className="ml-auto"
          disabled={isPending || (!body.trim() && queuedAttachments.length === 0)}
          onClick={async () => {
            await onSubmit(body, queuedAttachments.map(({ file }) => file));
            setBody("");
            clearQueuedAttachments();
          }}
          type="button"
        >
          <Send className="size-4" />
          {isPending ? pendingLabel : submitLabel}
        </Button>
      </div>
    </div>
  );
}

function IssueCommentEntry({
  identifier,
  comment,
  level,
  editingCommentID,
  updatePending,
  onStartEdit,
  onCancelEdit,
  onSaveEdit,
  onDelete,
  replyParentID,
  replyPending,
  onStartReply,
  onCancelReply,
  onSaveReply,
}: {
  identifier: string;
  comment: IssueComment;
  level: number;
  editingCommentID: string | null;
  updatePending: boolean;
  onStartEdit: (comment: IssueComment) => void;
  onCancelEdit: () => void;
  onSaveEdit: (commentID: string, body: string, files: File[], removeAttachmentIDs: string[]) => Promise<void>;
  onDelete: (commentID: string) => Promise<void>;
  replyParentID: string | null;
  replyPending: boolean;
  onStartReply: (commentID: string) => void;
  onCancelReply: () => void;
  onSaveReply: (parentCommentID: string, body: string, files: File[]) => Promise<void>;
}) {
  const normalizedComment = normalizeIssueComment(comment);
  const isEditing = editingCommentID === comment.id;
  const isReplying = replyParentID === comment.id;
  const [removeAttachmentIDs, setRemoveAttachmentIDs] = useState<string[]>([]);

  return (
    <div className="grid gap-3 rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge className="border-white/10 bg-white/5 text-white">{normalizedComment.author?.name || "Unknown"}</Badge>
        <span className="text-xs text-[var(--muted-foreground)]">{formatRelativeTime(normalizedComment.created_at)}</span>
        {level > 0 ? <Badge className="border-cyan-400/20 bg-cyan-400/10 text-cyan-100">Reply</Badge> : null}
      </div>
      {!isEditing ? (
        <>
          <p className="whitespace-pre-wrap text-sm leading-6 text-[var(--muted-foreground)]">
            {normalizedComment.deleted_at ? "[deleted]" : normalizedComment.body || "No text"}
          </p>
          {normalizedComment.attachments.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {normalizedComment.attachments.map((attachment) => (
                <IssueCommentAttachmentLink
                  key={attachment.id}
                  identifier={identifier}
                  commentID={normalizedComment.id}
                  attachment={attachment}
                />
              ))}
            </div>
          ) : null}
          {!normalizedComment.deleted_at ? (
            <div className="flex flex-wrap gap-2">
              {level === 0 ? (
                <CommentActionButton
                  icon={Reply}
                  label="Reply"
                  onClick={() => onStartReply(normalizedComment.id)}
                  variant="secondary"
                />
              ) : null}
              <CommentActionButton
                icon={Pencil}
                label="Edit"
                onClick={() => onStartEdit(normalizedComment)}
                variant="secondary"
              />
              <CommentActionButton
                icon={Trash2}
                label="Delete"
                onClick={() => void onDelete(normalizedComment.id)}
                variant="destructive"
              />
            </div>
          ) : null}
        </>
      ) : (
        <div className="grid gap-3">
          <CommentComposer
            initialBody={normalizedComment.body || ""}
            isPending={updatePending}
            pendingLabel="Saving..."
            placeholder="Update the comment"
            submitLabel="Save"
            onSubmit={async (body, files) => {
              await onSaveEdit(normalizedComment.id, body, files, removeAttachmentIDs);
              setRemoveAttachmentIDs([]);
            }}
          />
          {normalizedComment.attachments.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {normalizedComment.attachments.map((attachment) => {
                const removed = removeAttachmentIDs.includes(attachment.id);
                return (
                  <EditableCommentAttachmentChip
                    key={attachment.id}
                    attachment={attachment}
                    commentID={normalizedComment.id}
                    identifier={identifier}
                    onToggle={() =>
                      setRemoveAttachmentIDs((current) =>
                        current.includes(attachment.id)
                          ? current.filter((id) => id !== attachment.id)
                          : [...current, attachment.id],
                      )
                    }
                    removed={removed}
                  />
                );
              })}
            </div>
          ) : null}
          <div className="flex justify-end">
            <Button
              size="sm"
              type="button"
              variant="secondary"
              onClick={() => {
                setRemoveAttachmentIDs([]);
                onCancelEdit();
              }}
            >
              Cancel
            </Button>
          </div>
        </div>
      )}
      {isReplying ? (
        <CommentComposer
          isPending={replyPending}
          pendingLabel="Replying..."
          placeholder="Write a reply"
          submitLabel="Reply"
          onSubmit={async (body, files) => {
            await onSaveReply(normalizedComment.id, body, files);
          }}
        />
      ) : null}
      {isReplying ? (
        <div className="flex justify-end">
          <Button size="sm" type="button" variant="secondary" onClick={onCancelReply}>
            Cancel
          </Button>
        </div>
      ) : null}
      {normalizedComment.replies.length > 0 ? (
        <div className="grid gap-3 border-l border-white/10 pl-4">
          {normalizedComment.replies.map((reply) => (
            <IssueCommentEntry
              key={reply.id}
              identifier={identifier}
              comment={reply}
              editingCommentID={editingCommentID}
              level={level + 1}
              onCancelEdit={onCancelEdit}
              onCancelReply={onCancelReply}
              onDelete={onDelete}
              onSaveEdit={onSaveEdit}
              onSaveReply={onSaveReply}
              onStartEdit={onStartEdit}
              onStartReply={onStartReply}
              replyParentID={replyParentID}
              replyPending={replyPending}
              updatePending={updatePending}
            />
          ))}
        </div>
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
  const [selectedAssetID, setSelectedAssetID] = useState<string | null>(null);
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [deleteAssetConfirmOpen, setDeleteAssetConfirmOpen] = useState(false);
  const [editingCommentID, setEditingCommentID] = useState<string | null>(null);
  const [replyParentID, setReplyParentID] = useState<string | null>(null);

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
      if (typeof document !== "undefined" && document.visibilityState === "hidden") {
        return false;
      }
      if (query.state.data?.active) {
        return 1500;
      }
      if (query.state.data?.retry_state === "scheduled") {
        return 5000;
      }
      return false;
    },
  });
  const comments = useQuery({
    queryKey: ["issue-comments", identifier],
    queryFn: () => api.listIssueComments(identifier),
  });

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["bootstrap"] }),
      queryClient.invalidateQueries({ queryKey: ["issues"] }),
      queryClient.invalidateQueries({ queryKey: ["issue", identifier] }),
      queryClient.invalidateQueries({ queryKey: ["issue-comments", identifier] }),
      queryClient.invalidateQueries({
        queryKey: ["issue-execution", identifier],
      }),
      queryClient.invalidateQueries({ queryKey: ["project"] }),
      queryClient.invalidateQueries({ queryKey: ["epic"] }),
      queryClient.invalidateQueries({ queryKey: ["interrupts"] }),
      queryClient.invalidateQueries({ queryKey: ["sessions"] }),
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
  const permissionMutation = useMutation({
    mutationFn: (permissionProfile: PermissionProfile) =>
      api.setIssuePermissionProfile(identifier, permissionProfile),
    onSuccess: async () => {
      toast.success("Permissions updated");
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to update permissions: ${error.message}` : "Unable to update permissions");
    },
  });
  const approvePlanMutation = useMutation({
    mutationFn: () => api.approveIssuePlan(identifier),
    onSuccess: async () => {
      toast.success("Plan approved");
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to approve plan: ${error.message}` : "Unable to approve plan");
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
  const uploadAssetsMutation = useMutation({
    mutationFn: (files: File[]) =>
      applyIssueAssetChanges(identifier, {
        newAssets: files,
        removeAssetIDs: [],
      }),
    onSuccess: async (result, files) => {
      if (result.failures.length > 0) {
        toast.error(`Upload finished with errors: ${summarizeIssueAssetFailures(result)}`);
      } else {
        toast.success(files.length === 1 ? "Asset attached" : `${files.length} assets attached`);
      }
      await invalidate();
    },
  });
  const deleteAssetMutation = useMutation({
    mutationFn: (assetID: string) =>
      applyIssueAssetChanges(identifier, {
        newAssets: [],
        removeAssetIDs: [assetID],
      }),
    onSuccess: async (result) => {
      if (result.failures.length > 0) {
        toast.error(`Unable to remove asset: ${summarizeIssueAssetFailures(result)}`);
      } else {
        setSelectedAssetID(null);
        toast.success("Asset removed");
      }
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to remove asset: ${error.message}` : "Unable to remove asset");
    },
  });
  const stateMutation = useMutation({
    mutationFn: (nextState: IssueState) => api.setIssueState(identifier, nextState),
    onSuccess: async () => {
      toast.success("State updated");
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to update state: ${error.message}` : "Unable to update state");
    },
  });
  const createCommentMutation = useMutation({
    mutationFn: ({ body, parentCommentID, files }: { body: string; parentCommentID?: string; files: File[] }) =>
      api.createIssueComment(identifier, {
        body,
        parent_comment_id: parentCommentID,
        files,
      }),
    onSuccess: async () => {
      toast.success("Comment added");
      setReplyParentID(null);
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to add comment: ${error.message}` : "Unable to add comment");
    },
  });
  const updateCommentMutation = useMutation({
    mutationFn: ({
      commentID,
      body,
      files,
      removeAttachmentIDs,
    }: {
      commentID: string;
      body: string;
      files: File[];
      removeAttachmentIDs: string[];
    }) =>
      api.updateIssueComment(identifier, commentID, {
        body,
        files,
        remove_attachment_ids: removeAttachmentIDs,
      }),
    onSuccess: async () => {
      toast.success("Comment updated");
      setEditingCommentID(null);
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to update comment: ${error.message}` : "Unable to update comment");
    },
  });
  const deleteCommentMutation = useMutation({
    mutationFn: (commentID: string) => api.deleteIssueComment(identifier, commentID),
    onSuccess: async () => {
      toast.success("Comment deleted");
      setEditingCommentID(null);
      setReplyParentID(null);
      await invalidate();
    },
    onError: (error) => {
      toast.error(error instanceof Error ? `Unable to delete comment: ${error.message}` : "Unable to delete comment");
    },
  });

  if (!bootstrap.data || !issue.data || !execution.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />;
  }

  const commentItems = (comments.data?.items ?? []).map(normalizeIssueComment);
  const availableStates = issueStatesFor(bootstrap.data.issues.items, [issue.data.state]);
  const selectedAsset = issue.data.assets.find((asset) => asset.id === selectedAssetID) ?? null;
  const previewAsset = selectedAsset && isIssueAssetImage(selectedAsset.content_type) ? selectedAsset : null;
  const imageAssets = issue.data.assets.filter((asset) => isIssueAssetImage(asset.content_type));
  const fileAssets = issue.data.assets.filter((asset) => !isIssueAssetImage(asset.content_type));
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
              <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 px-3.5 py-3">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Assigned agent</p>
                <p className="mt-3 text-white">{issue.data.agent_name || "No agent assigned"}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                  {issue.data.agent_prompt || "No agent-specific prompt"}
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

          <Card data-testid="issue-assets-card">
            <CardHeader className="flex-col gap-3 pb-2.5 sm:flex-row sm:items-start sm:justify-between">
              <div className="min-w-0">
                <CardTitle>Assets</CardTitle>
                <p className="mt-1 text-sm text-[var(--muted-foreground)]">
                  Attach files locally to this issue. Image assets are previewable; other assets open directly.
                </p>
              </div>
            </CardHeader>
            <CardContent className="grid gap-4 pt-0">
              <FilePicker
                compact
                ariaLabel="Attach assets"
                buttonLabel={uploadAssetsMutation.isPending ? "Uploading..." : "Attach files"}
                disabled={uploadAssetsMutation.isPending}
                summary="Drop files here or paste with Cmd/Ctrl+V while this picker is focused."
                multiple
                onFilesSelected={(files) => {
                  if (files.length > 0) {
                    uploadAssetsMutation.mutate(files);
                  }
                }}
              />
              {issue.data.assets.length === 0 ? (
                <div className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 px-4 py-4">
                  <p className="text-sm font-medium text-white">No assets attached yet</p>
                  <p className="mt-1 text-sm text-[var(--muted-foreground)]">
                    Add screenshots, recordings, logs, docs, or any other issue-specific files here.
                  </p>
                </div>
              ) : (
                <div className="grid gap-4">
                  {imageAssets.length > 0 ? (
                    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                      {imageAssets.map((asset) => (
                        <button
                          key={asset.id}
                          type="button"
                          aria-label={`Open ${asset.filename}`}
                          className="group overflow-hidden rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 text-left transition hover:border-white/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)]"
                          onClick={() => setSelectedAssetID(asset.id)}
                        >
                          <img
                            alt={asset.filename}
                            className="aspect-square w-full bg-black object-cover transition duration-300 group-hover:scale-[1.02]"
                            loading="lazy"
                            src={issueAssetContentURL(identifier, asset.id)}
                          />
                        </button>
                      ))}
                    </div>
                  ) : null}
                  {fileAssets.length > 0 ? (
                    <div className="grid gap-3">
                      {fileAssets.map((asset) => (
                        <div
                          key={asset.id}
                          className="flex flex-wrap items-center justify-between gap-3 rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 px-4 py-3"
                        >
                          <div className="min-w-0">
                            <IssueAssetLink identifier={identifier} asset={asset} />
                            <p className="mt-1 text-xs text-[var(--muted-foreground)]">
                              {formatIssueAssetSize(asset.byte_size)} · {asset.content_type}
                            </p>
                          </div>
                          <div className="flex gap-2">
                            <a
                              className="inline-flex h-9 items-center justify-center gap-2 rounded-xl border border-white/10 bg-white/5 px-3 text-sm font-medium text-white transition duration-200 hover:bg-white/10"
                              href={issueAssetContentURL(identifier, asset.id)}
                              rel="noreferrer"
                              target="_blank"
                            >
                              Open
                            </a>
                            <Button
                              size="sm"
                              type="button"
                              variant="destructive"
                              onClick={() => {
                                setSelectedAssetID(asset.id);
                                setDeleteAssetConfirmOpen(true);
                              }}
                            >
                              Remove
                            </Button>
                          </div>
                        </div>
                      ))}
                    </div>
                  ) : null}
                </div>
              )}
            </CardContent>
          </Card>

          <Card data-testid="issue-comments-card">
            <CardHeader className="pb-2.5">
              <CardTitle>Comments</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-4 pt-0">
              <CommentComposer
                isPending={createCommentMutation.isPending}
                pendingLabel="Posting..."
                placeholder="Add context, ask for a change, or leave review notes."
                submitLabel="Post comment"
                onSubmit={async (body, files) => {
                  await createCommentMutation.mutateAsync({ body, files });
                }}
              />
              {comments.isPending ? (
                <div className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 px-4 py-4">
                  <p className="text-sm font-medium text-white">Loading comments…</p>
                </div>
              ) : comments.isError ? (
                <div className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-amber-400/20 bg-amber-400/[0.06] px-4 py-4">
                  <p className="text-sm font-medium text-white">Comments are temporarily unavailable</p>
                  <p className="mt-1 text-sm text-[var(--muted-foreground)]">
                    {comments.error instanceof Error ? comments.error.message : "Unable to load comments right now."}
                  </p>
                </div>
              ) : commentItems.length === 0 ? (
                <div className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 px-4 py-4">
                  <p className="text-sm font-medium text-white">No comments yet</p>
                  <p className="mt-1 text-sm text-[var(--muted-foreground)]">
                    Use comments for review notes, decisions, and issue-specific follow-ups.
                  </p>
                </div>
              ) : (
                <div className="grid gap-3">
                  {commentItems.map((comment) => (
                    <IssueCommentEntry
                      key={comment.id}
                      identifier={identifier}
                      comment={comment}
                      editingCommentID={editingCommentID}
                      level={0}
                      onCancelEdit={() => setEditingCommentID(null)}
                      onCancelReply={() => setReplyParentID(null)}
                      onDelete={async (commentID) => {
                        await deleteCommentMutation.mutateAsync(commentID);
                      }}
                      onSaveEdit={async (commentID, body, files, removeAttachmentIDs) => {
                        await updateCommentMutation.mutateAsync({ commentID, body, files, removeAttachmentIDs });
                      }}
                      onSaveReply={async (parentCommentID, body, files) => {
                        await createCommentMutation.mutateAsync({ body, parentCommentID, files });
                      }}
                      onStartEdit={(comment) => {
                        setReplyParentID(null);
                        setEditingCommentID(comment.id);
                      }}
                      onStartReply={(commentID) => {
                        setEditingCommentID(null);
                        setReplyParentID(commentID);
                      }}
                      replyParentID={replyParentID}
                      replyPending={createCommentMutation.isPending}
                      updatePending={updateCommentMutation.isPending}
                    />
                  ))}
                </div>
              )}
            </CardContent>
          </Card>

          <SessionExecutionCard
            approvingPlan={approvePlanMutation.isPending}
            execution={execution.data}
            issueTotalTokens={issue.data.total_tokens_spent}
            onApprovePlan={() => {
              approvePlanMutation.mutate();
            }}
          />
        </div>

        <div className="grid content-start gap-[var(--section-gap)]" data-testid="issue-control-rail">
          <Card>
            <CardHeader>
              <CardTitle>Issue actions</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-2.5">
              <Select
                value={issue.data.state}
                disabled={stateMutation.isPending}
                onValueChange={(value) => {
                  stateMutation.mutate(value as IssueState);
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
              <div className="grid gap-2">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Agent permissions</p>
                <Select
                  value={issue.data.permission_profile ?? "default"}
                  onValueChange={async (value) => {
                    await permissionMutation.mutateAsync(value as PermissionProfile);
                  }}
                >
                  <SelectTrigger aria-label="Agent permissions" disabled={permissionMutation.isPending}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="default">{permissionProfileLabels.default}</SelectItem>
                    <SelectItem value="full-access">{permissionProfileLabels["full-access"]}</SelectItem>
                    <SelectItem value="plan-then-full-access">{permissionProfileLabels["plan-then-full-access"]}</SelectItem>
                  </SelectContent>
                </Select>
                <p className="text-xs text-[var(--muted-foreground)]">
                  {permissionProfileHelpText(issue.data.permission_profile, issue.data.project_permission_profile)}
                </p>
              </div>
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
        onSubmit={async (body, assetChanges) => {
          const updated = await api.updateIssue(identifier, body);
          const result = await applyIssueAssetChanges(updated.identifier, assetChanges);
          if (result.failures.length > 0) {
            toast.error(`Issue updated, but ${summarizeIssueAssetFailures(result)}`);
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
        description="This removes the issue from Maestro, including its local workspace, activity history, and attached assets. Linked provider items may also be removed."
        confirmLabel="Delete issue"
        pendingLabel="Deleting issue..."
        isPending={deleteMutation.isPending}
        onConfirm={async () => {
          await deleteMutation.mutateAsync();
        }}
      />

      <Dialog
        open={previewAsset !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) {
            setSelectedAssetID(null);
            setDeleteAssetConfirmOpen(false);
          }
        }}
      >
        {previewAsset ? (
          <DialogContent className="max-h-[calc(100vh-2rem)] w-[min(96vw,1100px)] overflow-y-auto p-0">
            <div className="grid lg:grid-cols-[minmax(0,1fr)_320px]">
              <div className="flex min-h-[320px] items-center justify-center bg-black p-4">
                <img
                  alt={previewAsset.filename}
                  className="max-h-[78vh] w-full rounded-[calc(var(--panel-radius)-0.25rem)] object-contain"
                  src={issueAssetContentURL(identifier, previewAsset.id)}
                />
              </div>
              <div className="grid content-start gap-5 p-6">
                <div>
                  <DialogTitle className="pr-10 text-xl font-semibold text-white">{previewAsset.filename}</DialogTitle>
                  <DialogDescription className="mt-2 pr-10 text-sm text-[var(--muted-foreground)]">
                    Stored locally for this issue and served by the Maestro dashboard API.
                  </DialogDescription>
                </div>
                <div className="grid gap-3 text-sm">
                  <div className="rounded-xl border border-white/10 bg-black/20 px-4 py-3">
                    <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Size</p>
                    <p className="mt-2 text-white">{formatIssueAssetSize(previewAsset.byte_size)}</p>
                  </div>
                  <div className="rounded-xl border border-white/10 bg-black/20 px-4 py-3">
                    <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Type</p>
                    <p className="mt-2 text-white">{previewAsset.content_type}</p>
                  </div>
                  <div className="rounded-xl border border-white/10 bg-black/20 px-4 py-3">
                    <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Uploaded</p>
                    <p className="mt-2 text-white">{formatRelativeTime(previewAsset.created_at)}</p>
                    <p className="mt-1 text-xs text-[var(--muted-foreground)]">
                      {formatDateTime(previewAsset.created_at)}
                    </p>
                  </div>
                </div>
                <div className="flex justify-end">
                  <Button
                    variant="destructive"
                    disabled={deleteAssetMutation.isPending}
                    onClick={() => setDeleteAssetConfirmOpen(true)}
                  >
                    <Trash2 className="size-4" />
                    Remove asset
                  </Button>
                </div>
              </div>
            </div>
          </DialogContent>
        ) : null}
      </Dialog>

      <ConfirmationDialog
        open={deleteAssetConfirmOpen && selectedAsset !== null}
        onOpenChange={setDeleteAssetConfirmOpen}
        title={selectedAsset ? `Delete ${selectedAsset.filename}?` : "Delete asset?"}
        description="This permanently deletes the asset from the issue."
        confirmLabel="Delete asset"
        pendingLabel="Deleting asset..."
        isPending={deleteAssetMutation.isPending}
        onConfirm={async () => {
          if (!selectedAsset) {
            return;
          }
          await deleteAssetMutation.mutateAsync(selectedAsset.id);
        }}
      />
    </div>
  );
}
