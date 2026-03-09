export type IssueState = string

export type WorkflowPhase = 'implementation' | 'review' | 'done' | 'complete'

export interface IssueStateCounts {
  backlog: number
  ready: number
  in_progress: number
  in_review: number
  done: number
  cancelled: number
}

export interface ProviderCapabilities {
  projects: boolean
  epics: boolean
  issues: boolean
  issue_state_update: boolean
  issue_delete: boolean
}

export interface StateBucket {
  state: string
  count: number
  is_active?: boolean
  is_terminal?: boolean
}

export interface Project {
  id: string
  name: string
  description?: string
  repo_path?: string
  workflow_path?: string
  provider_kind?: string
  provider_project_ref?: string
  provider_config?: Record<string, unknown>
  capabilities: ProviderCapabilities
  orchestration_ready: boolean
  dispatch_ready?: boolean
  dispatch_error?: string
  created_at: string
  updated_at: string
}

export interface ProjectSummary extends Project {
  counts: IssueStateCounts
  state_buckets?: StateBucket[]
  total_count?: number
  active_count?: number
  terminal_count?: number
}

export interface ProjectDetailResponse {
  project: ProjectSummary
  epics: EpicSummary[]
  issues: {
    items: IssueSummary[]
    total: number
    limit: number
    offset: number
  }
}

export interface Epic {
  id: string
  project_id: string
  name: string
  description?: string
  created_at: string
  updated_at: string
}

export interface EpicSummary extends Epic {
  project_name?: string
  counts: IssueStateCounts
  state_buckets?: StateBucket[]
  total_count?: number
  active_count?: number
  terminal_count?: number
}

export interface EpicDetailResponse {
  epic: EpicSummary
  project?: Project
  sibling_epics: EpicSummary[]
  issues: {
    items: IssueSummary[]
    total: number
    limit: number
    offset: number
  }
}

export interface Issue {
  id: string
  project_id?: string
  epic_id?: string
  identifier: string
  provider_kind?: string
  provider_issue_ref?: string
  provider_shadow?: boolean
  title: string
  description?: string
  state: IssueState
  workflow_phase: WorkflowPhase
  priority: number
  labels?: string[]
  branch_name?: string
  pr_number?: number
  pr_url?: string
  blocked_by?: string[]
  created_at: string
  updated_at: string
  started_at?: string
  completed_at?: string
  last_synced_at?: string
}

export interface IssueSummary extends Issue {
  project_name?: string
  epic_name?: string
  workspace_path?: string
  workspace_run_count: number
  workspace_last_run?: string
  is_blocked: boolean
}

export interface IssueDetail extends IssueSummary {
  project_description?: string
  epic_description?: string
}

export interface TokenTotals {
  input_tokens: number
  output_tokens: number
  total_tokens: number
  seconds_running: number
}

export interface RunningEntry {
  issue_id: string
  identifier: string
  state: string
  phase?: string
  session_id?: string
  turn_count: number
  last_event?: string
  last_message?: string
  started_at: string
  last_event_at?: string
  tokens: TokenTotals
}

export interface RetryEntry {
  issue_id: string
  identifier: string
  phase?: string
  attempt: number
  due_at: string
  due_in_ms: number
  error?: string
  delay_type?: string
}

export interface Snapshot {
  generated_at: string
  running: RunningEntry[]
  retrying: RetryEntry[]
  codex_totals: TokenTotals
  workspace_root?: string
}

export interface RuntimeEvent {
  seq: number
  kind: string
  issue_id?: string
  identifier?: string
  title?: string
  phase?: string
  attempt?: number
  delay_type?: string
  input_tokens?: number
  output_tokens?: number
  total_tokens?: number
  error?: string
  ts: string
  payload?: Record<string, unknown>
}

export interface RuntimeSeriesPoint {
  bucket: string
  runs_started: number
  runs_completed: number
  runs_failed: number
  retries: number
  tokens: number
}

export interface SessionEvent {
  type: string
  thread_id: string
  turn_id: string
  input_tokens: number
  output_tokens: number
  total_tokens: number
  message: string
}

export interface Session {
  issue_id?: string
  issue_identifier?: string
  session_id: string
  thread_id: string
  turn_id: string
  codex_app_server_pid?: number
  last_event: string
  last_timestamp: string
  last_message?: string
  input_tokens: number
  output_tokens: number
  total_tokens: number
  events_processed: number
  turns_started: number
  turns_completed: number
  terminal: boolean
  terminal_reason?: string
  history?: SessionEvent[]
}

export interface IssueExecutionDetail {
  issue_id: string
  identifier: string
  active: boolean
  phase: string
  attempt_number: number
  failure_class?: string
  current_error?: string
  retry_state: 'none' | 'active' | 'scheduled'
  next_retry_at?: string
  session_source: 'none' | 'live' | 'persisted'
  session?: Session
  runtime_events: RuntimeEvent[]
}

export interface BootstrapResponse {
  generated_at: string
  overview: {
    status: Record<string, unknown>
    snapshot: Snapshot
    board: IssueStateCounts
    project_count: number
    epic_count: number
    issue_count: number
    series: RuntimeSeriesPoint[]
    recent_events: RuntimeEvent[]
  }
  projects: ProjectSummary[]
  epics: EpicSummary[]
  issues: {
    items: IssueSummary[]
    total: number
    limit: number
    offset: number
  }
  sessions: {
    sessions: Record<string, Session>
  }
}
