import { type ReactNode, useEffect, useId, useMemo, useState } from "react";

import { MultiCombobox, type MultiComboboxOption } from "@/components/ui/multi-combobox";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { api } from "@/lib/api";
import { getStateMeta, issueStates } from "@/lib/dashboard";
import type { EpicSummary, IssueDetail, IssueSummary, IssueType, ProjectSummary } from "@/lib/types";

const noEpicValue = "__no-epic__";

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

async function loadProjectIssues(projectID: string) {
  if (!projectID) return [];

  const items: IssueSummary[] = [];
  let offset = 0;

  for (;;) {
    const page = await api.listIssues({
      project_id: projectID,
      limit: 200,
      offset,
      sort: "identifier_asc",
    });
    items.push(...page.items);
    offset += page.items.length;
    if (items.length >= page.total || page.items.length === 0) {
      return items;
    }
  }
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
    provider_kind?: string;
    provider_project_ref?: string;
    provider_config?: Record<string, unknown>;
  }) => Promise<void>;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [repoPath, setRepoPath] = useState(initial?.repo_path ?? "");
  const [workflowPath, setWorkflowPath] = useState(initial?.workflow_path ?? "");
  const [providerKind, setProviderKind] = useState(initial?.provider_kind ?? "kanban");
  const [providerProjectRef, setProviderProjectRef] = useState(initial?.provider_project_ref ?? "");
  const [providerEndpoint, setProviderEndpoint] = useState(String(initial?.provider_config?.endpoint ?? ""));
  const [providerAssignee, setProviderAssignee] = useState(String(initial?.provider_config?.assignee ?? ""));
  const [pending, setPending] = useState(false);

  useEffect(() => {
    setName(initial?.name ?? "");
    setDescription(initial?.description ?? "");
    setRepoPath(initial?.repo_path ?? "");
    setWorkflowPath(initial?.workflow_path ?? "");
    setProviderKind(initial?.provider_kind ?? "kanban");
    setProviderProjectRef(initial?.provider_project_ref ?? "");
    setProviderEndpoint(String(initial?.provider_config?.endpoint ?? ""));
    setProviderAssignee(String(initial?.provider_config?.assignee ?? ""));
  }, [initial, open]);

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
            <Field label="Provider">
              {({ labelId }) => (
                <Select value={providerKind} onValueChange={setProviderKind}>
                  <SelectTrigger aria-labelledby={labelId}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="kanban">kanban</SelectItem>
                    <SelectItem value="linear">linear</SelectItem>
                  </SelectContent>
                </Select>
              )}
            </Field>
            <Field label="Provider project ref">
              {({ labelId }) => (
                <Input
                  aria-labelledby={labelId}
                  value={providerProjectRef}
                  onChange={(event) => setProviderProjectRef(event.target.value)}
                  placeholder={providerKind === "linear" ? "Linear project slug" : "Optional provider project ref"}
                />
              )}
            </Field>
            <Field label="Provider endpoint">
              {({ labelId }) => (
                <Input
                  aria-labelledby={labelId}
                  value={providerEndpoint}
                  onChange={(event) => setProviderEndpoint(event.target.value)}
                  placeholder="Optional API endpoint override"
                />
              )}
            </Field>
            <Field label="Provider assignee">
              {({ labelId }) => (
                <Input
                  aria-labelledby={labelId}
                  value={providerAssignee}
                  onChange={(event) => setProviderAssignee(event.target.value)}
                  placeholder={
                    providerKind === "linear" ? "Optional assignee ID or 'me'" : "Optional provider assignee filter"
                  }
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
                  const providerConfig: Record<string, unknown> = { ...(initial?.provider_config ?? {}) };
                  if (providerEndpoint) {
                    providerConfig.endpoint = providerEndpoint;
                  } else {
                    delete providerConfig.endpoint;
                  }
                  if (providerAssignee) {
                    providerConfig.assignee = providerAssignee;
                  } else {
                    delete providerConfig.assignee;
                  }
                  await onSubmit({
                    name,
                    description,
                    repo_path: repoPath,
                    workflow_path: workflowPath || undefined,
                    provider_kind: providerKind,
                    provider_project_ref: providerProjectRef || undefined,
                    provider_config: Object.keys(providerConfig).length > 0 ? providerConfig : undefined,
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

  useEffect(() => {
    setProjectID(initial?.project_id ?? projects[0]?.id ?? "");
    setName(initial?.name ?? "");
    setDescription(initial?.description ?? "");
  }, [initial, open, projects]);

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
                <Select value={projectID || undefined} onValueChange={setProjectID}>
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
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initial?: Partial<IssueDetail>;
  projects: ProjectSummary[];
  epics: EpicSummary[];
  onSubmit: (body: Record<string, unknown>) => Promise<void>;
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
  const [blockedBy, setBlockedBy] = useState(initial?.blocked_by ?? []);
  const [branchName, setBranchName] = useState(initial?.branch_name ?? "");
  const [prURL, setPrURL] = useState(initial?.pr_url ?? "");
  const [projectIssues, setProjectIssues] = useState<IssueSummary[]>([]);
  const [loadingProjectIssues, setLoadingProjectIssues] = useState(false);
  const [pending, setPending] = useState(false);
  const selectedProject = projects.find((project) => project.id === projectID);
  const supportsEpics = selectedProject?.capabilities?.epics ?? true;
  const canChangeIssueType = !isEditing || initial?.provider_kind === "kanban";

  useEffect(() => {
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
    setBlockedBy(initial?.blocked_by ?? []);
    setBranchName(initial?.branch_name ?? "");
    setPrURL(initial?.pr_url ?? "");
  }, [initial, open, projects]);

  useEffect(() => {
    if (!open || !projectID) {
      setProjectIssues([]);
      setLoadingProjectIssues(false);
      return;
    }

    let active = true;

    setLoadingProjectIssues(true);
    loadProjectIssues(projectID)
      .then((items) => {
        if (!active) return;
        setProjectIssues(items);
      })
      .catch(() => {
        if (!active) return;
        setProjectIssues([]);
      })
      .finally(() => {
        if (active) {
          setLoadingProjectIssues(false);
        }
      });

    return () => {
      active = false;
    };
  }, [open, projectID]);

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
    for (const issue of projectIssues) {
      for (const label of issue.labels ?? []) {
        const trimmed = label.trim();
        if (trimmed) {
          unique.add(trimmed);
        }
      }
    }
    return [...unique].sort((left, right) => left.localeCompare(right)).map((label) => ({ value: label, label }));
  }, [projectIssues]);

  const blockerOptions = useMemo<MultiComboboxOption[]>(
    () =>
      projectIssues
        .filter((issue) => issue.identifier !== initial?.identifier)
        .map((issue) => ({
          value: issue.identifier,
          label: issueOptionLabel(issue),
          keywords: [issue.identifier, issue.title],
        })),
    [initial?.identifier, projectIssues],
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
                  value={projectID || undefined}
                  onValueChange={(nextProjectID) => {
                    setProjectID(nextProjectID);
                    if (nextProjectID !== projectID) {
                      setBlockedBy([]);
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
                  labelledBy={labelId}
                  value={labels}
                  onChange={setLabels}
                  options={labelOptions}
                  loading={loadingProjectIssues}
                  allowCreate
                  placeholder="Select or create labels"
                  emptyText="No labels found."
                  createLabel={(value) => `Create label "${value}"`}
                />
              )}
            </Field>
            <Field label="Blockers">
              {({ labelId }) => (
                <MultiCombobox
                  labelledBy={labelId}
                  value={blockedBy}
                  onChange={setBlockedBy}
                  options={blockerOptions}
                  loading={loadingProjectIssues}
                  placeholder="Select blocker issues"
                  emptyText={projectID ? "No blockers available in this project." : "Select a project first."}
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
          <Field label="Description">
            {({ labelId }) => (
              <Textarea
                aria-labelledby={labelId}
                value={description}
                onChange={(event) => setDescription(event.target.value)}
                className="min-h-[180px]"
              />
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
                    blocked_by: blockedBy,
                    branch_name: branchName,
                    pr_url: prURL,
                  };
                  if (issueType === "recurring") {
                    body.cron = cron;
                    body.enabled = enabled;
                  }
                  await onSubmit(body);
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
