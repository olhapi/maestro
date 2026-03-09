import { type ReactNode, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { getStateMeta, issueStates } from '@/lib/dashboard'
import type { EpicSummary, IssueDetail, ProjectSummary } from '@/lib/types'

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="grid gap-2">
      <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{label}</span>
      {children}
    </label>
  )
}

export function ProjectDialog({
  open,
  onOpenChange,
  initial,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initial?: Partial<ProjectSummary>
  onSubmit: (body: {
    name: string
    description?: string
    repo_path: string
    workflow_path?: string
    provider_kind?: string
    provider_project_ref?: string
    provider_config?: Record<string, unknown>
  }) => Promise<void>
}) {
  const [name, setName] = useState(initial?.name ?? '')
  const [description, setDescription] = useState(initial?.description ?? '')
  const [repoPath, setRepoPath] = useState(initial?.repo_path ?? '')
  const [workflowPath, setWorkflowPath] = useState(initial?.workflow_path ?? '')
  const [providerKind, setProviderKind] = useState(initial?.provider_kind ?? 'kanban')
  const [providerProjectRef, setProviderProjectRef] = useState(initial?.provider_project_ref ?? '')
  const [providerEndpoint, setProviderEndpoint] = useState(String(initial?.provider_config?.endpoint ?? ''))
  const [providerAssignee, setProviderAssignee] = useState(String(initial?.provider_config?.assignee ?? ''))
  const [pending, setPending] = useState(false)

  useEffect(() => {
    setName(initial?.name ?? '')
    setDescription(initial?.description ?? '')
    setRepoPath(initial?.repo_path ?? '')
    setWorkflowPath(initial?.workflow_path ?? '')
    setProviderKind(initial?.provider_kind ?? 'kanban')
    setProviderProjectRef(initial?.provider_project_ref ?? '')
    setProviderEndpoint(String(initial?.provider_config?.endpoint ?? ''))
    setProviderAssignee(String(initial?.provider_config?.assignee ?? ''))
  }, [initial, open])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <div className="space-y-6">
          <div>
            <DialogTitle className="text-xl font-semibold text-white">{initial ? 'Edit project' : 'Create project'}</DialogTitle>
            <DialogDescription className="mt-2 text-sm text-[var(--muted-foreground)]">
              Manage the top-level portfolio containers for Maestro work.
            </DialogDescription>
          </div>
          <div className="grid gap-4">
            <Field label="Project name">
              <Input value={name} onChange={(event) => setName(event.target.value)} />
            </Field>
            <Field label="Description">
              <Textarea value={description} onChange={(event) => setDescription(event.target.value)} />
            </Field>
            <Field label="Repo path">
              <Input value={repoPath} onChange={(event) => setRepoPath(event.target.value)} placeholder="/absolute/path/to/repo" />
            </Field>
            <Field label="Workflow path override">
              <Input value={workflowPath} onChange={(event) => setWorkflowPath(event.target.value)} placeholder="Optional; defaults to <repo>/WORKFLOW.md" />
            </Field>
            <Field label="Provider">
              <Select value={providerKind} onChange={(event) => setProviderKind(event.target.value)}>
                <option value="kanban">kanban</option>
                <option value="linear">linear</option>
              </Select>
            </Field>
            <Field label="Provider project ref">
              <Input
                value={providerProjectRef}
                onChange={(event) => setProviderProjectRef(event.target.value)}
                placeholder={providerKind === 'linear' ? 'Linear project slug' : 'Optional provider project ref'}
              />
            </Field>
            <Field label="Provider endpoint">
              <Input value={providerEndpoint} onChange={(event) => setProviderEndpoint(event.target.value)} placeholder="Optional API endpoint override" />
            </Field>
            <Field label="Provider assignee">
              <Input
                value={providerAssignee}
                onChange={(event) => setProviderAssignee(event.target.value)}
                placeholder={providerKind === 'linear' ? "Optional assignee ID or 'me'" : 'Optional provider assignee filter'}
              />
            </Field>
          </div>
          <div className="flex justify-end gap-3">
            <Button variant="secondary" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              disabled={!name.trim() || !repoPath.trim() || pending}
              onClick={async () => {
                setPending(true)
                try {
                  const providerConfig: Record<string, unknown> = { ...(initial?.provider_config ?? {}) }
                  if (providerEndpoint) {
                    providerConfig.endpoint = providerEndpoint
                  } else {
                    delete providerConfig.endpoint
                  }
                  if (providerAssignee) {
                    providerConfig.assignee = providerAssignee
                  } else {
                    delete providerConfig.assignee
                  }
                  await onSubmit({
                    name,
                    description,
                    repo_path: repoPath,
                    workflow_path: workflowPath || undefined,
                    provider_kind: providerKind,
                    provider_project_ref: providerProjectRef || undefined,
                    provider_config: Object.keys(providerConfig).length > 0 ? providerConfig : undefined,
                  })
                  onOpenChange(false)
                } finally {
                  setPending(false)
                }
              }}
            >
              {pending ? 'Saving…' : initial ? 'Update project' : 'Create project'}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

export function EpicDialog({
  open,
  onOpenChange,
  initial,
  projects,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initial?: Partial<EpicSummary>
  projects: ProjectSummary[]
  onSubmit: (body: { project_id: string; name: string; description?: string }) => Promise<void>
}) {
  const [projectID, setProjectID] = useState(initial?.project_id ?? '')
  const [name, setName] = useState(initial?.name ?? '')
  const [description, setDescription] = useState(initial?.description ?? '')
  const [pending, setPending] = useState(false)

  useEffect(() => {
    setProjectID(initial?.project_id ?? projects[0]?.id ?? '')
    setName(initial?.name ?? '')
    setDescription(initial?.description ?? '')
  }, [initial, open, projects])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <div className="space-y-6">
          <div>
            <DialogTitle className="text-xl font-semibold text-white">{initial ? 'Edit epic' : 'Create epic'}</DialogTitle>
            <DialogDescription className="mt-2 text-sm text-[var(--muted-foreground)]">
              Group related issues under a focused delivery arc.
            </DialogDescription>
          </div>
          <div className="grid gap-4">
            <Field label="Project">
              <Select value={projectID} onChange={(event) => setProjectID(event.target.value)}>
                <option value="">Select project</option>
                {projects.map((project) => (
                  <option key={project.id} value={project.id}>
                    {project.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Epic name">
              <Input value={name} onChange={(event) => setName(event.target.value)} />
            </Field>
            <Field label="Description">
              <Textarea value={description} onChange={(event) => setDescription(event.target.value)} />
            </Field>
          </div>
          <div className="flex justify-end gap-3">
            <Button variant="secondary" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              disabled={!name.trim() || !projectID || pending}
              onClick={async () => {
                setPending(true)
                try {
                  await onSubmit({ project_id: projectID, name, description })
                  onOpenChange(false)
                } finally {
                  setPending(false)
                }
              }}
            >
              {pending ? 'Saving…' : initial ? 'Update epic' : 'Create epic'}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

export function IssueDialog({
  open,
  onOpenChange,
  initial,
  projects,
  epics,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initial?: Partial<IssueDetail>
  projects: ProjectSummary[]
  epics: EpicSummary[]
  onSubmit: (body: Record<string, unknown>) => Promise<void>
}) {
  const isEditing = Boolean(initial?.identifier)
  const defaultProjectID = initial?.project_id ?? projects[0]?.id ?? ''
  const [projectID, setProjectID] = useState(defaultProjectID)
  const [epicID, setEpicID] = useState(initial?.epic_id ?? '')
  const [title, setTitle] = useState(initial?.title ?? '')
  const [description, setDescription] = useState(initial?.description ?? '')
  const [state, setState] = useState<string>(initial?.state ?? 'backlog')
  const [priority, setPriority] = useState(String(initial?.priority ?? 0))
  const [labels, setLabels] = useState(initial?.labels?.join(', ') ?? '')
  const [blockedBy, setBlockedBy] = useState(initial?.blocked_by?.join(', ') ?? '')
  const [branchName, setBranchName] = useState(initial?.branch_name ?? '')
  const [prNumber, setPrNumber] = useState(String(initial?.pr_number ?? 0))
  const [prURL, setPrURL] = useState(initial?.pr_url ?? '')
  const [pending, setPending] = useState(false)
  const selectedProject = projects.find((project) => project.id === projectID)
  const supportsEpics = selectedProject?.capabilities?.epics ?? true

  useEffect(() => {
    setProjectID(initial?.project_id ?? projects[0]?.id ?? '')
    setEpicID(initial?.epic_id ?? '')
    setTitle(initial?.title ?? '')
    setDescription(initial?.description ?? '')
    setState(initial?.state ?? 'backlog')
    setPriority(String(initial?.priority ?? 0))
    setLabels(initial?.labels?.join(', ') ?? '')
    setBlockedBy(initial?.blocked_by?.join(', ') ?? '')
    setBranchName(initial?.branch_name ?? '')
    setPrNumber(String(initial?.pr_number ?? 0))
    setPrURL(initial?.pr_url ?? '')
  }, [initial, open, projects])

  const filteredEpics = epics.filter((epic) => !projectID || epic.project_id === projectID)

  useEffect(() => {
    if (!epicID) return
    if (!filteredEpics.some((epic) => epic.id === epicID)) {
      setEpicID('')
    }
  }, [epicID, filteredEpics])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="w-[min(96vw,920px)]">
        <div className="space-y-6">
          <div>
            <DialogTitle className="text-xl font-semibold text-white">{isEditing ? `Edit ${initial?.identifier}` : 'Create issue'}</DialogTitle>
            <DialogDescription className="mt-2 text-sm text-[var(--muted-foreground)]">
              Shape the issue, set operational metadata, and make it immediately actionable.
            </DialogDescription>
          </div>
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Project">
              <Select value={projectID} onChange={(event) => setProjectID(event.target.value)}>
                <option value="">No project</option>
                {projects.map((project) => (
                  <option key={project.id} value={project.id}>
                    {project.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Epic">
              <Select disabled={!supportsEpics} value={epicID} onChange={(event) => setEpicID(event.target.value)}>
                <option value="">No epic</option>
                {filteredEpics.map((epic) => (
                  <option key={epic.id} value={epic.id}>
                    {epic.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Title">
              <Input value={title} onChange={(event) => setTitle(event.target.value)} />
            </Field>
            <Field label="State">
              <Select value={state} onChange={(event) => setState(event.target.value)}>
                {[...new Set([state, ...issueStates])].map((value) => (
                  <option key={value} value={value}>
                    {getStateMeta(value).label}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Priority">
              <Input type="number" min={0} value={priority} onChange={(event) => setPriority(event.target.value)} />
            </Field>
            <Field label="Labels">
              <Input value={labels} onChange={(event) => setLabels(event.target.value)} placeholder="bug, api, urgent" />
            </Field>
            <Field label="Blockers">
              <Input value={blockedBy} onChange={(event) => setBlockedBy(event.target.value)} placeholder="ISS-1, ISS-2" />
            </Field>
            <Field label="Branch">
              <Input value={branchName} onChange={(event) => setBranchName(event.target.value)} />
            </Field>
            <Field label="PR number">
              <Input type="number" min={0} value={prNumber} onChange={(event) => setPrNumber(event.target.value)} />
            </Field>
            <Field label="PR URL">
              <Input value={prURL} onChange={(event) => setPrURL(event.target.value)} />
            </Field>
          </div>
          <Field label="Description">
            <Textarea value={description} onChange={(event) => setDescription(event.target.value)} className="min-h-[180px]" />
          </Field>
          <div className="flex justify-end gap-3">
            <Button variant="secondary" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              disabled={!title.trim() || pending}
              onClick={async () => {
                setPending(true)
                try {
                  await onSubmit({
                    project_id: projectID,
                    epic_id: epicID,
                    title,
                    description,
                    state,
                    priority: Number(priority),
                    labels: labels
                      .split(',')
                      .map((value) => value.trim())
                      .filter(Boolean),
                    blocked_by: blockedBy
                      .split(',')
                      .map((value) => value.trim())
                      .filter(Boolean),
                    branch_name: branchName,
                    pr_number: Number(prNumber),
                    pr_url: prURL,
                  })
                  onOpenChange(false)
                } finally {
                  setPending(false)
                }
              }}
            >
              {pending ? 'Saving…' : isEditing ? 'Update issue' : 'Create issue'}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
