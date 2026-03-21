import { type ReactNode, useEffect, useId, useMemo, useRef, useState } from "react";

import { MultiCombobox, type MultiComboboxOption } from "@/components/ui/multi-combobox";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { IssueDescriptionField } from "@/components/issue-description-field";
import { FilePicker } from "@/components/ui/file-picker";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { api } from "@/lib/api";
import { getStateMeta, issueStates } from "@/lib/dashboard";
import {
  formatIssueAssetSize,
  issueAssetInputAccept,
  type IssueAssetChangeSet,
} from "@/lib/issue-assets";
import type { EpicSummary, IssueDetail, IssueSummary, IssueType, ProjectSummary } from "@/lib/types";

const noEpicValue = "__no-epic__";
const blockerSearchDebounceMs = 150;

function Field({
  label,
  children,
}: {
  label: string;
  children: ReactNode | ((props: { labelId: string }) => ReactNode);
}) {
  const labelId = useId();

  return (
    <div className="grid gap-2">
      <Label id={labelId}>{label}</Label>
      {typeof children === "function" ? children({ labelId }) : children}
    </div>
  );
}

function issueOptionLabel(issue: IssueSummary) {
  return issue.title ? `${issue.identifier} · ${issue.title}` : issue.identifier;
}

function dedupeIssues(issues: IssueSummary[]) {
  const unique = new Map<string, IssueSummary>();

  for (const issue of issues) {
    if (!unique.has(issue.identifier)) {
      unique.set(issue.identifier, issue);
    }
  }

  return [...unique.values()];
}

function useDialogReset(open: boolean, resetKey: string, reset: () => void) {
  const previousOpenRef = useRef(open);
  const previousResetKeyRef = useRef(resetKey);
  const resetRef = useRef(reset);

  useEffect(() => {
    resetRef.current = reset;
  }, [reset]);

  useEffect(() => {
    const opened = open && !previousOpenRef.current;
    const targetChanged = open && previousResetKeyRef.current !== resetKey;

    if (opened || targetChanged) {
      resetRef.current();
    }

    previousOpenRef.current = open;
    previousResetKeyRef.current = resetKey;
  }, [open, resetKey]);
}

export function ProjectDialog({
  open,
  onOpenChange,
  initial,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initial?: Partial<ProjectSummary>;
  onSubmit: (body: {
    name: string;
    description?: string;
    repo_path: string;
    workflow_path?: string;
  }) => Promise<void>;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [repoPath, setRepoPath] = useState(initial?.repo_path ?? "");
  const [workflowPath, setWorkflowPath] = useState(initial?.workflow_path ?? "");
  const [pending, setPending] = useState(false);

  useDialogReset(open, initial?.id ?? initial?.name ?? "__new__", () => {
    setName(initial?.name ?? "");
    setDescription(initial?.description ?? "");
    setRepoPath(initial?.repo_path ?? "");
    setWorkflowPath(initial?.workflow_path ?? "");
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100vh-2rem)] overflow-y-auto">
        <div className="space-y-6">
          <div>
            <DialogTitle className="text-xl font-semibold text-white">
              {initial ? "Edit project" : "Create project"}
            </DialogTitle>
            <DialogDescription className="mt-2 text-sm text-[var(--muted-foreground)]">
              Manage the top-level portfolio containers for Maestro work.
            </DialogDescription>
          </div>
          <div className="grid gap-4">
            <Field label="Project name">
              {({ labelId }) => (
                <Input aria-labelledby={labelId} value={name} onChange={(event) => setName(event.target.value)} />
              )}
            </Field>
            <Field label="Description">
              {({ labelId }) => (
                <Textarea
                  aria-labelledby={labelId}
                  value={description}
                  onChange={(event) => setDescription(event.target.value)}
                />
              )}
            </Field>
            <Field label="Repo path">
              {({ labelId }) => (
                <Input
                  aria-labelledby={labelId}
                  value={repoPath}
                  onChange={(event) => setRepoPath(event.target.value)}
                  placeholder="/absolute/path/to/repo"
                />
              )}
            </Field>
            <Field label="Workflow path override">
              {({ labelId }) => (
                <Input
                  aria-labelledby={labelId}
                  value={workflowPath}
                  onChange={(event) => setWorkflowPath(event.target.value)}
                  placeholder="Optional; defaults to <repo>/WORKFLOW.md"
                />
              )}
            </Field>
          </div>
          <div className="flex justify-end gap-3">
            <Button variant="secondary" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              disabled={!name.trim() || !repoPath.trim() || pending}
              onClick={async () => {
                setPending(true);
                try {
                  await onSubmit({
                    name,
                    description,
                    repo_path: repoPath,
                    workflow_path: workflowPath || undefined,
                  });
                  onOpenChange(false);
                } finally {
                  setPending(false);
                }
              }}
            >
              {pending ? "Saving…" : initial ? "Update project" : "Create project"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function EpicDialog({
  open,
  onOpenChange,
  initial,
  projects,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initial?: Partial<EpicSummary>;
  projects: ProjectSummary[];
  onSubmit: (body: { project_id: string; name: string; description?: string }) => Promise<void>;
}) {
  const [projectID, setProjectID] = useState(initial?.project_id ?? "");
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [pending, setPending] = useState(false);

  useDialogReset(open, initial?.id ?? initial?.name ?? "__new__", () => {
    setProjectID(initial?.project_id ?? projects[0]?.id ?? "");
    setName(initial?.name ?? "");
    setDescription(initial?.description ?? "");
  });

  useEffect(() => {
    if (!open || initial?.project_id || projectID || !projects[0]?.id) {
      return;
    }
    setProjectID(projects[0].id);
  }, [initial?.project_id, open, projectID, projects]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <div className="space-y-6">
          <div>
            <DialogTitle className="text-xl font-semibold text-white">
              {initial ? "Edit epic" : "Create epic"}
            </DialogTitle>
            <DialogDescription className="mt-2 text-sm text-[var(--muted-foreground)]">
              Group related issues under a focused delivery arc.
            </DialogDescription>
          </div>
          <div className="grid gap-4">
            <Field label="Project">
              {({ labelId }) => (
                <Select value={projectID} onValueChange={setProjectID}>
                  <SelectTrigger aria-labelledby={labelId}>
                    <SelectValue placeholder="Select project" />
                  </SelectTrigger>
                  <SelectContent>
                    {projects.map((project) => (
                      <SelectItem key={project.id} value={project.id}>
                        {project.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>
            <Field label="Epic name">
              {({ labelId }) => (
                <Input aria-labelledby={labelId} value={name} onChange={(event) => setName(event.target.value)} />
              )}
            </Field>
            <Field label="Description">
              {({ labelId }) => (
                <Textarea
                  aria-labelledby={labelId}
                  value={description}
                  onChange={(event) => setDescription(event.target.value)}
                />
              )}
            </Field>
          </div>
          <div className="flex justify-end gap-3">
            <Button variant="secondary" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              disabled={!name.trim() || !projectID || pending}
              onClick={async () => {
                setPending(true);
                try {
                  await onSubmit({ project_id: projectID, name, description });
                  onOpenChange(false);
                } finally {
                  setPending(false);
                }
              }}
            >
              {pending ? "Saving…" : initial ? "Update epic" : "Create epic"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function IssueDialog({
  open,
  onOpenChange,
  initial,
  projects,
  epics,
  availableIssues = [],
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initial?: Partial<IssueDetail>;
  projects: ProjectSummary[];
  epics: EpicSummary[];
  availableIssues?: IssueSummary[];
  onSubmit: (body: Record<string, unknown>, assets: IssueAssetChangeSet) => Promise<void>;
}) {
  const isEditing = Boolean(initial?.identifier);
  const defaultProjectID = initial?.project_id ?? projects[0]?.id ?? "";
  const [projectID, setProjectID] = useState(defaultProjectID);
  const [epicID, setEpicID] = useState(initial?.epic_id ?? "");
  const [title, setTitle] = useState(initial?.title ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [issueType, setIssueType] = useState<IssueType>(initial?.issue_type ?? "standard");
  const [cron, setCron] = useState(initial?.cron ?? "");
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [state, setState] = useState<string>(initial?.state ?? "backlog");
  const [priority, setPriority] = useState(String(initial?.priority ?? 0));
  const [labels, setLabels] = useState(initial?.labels ?? []);
  const [agentName, setAgentName] = useState(initial?.agent_name ?? "");
  const [agentPrompt, setAgentPrompt] = useState(initial?.agent_prompt ?? "");
  const [blockedBy, setBlockedBy] = useState(initial?.blocked_by ?? []);
  const [branchName, setBranchName] = useState(initial?.branch_name ?? "");
  const [prURL, setPrURL] = useState(initial?.pr_url ?? "");
  const [newAssets, setNewAssets] = useState<File[]>([]);
  const [removedAssetIDs, setRemovedAssetIDs] = useState<string[]>([]);
  const [blockerSearch, setBlockerSearch] = useState("");
  const [remoteBlockerIssues, setRemoteBlockerIssues] = useState<IssueSummary[]>([]);
  const [loadingBlockerIssues, setLoadingBlockerIssues] = useState(false);
  const [pending, setPending] = useState(false);
  const selectedProject = projects.find((project) => project.id === projectID);
  const supportsEpics = selectedProject?.capabilities?.epics ?? true;
  const canChangeIssueType = true;

  useDialogReset(open, initial?.identifier ?? "__new__", () => {
    setProjectID(initial?.project_id ?? projects[0]?.id ?? "");
    setEpicID(initial?.epic_id ?? "");
    setTitle(initial?.title ?? "");
    setDescription(initial?.description ?? "");
    setIssueType(initial?.issue_type ?? "standard");
    setCron(initial?.cron ?? "");
    setEnabled(initial?.enabled ?? true);
    setState(initial?.state ?? "backlog");
    setPriority(String(initial?.priority ?? 0));
    setLabels(initial?.labels ?? []);
    setAgentName(initial?.agent_name ?? "");
    setAgentPrompt(initial?.agent_prompt ?? "");
    setBlockedBy(initial?.blocked_by ?? []);
    setBranchName(initial?.branch_name ?? "");
    setPrURL(initial?.pr_url ?? "");
    setNewAssets([]);
    setRemovedAssetIDs([]);
    setBlockerSearch("");
    setRemoteBlockerIssues([]);
  });

  useEffect(() => {
    if (!open || initial?.project_id || projectID || !projects[0]?.id) {
      return;
    }
    setProjectID(projects[0].id);
  }, [initial?.project_id, open, projectID, projects]);

  useEffect(() => {
    if (!open || !projectID || blockerSearch.trim().length < 2) {
      return;
    }

    const controller = new AbortController();
    const timeoutID = window.setTimeout(() => {
      setLoadingBlockerIssues(true);
      api.listIssues(
        {
          project_id: projectID,
          search: blockerSearch.trim(),
          limit: 25,
          sort: "updated_desc",
        },
        { signal: controller.signal },
      )
        .then((page) => {
          if (controller.signal.aborted) return;
          setRemoteBlockerIssues(page.items);
        })
        .catch((error: unknown) => {
          if ((error as Error).name === "AbortError") {
            return;
          }
          setRemoteBlockerIssues([]);
        })
        .finally(() => {
          if (!controller.signal.aborted) {
            setLoadingBlockerIssues(false);
          }
        });
    }, blockerSearchDebounceMs);

    return () => {
      window.clearTimeout(timeoutID);
      controller.abort();
    };
  }, [blockerSearch, open, projectID]);
  const visibleRemoteBlockerIssues = useMemo(
    () => (open && blockerSearch.trim().length >= 2 ? remoteBlockerIssues : []),
    [blockerSearch, open, remoteBlockerIssues],
  );
  const blockerSearchLoading = useMemo(
    () => (open && blockerSearch.trim().length >= 2 ? loadingBlockerIssues : false),
    [blockerSearch, loadingBlockerIssues, open],
  );

  const localProjectIssues = useMemo(
    () => dedupeIssues(availableIssues.filter((issue) => issue.project_id === projectID)),
    [availableIssues, projectID],
  );

  const filteredEpics = epics.filter((epic) => projectID !== "" && epic.project_id === projectID);

  useEffect(() => {
    if (!epicID) return;
    if (!filteredEpics.some((epic) => epic.id === epicID)) {
      setEpicID("");
    }
  }, [epicID, filteredEpics]);

  useEffect(() => {
    if (canChangeIssueType) {
      return;
    }
    setIssueType(initial?.issue_type ?? "standard");
    setCron(initial?.cron ?? "");
    setEnabled(initial?.enabled ?? true);
  }, [canChangeIssueType, initial?.cron, initial?.enabled, initial?.issue_type]);

  const labelOptions = useMemo<MultiComboboxOption[]>(() => {
    const unique = new Set<string>();
    for (const issue of localProjectIssues) {
      for (const label of issue.labels ?? []) {
        const trimmed = label.trim();
        if (trimmed) {
          unique.add(trimmed);
        }
      }
    }
    return [...unique].sort((left, right) => left.localeCompare(right)).map((label) => ({ value: label, label }));
  }, [localProjectIssues]);

  const blockerOptions = useMemo<MultiComboboxOption[]>(
    () =>
      dedupeIssues([...localProjectIssues, ...visibleRemoteBlockerIssues])
        .filter((issue) => issue.identifier !== initial?.identifier)
        .map((issue) => ({
          value: issue.identifier,
          label: issueOptionLabel(issue),
          keywords: [issue.identifier, issue.title],
        })),
    [initial?.identifier, localProjectIssues, visibleRemoteBlockerIssues],
  );

  const visibleExistingAssets = (initial?.assets ?? []).filter(
    (asset) => !removedAssetIDs.includes(asset.id),
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100vh-2rem)] w-[min(96vw,920px)] overflow-y-auto">
        <div className="space-y-6">
          <div>
            <DialogTitle className="text-xl font-semibold text-white">
              {isEditing ? `Edit ${initial?.identifier}` : "Create issue"}
            </DialogTitle>
            <DialogDescription className="mt-2 text-sm text-[var(--muted-foreground)]">
              Shape the issue, set operational metadata, and make it immediately actionable.
            </DialogDescription>
          </div>
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Project">
              {({ labelId }) => (
                <Select
                  value={projectID}
                  onValueChange={(nextProjectID) => {
                    setProjectID(nextProjectID);
                    if (nextProjectID !== projectID) {
                      setBlockedBy([]);
                      setBlockerSearch("");
                      setRemoteBlockerIssues([]);
                    }
                  }}
                >
                  <SelectTrigger aria-labelledby={labelId}>
                    <SelectValue placeholder={projects.length > 0 ? "Select project" : "Create a project first"} />
                  </SelectTrigger>
                  <SelectContent>
                    {projects.map((project) => (
                      <SelectItem key={project.id} value={project.id}>
                        {project.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>
            <Field label="Epic">
              {({ labelId }) => (
                <Select
                  disabled={!supportsEpics}
                  value={epicID || noEpicValue}
                  onValueChange={(value) => setEpicID(value === noEpicValue ? "" : value)}
                >
                  <SelectTrigger aria-labelledby={labelId}>
                    <SelectValue placeholder="No epic" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={noEpicValue}>No epic</SelectItem>
                    {filteredEpics.map((epic) => (
                      <SelectItem key={epic.id} value={epic.id}>
                        {epic.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>
            <Field label="Title">
              {({ labelId }) => (
                <Input aria-labelledby={labelId} value={title} onChange={(event) => setTitle(event.target.value)} />
              )}
            </Field>
            <Field label="Type">
              {({ labelId }) => (
                <ToggleGroup
                  type="single"
                  value={issueType}
                  onValueChange={(value) => {
                    if (value) {
                      setIssueType(value as IssueType);
                    }
                  }}
                  className="grid h-11 grid-cols-2 gap-1 rounded-xl border border-white/10 bg-black/20 p-[3px]"
                  aria-labelledby={labelId}
                  disabled={!canChangeIssueType}
                >
                  <ToggleGroupItem
                    className="h-full rounded-lg text-white data-[state=on]:bg-[var(--accent)] data-[state=on]:text-black"
                    value="standard"
                  >
                    Standard
                  </ToggleGroupItem>
                  <ToggleGroupItem
                    className="h-full rounded-lg text-white data-[state=on]:bg-[var(--accent)] data-[state=on]:text-black"
                    value="recurring"
                  >
                    Recurring
                  </ToggleGroupItem>
                </ToggleGroup>
              )}
            </Field>
            <Field label="State">
              {({ labelId }) => (
                <Select value={state} onValueChange={setState}>
                  <SelectTrigger aria-labelledby={labelId}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {[...new Set([state, ...issueStates])].map((value) => (
                      <SelectItem key={value} value={value}>
                        {getStateMeta(value).label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>
            <Field label="Priority">
              {({ labelId }) => (
                <Input
                  aria-labelledby={labelId}
                  type="number"
                  min={0}
                  value={priority}
                  onChange={(event) => setPriority(event.target.value)}
                />
              )}
            </Field>
            {issueType === "recurring" ? (
              <>
                <Field label="Cron">
                  {({ labelId }) => (
                    <Input
                      aria-labelledby={labelId}
                      value={cron}
                      onChange={(event) => setCron(event.target.value)}
                      placeholder="*/30 * * * *"
                    />
                  )}
                </Field>
                <Field label="Schedule">
                  {({ labelId }) => (
                    <div
                      aria-labelledby={labelId}
                      className="flex min-h-11 items-center justify-between rounded-xl border border-white/10 bg-black/20 px-4 py-2"
                    >
                      <div>
                        <p className="text-xs text-[var(--muted-foreground)]">Turn recurring runs on or off.</p>
                      </div>
                      <Switch aria-labelledby={labelId} checked={enabled} onCheckedChange={setEnabled} />
                    </div>
                  )}
                </Field>
              </>
            ) : null}
            <Field label="Labels">
              {({ labelId }) => (
                <MultiCombobox
                  key={`labels-${projectID || "none"}`}
                  labelledBy={labelId}
                  value={labels}
                  onChange={setLabels}
                  options={labelOptions}
                  allowCreate
                  placeholder="Select or create labels"
                  emptyText="No labels found."
                  createLabel={(value) => `Create label "${value}"`}
                />
              )}
            </Field>
            <Field label="Assigned agent">
              {({ labelId }) => (
                <Input
                  aria-labelledby={labelId}
                  value={agentName}
                  onChange={(event) => setAgentName(event.target.value)}
                  placeholder="marketing"
                />
              )}
            </Field>
            <Field label="Blockers">
              {({ labelId }) => (
                <MultiCombobox
                  key={`blockers-${projectID || "none"}-${initial?.identifier ?? "new"}`}
                  labelledBy={labelId}
                  value={blockedBy}
                  onChange={setBlockedBy}
                  onSearchChange={setBlockerSearch}
                  options={blockerOptions}
                  loading={blockerSearchLoading}
                  placeholder="Select blocker issues"
                  emptyText={
                    projectID
                      ? blockerSearch.trim().length >= 2
                        ? "No blockers found in this project."
                        : "Type at least 2 characters to search all project issues."
                      : "Select a project first."
                  }
                />
              )}
            </Field>
            <Field label="Branch">
              {({ labelId }) => (
                <Input
                  aria-labelledby={labelId}
                  value={branchName}
                  onChange={(event) => setBranchName(event.target.value)}
                />
              )}
            </Field>
            <Field label="PR URL">
              {({ labelId }) => (
                <Input aria-labelledby={labelId} value={prURL} onChange={(event) => setPrURL(event.target.value)} />
              )}
            </Field>
          </div>
          <Field label="Agent prompt">
            {({ labelId }) => (
              <Textarea
                aria-labelledby={labelId}
                value={agentPrompt}
                onChange={(event) => setAgentPrompt(event.target.value)}
                placeholder="Review the landing page copy and suggest stronger messaging."
              />
            )}
          </Field>
          <Field label="Description">
            {({ labelId }) => (
              <IssueDescriptionField
                labelledBy={labelId}
                value={description}
                onChange={setDescription}
              />
            )}
          </Field>
          <Field label="Assets">
            {({ labelId }) => (
              <div aria-labelledby={labelId} className="grid gap-3">
                <FilePicker
                  accept={issueAssetInputAccept}
                  ariaLabel="Assets"
                  buttonLabel="Choose files"
                  summary={
                    newAssets.length === 0
                      ? "Choose files to upload after save."
                      : newAssets.length === 1
                        ? newAssets[0].name
                        : `${newAssets.length} assets queued for upload`
                  }
                  multiple
                  onFilesSelected={(files) => {
                    setNewAssets((current) => [...current, ...files]);
                  }}
                />
                <p className="text-xs text-[var(--muted-foreground)]">
                  Any file type up to 100 MiB per asset. Assets stay local to this Maestro database.
                </p>
                {visibleExistingAssets.length > 0 ? (
                  <div className="grid gap-2">
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                      Attached now
                    </p>
                    {visibleExistingAssets.map((asset) => (
                      <div
                        key={asset.id}
                        className="flex items-center justify-between gap-3 rounded-xl border border-white/10 bg-black/20 px-4 py-3"
                      >
                        <div className="min-w-0">
                          <p className="truncate text-sm text-white">{asset.filename}</p>
                          <p className="mt-1 text-xs text-[var(--muted-foreground)]">
                            {formatIssueAssetSize(asset.byte_size)} · {asset.content_type}
                          </p>
                        </div>
                        <Button
                          type="button"
                          variant="secondary"
                          onClick={() =>
                            setRemovedAssetIDs((current) => [...current, asset.id])
                          }
                        >
                          Remove
                        </Button>
                      </div>
                    ))}
                  </div>
                ) : null}
                {removedAssetIDs.length > 0 ? (
                  <div className="grid gap-2">
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                      Removing on save
                    </p>
                    {(initial?.assets ?? [])
                      .filter((asset) => removedAssetIDs.includes(asset.id))
                      .map((asset) => (
                        <div
                          key={asset.id}
                          className="flex items-center justify-between gap-3 rounded-xl border border-amber-400/20 bg-amber-400/10 px-4 py-3"
                        >
                          <div className="min-w-0">
                            <p className="truncate text-sm text-white">{asset.filename}</p>
                            <p className="mt-1 text-xs text-[var(--muted-foreground)]">
                              Will be deleted after save
                            </p>
                          </div>
                          <Button
                            type="button"
                            variant="secondary"
                            onClick={() =>
                              setRemovedAssetIDs((current) =>
                                current.filter((value) => value !== asset.id),
                              )
                            }
                          >
                            Undo
                          </Button>
                        </div>
                      ))}
                  </div>
                ) : null}
                {newAssets.length > 0 ? (
                  <div className="grid gap-2">
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                      Queued uploads
                    </p>
                    {newAssets.map((file, index) => (
                      <div
                        key={`${file.name}-${file.size}-${index}`}
                        className="flex items-center justify-between gap-3 rounded-xl border border-white/10 bg-black/20 px-4 py-3"
                      >
                        <div className="min-w-0">
                          <p className="truncate text-sm text-white">{file.name}</p>
                          <p className="mt-1 text-xs text-[var(--muted-foreground)]">
                            {formatIssueAssetSize(file.size)} · {file.type || "Detected on upload"}
                          </p>
                        </div>
                        <Button
                          type="button"
                          variant="secondary"
                          onClick={() =>
                            setNewAssets((current) =>
                              current.filter((_, currentIndex) => currentIndex !== index),
                            )
                          }
                        >
                          Remove
                        </Button>
                      </div>
                    ))}
                  </div>
                ) : null}
              </div>
            )}
          </Field>
          <div className="flex justify-end gap-3">
            <Button variant="secondary" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              disabled={!projectID || !title.trim() || pending || (issueType === "recurring" && !cron.trim())}
              onClick={async () => {
                setPending(true);
                try {
                  const body: Record<string, unknown> = {
                    project_id: projectID,
                    epic_id: epicID,
                    title,
                    description,
                    state,
                    issue_type: issueType,
                    priority: Number(priority),
                    labels,
                    agent_name: agentName,
                    agent_prompt: agentPrompt,
                    blocked_by: blockedBy,
                    branch_name: branchName,
                    pr_url: prURL,
                  };
                  if (issueType === "recurring") {
                    body.cron = cron;
                    body.enabled = enabled;
                  }
                  await onSubmit(body, {
                    newAssets,
                    removeAssetIDs: removedAssetIDs,
                  });
                  onOpenChange(false);
                } finally {
                  setPending(false);
                }
              }}
            >
              {pending ? "Saving…" : isEditing ? "Update issue" : "Create issue"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
