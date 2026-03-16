export type IssueState = string;
export type IssueType = "standard" | "recurring";
export type ProjectState = "stopped" | "running";
export type ProjectPermissionProfile = "default" | "full-access";

export type WorkflowPhase = "implementation" | "review" | "done" | "complete";

export interface IssueStateCounts {
  backlog: number;
  ready: number;
  in_progress: number;
  in_review: number;
  done: number;
  cancelled: number;
}

export interface ProviderCapabilities {
  projects: boolean;
  epics: boolean;
  issues: boolean;
  issue_state_update: boolean;
  issue_delete: boolean;
}

export interface StateBucket {
  state: string;
  count: number;
  is_active?: boolean;
  is_terminal?: boolean;
}

export interface Project {
  id: string;
  name: string;
  description?: string;
  state: ProjectState;
  permission_profile?: ProjectPermissionProfile;
  repo_path?: string;
  workflow_path?: string;
  provider_kind?: string;
  provider_project_ref?: string;
  provider_config?: Record<string, unknown>;
  capabilities: ProviderCapabilities;
  orchestration_ready: boolean;
  dispatch_ready?: boolean;
  dispatch_error?: string;
  created_at: string;
  updated_at: string;
}

export interface ProjectSummary extends Project {
  total_tokens_spent?: number;
  counts: IssueStateCounts;
  state_buckets?: StateBucket[];
  total_count?: number;
  active_count?: number;
  terminal_count?: number;
}

export interface ProjectDetailResponse {
  project: ProjectSummary;
  epics: EpicSummary[];
  issues: {
    items: IssueSummary[];
    total: number;
    limit: number;
    offset: number;
  };
}

export interface Epic {
  id: string;
  project_id: string;
  name: string;
  description?: string;
  created_at: string;
  updated_at: string;
}

export interface EpicSummary extends Epic {
  project_name?: string;
  counts: IssueStateCounts;
  state_buckets?: StateBucket[];
  total_count?: number;
  active_count?: number;
  terminal_count?: number;
}

export interface EpicDetailResponse {
  epic: EpicSummary;
  project?: Project;
  sibling_epics: EpicSummary[];
  issues: {
    items: IssueSummary[];
    total: number;
    limit: number;
    offset: number;
  };
}

export interface Issue {
  id: string;
  project_id?: string;
  epic_id?: string;
  identifier: string;
  issue_type?: IssueType;
  provider_kind?: string;
  provider_issue_ref?: string;
  provider_shadow?: boolean;
  title: string;
  description?: string;
  state: IssueState;
  workflow_phase: WorkflowPhase;
  priority: number;
  labels?: string[];
  agent_name?: string;
  agent_prompt?: string;
  branch_name?: string;
  pr_url?: string;
  blocked_by?: string[];
  created_at: string;
  updated_at: string;
  total_tokens_spent: number;
  started_at?: string;
  completed_at?: string;
  last_synced_at?: string;
  cron?: string;
  enabled?: boolean;
  next_run_at?: string;
  last_enqueued_at?: string;
  pending_rerun?: boolean;
}

export interface IssueSummary extends Issue {
  project_name?: string;
  epic_name?: string;
  workspace_path?: string;
  workspace_run_count: number;
  workspace_last_run?: string;
  is_blocked: boolean;
}

export interface IssueImage {
  id: string;
  issue_id: string;
  filename: string;
  content_type: string;
  byte_size: number;
  created_at: string;
  updated_at: string;
}

export interface IssueDetail extends IssueSummary {
  project_description?: string;
  epic_description?: string;
  images: IssueImage[];
}

export interface TokenTotals {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  seconds_running: number;
}

export interface RunningEntry {
  issue_id: string;
  identifier: string;
  state: string;
  phase?: string;
  session_id?: string;
  turn_count: number;
  last_event?: string;
  last_message?: string;
  started_at: string;
  last_event_at?: string;
  tokens: TokenTotals;
}

export interface RetryEntry {
  issue_id: string;
  identifier: string;
  phase?: string;
  attempt: number;
  due_at: string;
  due_in_ms: number;
  error?: string;
  delay_type?: string;
}

export interface PausedEntry {
  issue_id: string;
  identifier: string;
  phase?: string;
  attempt: number;
  paused_at: string;
  error?: string;
  consecutive_failures: number;
  pause_threshold: number;
}

export interface Snapshot {
  generated_at: string;
  running: RunningEntry[];
  retrying: RetryEntry[];
  paused: PausedEntry[];
  codex_totals: TokenTotals;
  workspace_root?: string;
}

export interface RuntimeEvent {
  seq: number;
  kind: string;
  issue_id?: string;
  identifier?: string;
  title?: string;
  phase?: string;
  attempt?: number;
  delay_type?: string;
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
  error?: string;
  ts: string;
  payload?: Record<string, unknown>;
}

export interface RuntimeSeriesPoint {
  bucket: string;
  runs_started: number;
  runs_completed: number;
  runs_failed: number;
  retries: number;
  tokens: number;
}

export interface SessionEvent {
  type: string;
  thread_id: string;
  turn_id: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  message: string;
}

export interface Session {
  issue_id?: string;
  issue_identifier?: string;
  session_id: string;
  thread_id: string;
  turn_id: string;
  codex_app_server_pid?: number;
  last_event: string;
  last_timestamp: string;
  last_message?: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  events_processed: number;
  turns_started: number;
  turns_completed: number;
  terminal: boolean;
  terminal_reason?: string;
  history?: SessionEvent[];
}

export interface PendingApprovalDecision {
  value: string;
  label: string;
  description?: string;
  decision_payload?: Record<string, unknown>;
}

export interface PendingApproval {
  command?: string;
  cwd?: string;
  reason?: string;
  decisions: PendingApprovalDecision[];
}

export interface PendingUserInputOption {
  label: string;
  description?: string;
}

export interface PendingUserInputQuestion {
  header?: string;
  id: string;
  question?: string;
  options?: PendingUserInputOption[];
  is_other?: boolean;
  is_secret?: boolean;
}

export interface PendingUserInput {
  questions: PendingUserInputQuestion[];
}

export interface PendingInterrupt {
  id: string;
  request_id?: string;
  kind: "approval" | "user_input";
  method?: string;
  issue_id?: string;
  issue_identifier?: string;
  issue_title?: string;
  phase?: string;
  attempt?: number;
  session_id?: string;
  thread_id?: string;
  turn_id?: string;
  item_id?: string;
  requested_at: string;
  last_activity_at?: string;
  last_activity?: string;
  collaboration_mode?: "plan" | "default";
  approval?: PendingApproval;
  user_input?: PendingUserInput;
}

export interface PendingInterruptsResponse {
  count: number;
  current?: PendingInterrupt;
}

export interface SessionFeedEntry {
  issue_id: string;
  issue_identifier: string;
  issue_title?: string;
  source: "live" | "persisted";
  active: boolean;
  status: "active" | "waiting" | "paused" | "completed" | "failed" | "interrupted";
  pending_interrupt?: PendingInterrupt;
  phase?: string;
  attempt?: number;
  run_kind?: string;
  failure_class?: string;
  updated_at: string;
  last_event?: string;
  last_message?: string;
  total_tokens: number;
  events_processed: number;
  turns_started: number;
  turns_completed: number;
  terminal: boolean;
  terminal_reason?: string;
  history?: SessionEvent[];
  error?: string;
}

export interface SessionsResponse {
  sessions: Record<string, Session>;
  entries: SessionFeedEntry[];
}

export interface ActivityEntry {
  id: string;
  kind: "agent" | "command" | "status" | "secondary";
  title: string;
  summary: string;
  detail?: string;
  expandable: boolean;
  item_type?: string;
  phase?: string;
  status?: string;
  tone?: "default" | "success" | "error";
  started_at?: string;
  completed_at?: string;
}

export interface ActivityGroup {
  attempt: number;
  phase?: string;
  status?: string;
  entries: ActivityEntry[];
}

export interface AgentCommand {
  id: string;
  issue_id: string;
  command: string;
  status: "pending" | "waiting_for_unblock" | "delivered";
  created_at: string;
  delivered_at?: string;
  delivery_mode?: string;
  delivery_thread_id?: string;
  delivery_attempt?: number;
}

export interface IssueExecutionDetail {
  issue_id: string;
  identifier: string;
  active: boolean;
  phase: string;
  attempt_number: number;
  failure_class?: string;
  current_error?: string;
  retry_state: "none" | "active" | "scheduled" | "paused";
  next_retry_at?: string;
  paused_at?: string;
  pause_reason?: string;
  consecutive_failures?: number;
  pause_threshold?: number;
  session_source: "none" | "live" | "persisted";
  session?: Session;
  runtime_events: RuntimeEvent[];
  activity_groups: ActivityGroup[];
  debug_activity_groups?: ActivityGroup[];
  agent_commands: AgentCommand[];
  pending_interrupt?: PendingInterrupt;
}

export interface BootstrapResponse {
  generated_at: string;
  overview: {
    status: Record<string, unknown>;
    snapshot: Snapshot;
    board: IssueStateCounts;
    project_count: number;
    epic_count: number;
    issue_count: number;
    series: RuntimeSeriesPoint[];
    recent_events: RuntimeEvent[];
  };
  projects: ProjectSummary[];
  epics: EpicSummary[];
  issues: {
    items: IssueSummary[];
    total: number;
    limit: number;
    offset: number;
  };
  sessions: SessionsResponse;
}
