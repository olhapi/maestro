import type {
  BootstrapResponse,
  Epic,
  EpicDetailResponse,
  EpicSummary,
  IssueDetail,
  IssueExecutionDetail,
  IssueSummary,
  Project,
  ProjectDetailResponse,
  ProjectSummary,
  RuntimeEvent,
  RuntimeSeriesPoint,
  Session,
} from '@/lib/types'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
    ...init,
  })
  if (!response.ok) {
    const payload = (await response.json().catch(() => ({}))) as { error?: string }
    throw new Error(payload.error ?? `Request failed: ${response.status}`)
  }
  return response.json() as Promise<T>
}

export interface IssueFilters {
  project_id?: string
  epic_id?: string
  state?: string
  search?: string
  sort?: string
  limit?: number
  offset?: number
}

export interface ProjectInput {
  name: string
  description?: string
  repo_path: string
  workflow_path?: string
  provider_kind?: string
  provider_project_ref?: string
  provider_config?: Record<string, unknown>
}

export const api = {
  bootstrap: () => request<BootstrapResponse>('/api/v1/app/bootstrap'),
  listProjects: () => request<{ items: ProjectSummary[] }>('/api/v1/app/projects'),
  getProject: (id: string) => request<ProjectDetailResponse>(`/api/v1/app/projects/${id}`),
  createProject: (body: ProjectInput) =>
    request<Project>('/api/v1/app/projects', { method: 'POST', body: JSON.stringify(body) }),
  updateProject: (id: string, body: ProjectInput) =>
    request<Project>(`/api/v1/app/projects/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteProject: (id: string) => request<{ deleted: boolean }>(`/api/v1/app/projects/${id}`, { method: 'DELETE' }),
  listEpics: (projectID?: string) =>
    request<{ items: EpicSummary[] }>(`/api/v1/app/epics${projectID ? `?project_id=${projectID}` : ''}`),
  getEpic: (id: string) => request<EpicDetailResponse>(`/api/v1/app/epics/${id}`),
  createEpic: (body: { project_id: string; name: string; description?: string }) =>
    request<Epic>('/api/v1/app/epics', { method: 'POST', body: JSON.stringify(body) }),
  updateEpic: (id: string, body: { project_id: string; name: string; description?: string }) =>
    request<Epic>(`/api/v1/app/epics/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteEpic: (id: string) => request<{ deleted: boolean }>(`/api/v1/app/epics/${id}`, { method: 'DELETE' }),
  listIssues: (filters: IssueFilters) => {
    const query = new URLSearchParams()
    Object.entries(filters).forEach(([key, value]) => {
      if (value !== undefined && value !== '') query.set(key, String(value))
    })
    const suffix = query.toString() ? `?${query.toString()}` : ''
    return request<{ items: IssueSummary[]; total: number; limit: number; offset: number }>(
      `/api/v1/app/issues${suffix}`,
    )
  },
  createIssue: (body: Record<string, unknown>) =>
    request<IssueDetail>('/api/v1/app/issues', { method: 'POST', body: JSON.stringify(body) }),
  getIssue: (identifier: string) => request<IssueDetail>(`/api/v1/app/issues/${identifier}`),
  getIssueExecution: (identifier: string) => request<IssueExecutionDetail>(`/api/v1/app/issues/${identifier}/execution`),
  updateIssue: (identifier: string, body: Record<string, unknown>) =>
    request<IssueDetail>(`/api/v1/app/issues/${identifier}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),
  deleteIssue: (identifier: string) =>
    request<{ deleted: boolean }>(`/api/v1/app/issues/${identifier}`, { method: 'DELETE' }),
  setIssueState: (identifier: string, state: string) =>
    request<{ ok: boolean }>(`/api/v1/app/issues/${identifier}/state`, {
      method: 'POST',
      body: JSON.stringify({ state }),
    }),
  setIssueBlockers: (identifier: string, blockedBy: string[]) =>
    request<{ ok: boolean }>(`/api/v1/app/issues/${identifier}/blockers`, {
      method: 'POST',
      body: JSON.stringify({ blocked_by: blockedBy }),
    }),
  retryIssue: (identifier: string) =>
    request<Record<string, unknown>>(`/api/v1/app/issues/${identifier}/retry`, { method: 'POST' }),
  listRuntimeEvents: () => request<{ events: RuntimeEvent[] }>('/api/v1/app/runtime/events?limit=50'),
  listRuntimeSeries: () => request<{ series: RuntimeSeriesPoint[] }>('/api/v1/app/runtime/series?hours=24'),
  listSessions: () => request<{ sessions: Record<string, Session> }>('/api/v1/app/sessions'),
  requestRefresh: () => request<Record<string, unknown>>('/api/v1/refresh', { method: 'POST' }),
}
