import type {
  BootstrapResponse,
  Epic,
  EpicDetailResponse,
  EpicSummary,
  IssueAsset,
  AgentCommand,
  IssueComment,
  IssueDetail,
  IssueExecutionDetail,
  PendingInterruptsResponse,
  IssueSummary,
  Project,
  ProjectDetailResponse,
  ProjectSummary,
  RuntimeEvent,
  RuntimeSeriesPoint,
  SessionsResponse,
  WorkBootstrapResponse,
} from "@/lib/types";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (!(init?.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(path, {
    ...init,
    headers,
  });
  if (!response.ok) {
    const payload = (await response.json().catch(() => ({}))) as {
      error?: string;
    };
    throw new Error(payload.error ?? `Request failed: ${response.status}`);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  const contentType = response.headers.get("Content-Type") ?? "";
  if (!contentType.includes("application/json")) {
    return undefined as T;
  }
  return response.json() as Promise<T>;
}

export interface IssueFilters {
  project_id?: string;
  epic_id?: string;
  state?: string;
  issue_type?: string;
  search?: string;
  sort?: string;
  limit?: number;
  offset?: number;
}

export interface ProjectInput {
  name: string;
  description?: string;
  repo_path: string;
  workflow_path?: string;
}

export const api = {
  bootstrap: () => request<BootstrapResponse>("/api/v1/app/bootstrap"),
  workBootstrap: () => request<WorkBootstrapResponse>("/api/v1/app/work"),
  listProjects: () =>
    request<{ items: ProjectSummary[] }>("/api/v1/app/projects"),
  getProject: (id: string) =>
    request<ProjectDetailResponse>(`/api/v1/app/projects/${id}`),
  createProject: (body: ProjectInput) =>
    request<Project>("/api/v1/app/projects", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  updateProject: (id: string, body: ProjectInput) =>
    request<Project>(`/api/v1/app/projects/${id}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  deleteProject: (id: string) =>
    request<{ deleted: boolean }>(`/api/v1/app/projects/${id}`, {
      method: "DELETE",
    }),
  runProject: (id: string) =>
    request<Record<string, unknown>>(`/api/v1/app/projects/${id}/run`, {
      method: "POST",
    }),
  stopProject: (id: string) =>
    request<Record<string, unknown>>(`/api/v1/app/projects/${id}/stop`, {
      method: "POST",
    }),
  setProjectPermissionProfile: (id: string, permissionProfile: string) =>
    request<Project>(`/api/v1/app/projects/${id}/permissions`, {
      method: "POST",
      body: JSON.stringify({ permission_profile: permissionProfile }),
    }),
  listEpics: (projectID?: string) =>
    request<{ items: EpicSummary[] }>(
      `/api/v1/app/epics${projectID ? `?project_id=${projectID}` : ""}`,
    ),
  getEpic: (id: string) =>
    request<EpicDetailResponse>(`/api/v1/app/epics/${id}`),
  createEpic: (body: {
    project_id: string;
    name: string;
    description?: string;
  }) =>
    request<Epic>("/api/v1/app/epics", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  updateEpic: (
    id: string,
    body: { project_id: string; name: string; description?: string },
  ) =>
    request<Epic>(`/api/v1/app/epics/${id}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  deleteEpic: (id: string) =>
    request<{ deleted: boolean }>(`/api/v1/app/epics/${id}`, {
      method: "DELETE",
    }),
  listIssues: (filters: IssueFilters, init?: RequestInit) => {
    const query = new URLSearchParams();
    Object.entries(filters).forEach(([key, value]) => {
      if (value !== undefined && value !== "") query.set(key, String(value));
    });
    const suffix = query.toString() ? `?${query.toString()}` : "";
    return request<{
      items: IssueSummary[];
      total: number;
      limit: number;
      offset: number;
    }>(`/api/v1/app/issues${suffix}`, init);
  },
  createIssue: (body: Record<string, unknown>) =>
    request<IssueDetail>("/api/v1/app/issues", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  getIssue: (identifier: string) =>
    request<IssueDetail>(`/api/v1/app/issues/${identifier}`),
  listIssueComments: (identifier: string) =>
    request<{ items: IssueComment[] }>(`/api/v1/app/issues/${identifier}/comments`),
  getIssueExecution: (identifier: string) =>
    request<IssueExecutionDetail>(`/api/v1/app/issues/${identifier}/execution`),
  updateIssue: (identifier: string, body: Record<string, unknown>) =>
    request<IssueDetail>(`/api/v1/app/issues/${identifier}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  uploadIssueAsset: (identifier: string, file: File) => {
    const formData = new FormData();
    formData.set("file", file);
    return request<IssueAsset>(`/api/v1/app/issues/${identifier}/assets`, {
      method: "POST",
      body: formData,
    });
  },
  deleteIssueAsset: (identifier: string, assetID: string) =>
    request<{ deleted: boolean; identifier: string; asset_id: string }>(
      `/api/v1/app/issues/${identifier}/assets/${assetID}`,
      {
        method: "DELETE",
      },
    ),
  createIssueComment: (identifier: string, body: {
    body?: string;
    parent_comment_id?: string;
    files?: File[];
  }) => {
    const formData = new FormData();
    if (body.body !== undefined) {
      formData.set("body", body.body);
    }
    if (body.parent_comment_id) {
      formData.set("parent_comment_id", body.parent_comment_id);
    }
    for (const file of body.files ?? []) {
      formData.append("files", file);
    }
    return request<IssueComment>(`/api/v1/app/issues/${identifier}/comments`, {
      method: "POST",
      body: formData,
    });
  },
  updateIssueComment: (identifier: string, commentID: string, body: {
    body?: string;
    files?: File[];
    remove_attachment_ids?: string[];
  }) => {
    const formData = new FormData();
    if (body.body !== undefined) {
      formData.set("body", body.body);
    }
    for (const file of body.files ?? []) {
      formData.append("files", file);
    }
    for (const attachmentID of body.remove_attachment_ids ?? []) {
      formData.append("remove_attachment_ids", attachmentID);
    }
    return request<IssueComment>(`/api/v1/app/issues/${identifier}/comments/${commentID}`, {
      method: "PATCH",
      body: formData,
    });
  },
  deleteIssueComment: (identifier: string, commentID: string) =>
    request<{ deleted: boolean; identifier: string; comment_id: string }>(
      `/api/v1/app/issues/${identifier}/comments/${commentID}`,
      {
        method: "DELETE",
      },
    ),
  deleteIssue: (identifier: string) =>
    request<{ deleted: boolean }>(`/api/v1/app/issues/${identifier}`, {
      method: "DELETE",
    }),
  setIssueState: (identifier: string, state: string) =>
    request<{ ok: boolean }>(`/api/v1/app/issues/${identifier}/state`, {
      method: "POST",
      body: JSON.stringify({ state }),
    }),
  setIssuePermissionProfile: (identifier: string, permissionProfile: string) =>
    request<IssueDetail>(`/api/v1/app/issues/${identifier}/permissions`, {
      method: "POST",
      body: JSON.stringify({ permission_profile: permissionProfile }),
    }),
  approveIssuePlan: (identifier: string, note?: string) =>
    request<Record<string, unknown>>(`/api/v1/app/issues/${identifier}/approve-plan`, {
      method: "POST",
      body: JSON.stringify({ note }),
    }),
  requestIssuePlanRevision: (identifier: string, note: string) =>
    request<Record<string, unknown>>(`/api/v1/app/issues/${identifier}/request-plan-revision`, {
      method: "POST",
      body: JSON.stringify({ note }),
    }),
  setIssueBlockers: (identifier: string, blockedBy: string[]) =>
    request<{ ok: boolean }>(`/api/v1/app/issues/${identifier}/blockers`, {
      method: "POST",
      body: JSON.stringify({ blocked_by: blockedBy }),
    }),
  sendIssueCommand: (identifier: string, command: string) =>
    request<{ ok: boolean }>(`/api/v1/app/issues/${identifier}/commands`, {
      method: "POST",
      body: JSON.stringify({ command }),
    }),
  steerIssueCommand: (identifier: string, commandID: string) =>
    request<{ ok: boolean; command: AgentCommand }>(`/api/v1/app/issues/${identifier}/commands/${commandID}/steer`, {
      method: "POST",
    }),
  updateIssueCommand: (identifier: string, commandID: string, command: string) =>
    request<{ ok: boolean; command: AgentCommand }>(`/api/v1/app/issues/${identifier}/commands/${commandID}`, {
      method: "PATCH",
      body: JSON.stringify({ command }),
    }),
  deleteIssueCommand: (identifier: string, commandID: string) =>
    request<{ ok: boolean; deleted: boolean; command_id: string }>(
      `/api/v1/app/issues/${identifier}/commands/${commandID}`,
      {
        method: "DELETE",
      },
    ),
  retryIssue: (identifier: string) =>
    request<Record<string, unknown>>(`/api/v1/app/issues/${identifier}/retry`, {
      method: "POST",
    }),
  runIssueNow: (identifier: string) =>
    request<Record<string, unknown>>(`/api/v1/app/issues/${identifier}/run-now`, {
      method: "POST",
    }),
  listRuntimeEvents: () =>
    request<{ events: RuntimeEvent[] }>("/api/v1/app/runtime/events?limit=50"),
  listRuntimeSeries: () =>
    request<{ series: RuntimeSeriesPoint[] }>(
      "/api/v1/app/runtime/series?hours=24",
    ),
  listInterrupts: () =>
    request<PendingInterruptsResponse>("/api/v1/app/interrupts"),
  acknowledgeInterrupt: (id: string) =>
    request<{ id: string; status: string }>(`/api/v1/app/interrupts/${id}/acknowledge`, {
      method: "POST",
      body: JSON.stringify({}),
    }),
  respondToInterrupt: (
    id: string,
    body: {
      decision?: string;
      decision_payload?: Record<string, unknown>;
      answers?: Record<string, string[]>;
      note?: string;
      action?: "accept" | "decline" | "cancel";
      content?: unknown;
    },
  ) =>
    request<{ id: string; status: string }>(`/api/v1/app/interrupts/${id}/respond`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  listSessions: () => request<SessionsResponse>("/api/v1/app/sessions"),
  requestRefresh: () =>
    request<Record<string, unknown>>("/api/v1/refresh", { method: "POST" }),
};
