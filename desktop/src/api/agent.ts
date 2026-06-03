// API 封装对 Go agent HTTP 接口的请求，统一处理 baseURL 和错误。

// dev 模式对应开发版 agent（57018），build 后对应正式版（57017）
const AGENT_HOST = import.meta.env.DEV ? '127.0.0.1:57018' : '127.0.0.1:57017'
const BASE = `http://${AGENT_HOST}`
export const WS_BASE = `ws://${AGENT_HOST}`
export { AGENT_HOST }

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  })
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      /* 非 JSON 错误体 */
    }
    throw new Error(message)
  }
  return res.json() as Promise<T>
}

function qs(params?: Record<string, string | number | undefined>): string {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(params ?? {})) {
    if (value !== undefined && value !== '') query.set(key, String(value))
  }
  const encoded = query.toString()
  return encoded ? `?${encoded}` : ''
}

export type DeployLocation = 'local' | 'remote'
export type RuntimeType = 'command' | 'systemd' | 'launchd' | 'docker' | 'nginx_static' | 'external'
export type ControlMode = 'monitor' | 'managed'
export type LogKind = 'process' | 'journalctl' | 'macos_log' | 'docker' | 'nginx' | 'file_tail' | 'command'
export type Health = 'running' | 'healthy' | 'restarting' | 'stopped' | 'failed' | 'unknown'

export interface InstanceMetrics {
  cpu_percent: number | null
  mem_bytes: number | null
  uptime_sec: number | null
  restarts: number | null
  health: Health
  base: RuntimeType | 'process' | 'unknown' | string
}

export interface RuntimeInstanceStatus {
  service_id: string
  service_name: string
  deployment_id: string
  node_id: string
  node_name: string
  is_local: boolean
  error?: string
  metrics: InstanceMetrics
}

export interface EnvRuntimeStatus {
  env_name: string
  instances: RuntimeInstanceStatus[]
}

export interface RuntimeStatusResponse {
  environments: EnvRuntimeStatus[]
}

export type PipelinePhase = 'build' | 'deploy' | 'finally'
export type RunStatus = 'pending' | 'running' | 'success' | 'failed' | 'skipped' | 'canceled'

export interface PipelineStep {
  name: string
  type: string
  needs?: string[]
  roles?: string[]
  run_if?: string
  batch_size?: number
  retries?: number
  retry_delay?: string
  tolerate_failures?: string
  with?: Record<string, unknown>
}

export interface Pipeline {
  variables?: Record<string, string>
  roles?: Record<string, string[]>
  build?: PipelineStep[]
  deploy?: PipelineStep[]
  finally?: PipelineStep[]
}

export interface RuntimeConfig {
  type: RuntimeType
  command?: string
  working_dir?: string
  env_file?: string
  env_vars?: Record<string, string>
  service_name?: string
  release_dir?: string
  current_dir?: string
  exec_start?: string
  label?: string
  plist_path?: string
  container?: string
  domain?: string
}

export interface LogConfig {
  type: LogKind
  target?: string
  path?: string
  command?: string
  extra_args?: string[]
}

export interface PipelineEnvironment {
  variables?: Record<string, string>
}

export interface ProjectPipelineRole {
  from_service?: string
  hosts?: string[]
}

export type ArtifactKind = 'file' | 'image'

export interface ProjectPipeline {
  id: string
  name: string
  services?: string[]
  artifact_kind?: ArtifactKind
  variables?: Record<string, string>
  environments?: Record<string, PipelineEnvironment>
  roles?: Record<string, ProjectPipelineRole>
  pipeline: Pipeline
}

export interface TemplateFileItem {
  from: string
  to: string
}

export interface TemplateInput {
  label: string
  type: 'string' | 'number' | 'bool' | 'select' | 'path' | 'target_role' | 'file_list'
  required?: boolean
  default?: string
  description?: string
  options?: string[]
}

export interface PipelineTemplateSummary {
  source: 'builtin' | 'user' | 'project'
  id: string
  name: string
  version: string
  digest: string
  description?: string
  inputs?: Record<string, TemplateInput>
}

export interface PipelineTemplateModel {
  id: string
  name: string
  description?: string
  version: string
  inputs?: Record<string, TemplateInput>
  steps: PipelineStep[]
}

export interface PipelineTemplateDetail {
  source: 'builtin' | 'user' | 'project'
  id: string
  version: string
  digest: string
  yaml: string
  template: PipelineTemplateModel
}

export interface PipelineTemplatesResponse {
  items: PipelineTemplateSummary[]
}

export interface PipelinePreviewResponse {
  run: {
    deployment_id: string
    status: RunStatus
    step_runs: Array<{
      step_name: string
      type: string
      phase: PipelinePhase
      needs?: string[]
      status: RunStatus
      tasks: Array<{ host_id?: string; host_name?: string; status: RunStatus }>
    }>
  }
}

export interface RunTask {
  host_id?: string
  host_name?: string
  status: RunStatus
  exit_code?: number
  started_at?: number
  finished_at?: number
}

export interface StepRun {
  step_name: string
  type: string
  phase: PipelinePhase
  needs?: string[]
  status: RunStatus
  tasks: RunTask[]
}

export interface Run {
  id: string
  project_id?: string
  pipeline_id?: string
  env_name?: string
  deployment_id: string
  artifact_version?: string
  status: RunStatus
  step_runs: StepRun[]
  started_at: number
  finished_at?: number
}

export interface ProjectPipelineRunsResponse {
  items: Run[]
}

export interface ProjectPipelineDeployRequest {
  env_name: string
  host_ids?: string[]
  artifact_version?: string
  variables?: Record<string, string>
}

export interface RunLogLine {
  id: number
  run_id: string
  step_name: string
  host_id?: string
  stream: string
  line: string
  at: number
}

export interface ProjectPipelineRunLogsResponse {
  items: RunLogLine[]
}

export interface Deployment {
  id: string
  env_name: string
  location: DeployLocation
  control_mode?: ControlMode
  runtime?: RuntimeConfig
  logs?: LogConfig
  command?: string
  work_dir?: string
  env?: Record<string, string>
  host_ids?: string[]
  log_type?: LogSourceType
  log_target?: string
  extra_args?: string[]
  env_file?: string
  read_only?: boolean
  start_command?: string
  stop_command?: string
  status: '' | 'starting' | 'running' | 'failed'
  pid?: number
}

export interface Environment {
  id: string
  name: string
  is_dev: boolean
  order: number
}

export interface Service {
  id: string
  project_id: string
  name: string
  status: '' | 'starting' | 'running' | 'failed'
  pid?: number
  required: boolean
  order: number
  deployments?: Deployment[]
}

export interface Project {
  id: string
  name: string
  root_path: string
  variables?: Record<string, string>
  services: Service[]
  pipelines?: ProjectPipeline[]
  env_selected_service_ids?: Record<string, string[]>
  environments?: Environment[]
}

export interface LogEntry {
  id: number
  deployment_id: string
  run_id: string
  timestamp: string
  level: string
  message: string
  stream: string
  repeat_count?: number
  source_id?: string
}

export interface LogRule {
  id: string
  name: string
  type: 'include' | 'exclude'
  keywords: string[]
  logic: 'and' | 'or'
  enabled: boolean
}

export interface AgentSettings {
  log_retention_days: number
  sample_seeded?: boolean
  onboarding_completed?: boolean
}

export type AgentSettingsPatch = Partial<Pick<AgentSettings, 'log_retention_days' | 'onboarding_completed'>>

export interface OperationTarget {
  project_id?: string
  project_name?: string
  env_name?: string
  service_id?: string
  service_name?: string
  deployment_id?: string
  template_path?: string
  template_digest?: string
  pipeline_id?: string
}

export interface OperationCheck {
  name: string
  status: string
  message: string
}

export interface OperationPlan {
  id: string
  kind: string
  target: OperationTarget
  target_summary?: string
  risk_level: 'low' | 'medium' | 'high' | 'critical' | string
  requires_approval: boolean
  denied: boolean
  reasons?: string[]
  expected_effects?: string[]
  checks?: OperationCheck[]
  fingerprint: string
  created_at?: string
  expires_at?: string
}

export interface OperationApproval {
  id: string
  plan: OperationPlan
  status: 'pending' | 'approved' | 'rejected' | 'expired' | 'used' | string
  requested_by?: string
  requester_label?: string
  created_at?: string
  updated_at?: string
  expires_at?: string
  decided_by?: string
  decision_note?: string
}

export interface OperationApprovalDetail {
  approval: OperationApproval
  approval_token?: string
}

export interface OperationAuditEvent {
  id: string
  kind: string
  action: string
  approval_id?: string
  plan: OperationPlan
  summary: string
  data?: Record<string, unknown>
  created_at?: string
}

export interface OperationAuditList {
  events: OperationAuditEvent[]
  count: number
}

export interface FetchLogsParams {
  deployment?: string
  run?: string
  limit?: number
  before?: number
}

export interface LogSearchResponse {
  query: string
  total: number
  items: LogEntry[]
  deployment_counts: Record<string, number>
  has_more: boolean
}

export interface LogContextResponse {
  target_id: number
  anchor_time: string
  items_by_deployment: Record<string, LogEntry[]>
}

export type LogContextPageDirection = 'before' | 'after'

export interface LogContextPageResponse {
  deployment_id: string
  direction: LogContextPageDirection
  items: LogEntry[]
  has_more: boolean
}

export interface SearchLogsParams {
  project: string
  q: string
  deployment?: string[]
  limit?: number
  cursor_time?: string
  cursor_id?: number
}

export interface FetchLogContextParams {
  project: string
  id: number
  deployment?: string[]
  before_ms?: number
  after_ms?: number
}

export interface FetchLogContextPageParams {
  project: string
  deployment: string
  direction: LogContextPageDirection
  cursor_time: string
  cursor_id: number
  limit?: number
}

// ===== 远程监听相关类型 =====

export interface Host {
  id: string
  name: string
  ssh_host: string
  ssh_port: number
  ssh_user: string
  ssh_password?: string
  ssh_key_path?: string
  remote_agent_port: number
  local_tunnel_port: number
  tags: string[]
  is_self?: boolean
  node_id?: string
}

export interface InstallHostAgentResult {
  ok: boolean
  host_id: string
  platform: string
  message: string
}

export type LogSourceType = 'journalctl' | 'docker' | 'file_tail' | 'command'

export interface LogSource {
  id: string
  name: string
  type: LogSourceType
  host_ids: string[]
  tags: string[]
  extra_args: string[]
  project_id?: string
  service_id?: string
}

export interface LaunchConfig {
  name: string
  command: string
  work_dir: string
  env?: Record<string, string>
}

export interface SetupDeployment {
  id?: string
  env_name: string
  location: 'local' | 'remote'
  control_mode?: ControlMode
  runtime?: RuntimeConfig
  logs?: LogConfig
  command?: string
  work_dir?: string
  env?: Record<string, string>
  host_ids?: string[]
  log_type?: LogSourceType
  log_target?: string
  extra_args?: string[]
  env_file?: string
  read_only?: boolean
  start_command?: string
  stop_command?: string
}

export interface SetupServiceEntry {
  id: string
  name: string
  required: boolean
  order: number
  deployments: SetupDeployment[]
}

export interface SetupPayload {
  variables?: Record<string, string>
  environments: Array<{ id?: string; name: string; is_dev: boolean; order: number }>
  services: SetupServiceEntry[]
  pipelines?: ProjectPipeline[]
}

export interface ProjectPipelinePreviewRequest {
  env_name: string
  service_names?: string[]
  variables?: Record<string, string>
}

export interface SshConfigEntry {
  host: string
  hostname: string
  port: number
  user: string
  identity_file?: string
}

export type TunnelState = 'idle' | 'connecting' | 'open' | 'failed' | 'closed'

export type AgentHealth = 'unknown' | 'healthy' | 'unreachable' | 'version-mismatch'

export interface TunnelStatus {
  host_id: string
  state?: TunnelState
  local_port?: number
  error?: string
  last_active?: string
  agent?: AgentHealth
}

export interface RemoteLogEntry extends LogEntry {
  host_id: string
  host_name?: string
  key?: string
  log_source_id?: string
}

export interface RemoteViewGroup {
  group_key: string
  host_ids: string[]
}

export interface RemoteViewResponse {
  log_source: LogSource
  groups: RemoteViewGroup[]
  hosts: Host[]
}

export interface RemoteSearchParams {
  log_source_id?: string
  project_id?: string
  group: string
  query: string
  service_id?: string[]
  host_id?: string[]
  limit?: number
  cursor?: string
  from?: string
  to?: string
}

export interface RemoteSearchNode {
  host_id: string
  host_name?: string
  status: 'success' | 'failed' | 'timeout'
  count: number
  error?: string
}

export interface RemoteSearchServiceColumn {
  service_id: string
  service_name?: string
  status: 'success' | 'partial_failed' | 'failed' | 'timeout'
  result_count: number
  node_count: number
  nodes: RemoteSearchNode[]
  entries: RemoteLogEntry[]
}

export interface RemoteSearchFailure {
  service_id?: string
  host_id: string
  kind: 'failed' | 'timeout'
  message?: string
}

export interface RemoteSearchResponse {
  query?: string
  status?: 'success' | 'partial_failed' | 'failed'
  entries: RemoteLogEntry[]
  total_by_host: Record<string, number>
  hosts_failed: string[]
  service_columns?: RemoteSearchServiceColumn[]
  failures?: RemoteSearchFailure[]
  next_cursor: string
  has_more: boolean
}

// ===== Deployment 统一日志接口类型 =====

export interface DeploymentFetchLogsParams {
  deploymentId: string
  limit?: number
  before?: number
}

export interface DeploymentLogsResponse {
  items: LogEntry[]
  next?: {
    time?: string
    id?: number
  }
}

export interface DeploymentSearchParams {
  deploymentId: string
  q: string
  limit?: number
  cursor_time?: string
  cursor_id?: number
}

export interface DeploymentSearchResponse {
  items: LogEntry[]
  cursor: { time: string; id: number } | null
  has_more: boolean
}

export interface HostCreatePayload {
  name: string
  ssh_host: string
  ssh_port?: number
  ssh_user: string
  ssh_password?: string
  ssh_key_path?: string
  remote_agent_port?: number
  tags?: string[]
}

export type HostUpdatePayload = Partial<HostCreatePayload>

export interface LogSourceCreatePayload {
  name: string
  type: LogSourceType
  host_ids: string[]
  tags?: string[]
  extra_args?: string[]
  project_id?: string
  service_id?: string
}

export interface TestConnectionPayload {
  ssh_host: string
  ssh_port: number
  ssh_user: string
  ssh_password?: string
  ssh_key_path?: string
}

export interface TestConnectionResult {
  ok: boolean
  message: string
  latency_ms?: number
}

export type LogSourceUpdatePayload = Partial<LogSourceCreatePayload>

export const api = {
  // 项目
  listProjects: () => request<Project[]>('/api/projects'),
  addProject: (root_path: string) =>
    request<Project>('/api/projects', { method: 'POST', body: JSON.stringify({ root_path }) }),
  probeProject: (root_path: string) =>
    request<Project>(`/api/projects/probe?root_path=${encodeURIComponent(root_path)}`),
  deleteProject: (id: string) =>
    request<void>(`/api/projects/${id}`, { method: 'DELETE' }),
  getProjectRules: (id: string) => request<LogRule[]>(`/api/projects/${id}/rules`),
  putProjectRules: (id: string, rules: LogRule[]) =>
    request<LogRule[]>(`/api/projects/${id}/rules`, { method: 'PUT', body: JSON.stringify(rules) }),
  getVscodeLaunch: (projectId: string) =>
    request<LaunchConfig[]>(`/api/projects/${encodeURIComponent(projectId)}/vscode-launch`),
  putProjectSetup: (projectId: string, payload: SetupPayload) =>
    request<Project>(`/api/projects/${encodeURIComponent(projectId)}/setup`, {
      method: 'PUT',
      body: JSON.stringify(payload),
    }),
  getRuntimeStatus: (projectId: string) =>
    request<RuntimeStatusResponse>(`/api/projects/${encodeURIComponent(projectId)}/runtime-status`),

  // 设置
  getSettings: () => request<AgentSettings>('/api/settings'),
  putSettings: (settings: AgentSettingsPatch) =>
    request<AgentSettings>('/api/settings', { method: 'PUT', body: JSON.stringify(settings) }),

  // Operation 审批
  listOperationApprovals: (params?: { status?: string; project_id?: string; limit?: number }) =>
    request<OperationApproval[]>(`/api/operation-approvals${qs(params)}`),
  getOperationApproval: (id: string) =>
    request<OperationApprovalDetail>(`/api/operation-approvals/${encodeURIComponent(id)}`),
  approveOperationApproval: (id: string, payload: { decided_by: string; note?: string }) =>
    request<OperationApproval>(`/api/operation-approvals/${encodeURIComponent(id)}/approve`, {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  rejectOperationApproval: (id: string, payload: { decided_by: string; note?: string }) =>
    request<OperationApproval>(`/api/operation-approvals/${encodeURIComponent(id)}/reject`, {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  listOperationAudit: (params?: { project_id?: string; kind?: string; approval_id?: string; since?: string; limit?: number }) =>
    request<OperationAuditList>(`/api/operation-audit${qs(params)}`),

  // 服务
  listServices: (projectId?: string) => {
    const qs = projectId ? `?project_id=${projectId}` : ''
    return request<Service[]>(`/api/services${qs}`)
  },

  // Deployment 进程控制
  startDeployment: (id: string) =>
    request<void>(`/api/deployments/${encodeURIComponent(id)}/start`, { method: 'POST' }),
  stopDeployment: (id: string) =>
    request<void>(`/api/deployments/${encodeURIComponent(id)}/stop`, { method: 'POST' }),
  restartDeployment: (id: string) =>
    request<void>(`/api/deployments/${encodeURIComponent(id)}/restart`, { method: 'POST' }),

  // Pipeline 模板与预览
  listPipelineTemplates: () => request<PipelineTemplatesResponse>('/api/pipeline/templates'),
  getPipelineTemplate: (source: PipelineTemplateSummary['source'], id: string, version: string) =>
    request<PipelineTemplateDetail>(
      `/api/pipeline/templates/${encodeURIComponent(source)}/${encodeURIComponent(id)}?version=${encodeURIComponent(version)}`,
    ),
  importPipelineTemplate: (path: string) =>
    request<PipelineTemplateSummary>('/api/pipeline/templates/import', {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),
  previewProjectPipeline: (projectId: string, pipelineId: string, payload: ProjectPipelinePreviewRequest) =>
    request<PipelinePreviewResponse>(
      `/api/projects/${encodeURIComponent(projectId)}/pipelines/${encodeURIComponent(pipelineId)}/preview`,
      { method: 'POST', body: JSON.stringify(payload) },
    ),
  deployProjectPipeline: (projectId: string, pipelineId: string, payload: ProjectPipelineDeployRequest) =>
    request<Run>(`/api/projects/${encodeURIComponent(projectId)}/pipelines/${encodeURIComponent(pipelineId)}/deploy`, {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  listProjectPipelineRuns: (projectId: string, pipelineId: string) =>
    request<ProjectPipelineRunsResponse>(`/api/projects/${encodeURIComponent(projectId)}/pipelines/${encodeURIComponent(pipelineId)}/runs`),
  getProjectPipelineRun: (projectId: string, pipelineId: string, runId: string) =>
    request<Run>(`/api/projects/${encodeURIComponent(projectId)}/pipelines/${encodeURIComponent(pipelineId)}/runs/${encodeURIComponent(runId)}`),
  readProjectPipelineRunLogs: (projectId: string, pipelineId: string, runId: string, params?: { step_name?: string; host_id?: string; limit?: number; before?: number }) =>
    request<ProjectPipelineRunLogsResponse>(
      `/api/projects/${encodeURIComponent(projectId)}/pipelines/${encodeURIComponent(pipelineId)}/runs/${encodeURIComponent(runId)}/logs${qs(params)}`,
    ),

  // Env 级 selected
  putEnvSelected: (projectId: string, envName: string, names: string[]) =>
    request<void>(`/api/projects/${encodeURIComponent(projectId)}/env-selected`, {
      method: 'PUT',
      body: JSON.stringify({ env_name: envName, names }),
    }),
  startEnvSelected: (projectId: string, envName: string) =>
    request<void>(`/api/projects/${encodeURIComponent(projectId)}/envs/${encodeURIComponent(envName)}/start-selected`, {
      method: 'POST',
    }),

  // 日志
  fetchLogs: (params: FetchLogsParams) => {
    const qs = new URLSearchParams()
    if (params.deployment) qs.set('deployment', params.deployment)
    if (params.run) qs.set('run', params.run)
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.before) qs.set('before', String(params.before))
    return request<LogEntry[]>(`/api/logs${qs.toString() ? '?' + qs : ''}`)
  },
  searchLogs: (params: SearchLogsParams) => {
    const qs = new URLSearchParams()
    qs.set('project', params.project)
    qs.set('q', params.q)
    for (const deploymentId of params.deployment ?? []) qs.append('deployment', deploymentId)
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.cursor_time) qs.set('cursor_time', params.cursor_time)
    if (params.cursor_id) qs.set('cursor_id', String(params.cursor_id))
    return request<LogSearchResponse>(`/api/log-search?${qs}`)
  },
  fetchLogContext: (params: FetchLogContextParams) => {
    const qs = new URLSearchParams()
    qs.set('project', params.project)
    qs.set('id', String(params.id))
    for (const deploymentId of params.deployment ?? []) qs.append('deployment', deploymentId)
    if (params.before_ms) qs.set('before_ms', String(params.before_ms))
    if (params.after_ms) qs.set('after_ms', String(params.after_ms))
    return request<LogContextResponse>(`/api/logs/context?${qs}`)
  },
  fetchLogContextPage: (params: FetchLogContextPageParams) => {
    const qs = new URLSearchParams()
    qs.set('project', params.project)
    qs.set('deployment', params.deployment)
    qs.set('direction', params.direction)
    qs.set('cursor_time', params.cursor_time)
    qs.set('cursor_id', String(params.cursor_id))
    if (params.limit) qs.set('limit', String(params.limit))
    return request<LogContextPageResponse>(`/api/logs/context/page?${qs}`)
  },

  // 远程监听：Host 辅助操作
  detectSshKeys: () => request<string[]>('/api/hosts/detect-ssh-keys'),
  testConnection: (payload: TestConnectionPayload) =>
    request<TestConnectionResult>('/api/hosts/test-connection', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),

  // 远程监听：Host CRUD
  listHosts: () => request<Host[]>('/api/hosts'),
  createHost: (payload: HostCreatePayload) =>
    request<Host>('/api/hosts', { method: 'POST', body: JSON.stringify(payload) }),
  updateHost: (id: string, payload: HostUpdatePayload) =>
    request<Host>(`/api/hosts/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteHost: (id: string) =>
    request<void>(`/api/hosts/${id}`, { method: 'DELETE' }),
  installHostAgent: (id: string) =>
    request<InstallHostAgentResult>(`/api/hosts/${id}/agent/install`, { method: 'POST' }),

  // 远程监听：SSH config 导入
  listSshConfigHosts: () => request<SshConfigEntry[]>('/api/ssh-config/hosts'),

  // 远程监听：LogSource CRUD
  listLogSources: () => request<LogSource[]>('/api/log-sources'),
  createLogSource: (payload: LogSourceCreatePayload) =>
    request<LogSource>('/api/log-sources', { method: 'POST', body: JSON.stringify(payload) }),
  updateLogSource: (id: string, payload: LogSourceUpdatePayload) =>
    request<LogSource>(`/api/log-sources/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteLogSource: (id: string) =>
    request<void>(`/api/log-sources/${id}`, { method: 'DELETE' }),

  // 远程监听：隧道（POST 建立，DELETE 断开）
  listTunnels: () => request<TunnelStatus[]>('/api/tunnels'),
  openTunnel: (hostId: string) =>
    request<TunnelStatus>(`/api/tunnels/${hostId}`, { method: 'POST' }),
  closeTunnel: (hostId: string) =>
    request<void>(`/api/tunnels/${hostId}`, { method: 'DELETE' }),
  ensureCollector: (_hostId: string, localPort: number, name: string, type: LogSourceType) => {
    const url = `http://127.0.0.1:${localPort}/api/collectors`
    return fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, type }),
    }).then(async res => {
      if (!res.ok) {
        const body = await res.json().catch(() => ({})) as { error?: string }
        throw new Error(body.error ?? `${res.status} ${res.statusText}`)
      }
      return res.json() as Promise<{ id: string; service_id: string }>
    })
  },

  // 远程监听：LogSource 视图与跨节点搜索
  getRemoteView: (logSourceId: string) => {
    const qs = new URLSearchParams()
    qs.set('log_source_id', logSourceId)
    return request<RemoteViewResponse>(`/api/remote/view?${qs}`)
  },
  remoteSearch: (params: RemoteSearchParams) => {
    const qs = new URLSearchParams()
    if (params.log_source_id) qs.set('log_source_id', params.log_source_id)
    if (params.project_id) qs.set('project_id', params.project_id)
    qs.set('group', params.group)
    qs.set('query', params.query)
    for (const serviceId of params.service_id ?? []) qs.append('service_id', serviceId)
    for (const hostId of params.host_id ?? []) qs.append('host_id', hostId)
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.cursor) qs.set('cursor', params.cursor)
    if (params.from) qs.set('from', params.from)
    if (params.to) qs.set('to', params.to)
    return request<RemoteSearchResponse>(`/api/remote-log-search?${qs}`)
  },

  // Deployment 统一日志接口
  fetchDeploymentLogs: (params: DeploymentFetchLogsParams) => {
    const qs = new URLSearchParams()
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.before != null) qs.set('before', String(params.before))
    const q = qs.toString()
    return request<DeploymentLogsResponse | LogEntry[]>(`/api/deployments/${encodeURIComponent(params.deploymentId)}/logs${q ? '?' + q : ''}`)
      .then(body => Array.isArray(body) ? body : (body.items ?? []))
  },
  searchDeploymentLogs: (params: DeploymentSearchParams) => {
    const qs = new URLSearchParams()
    qs.set('q', params.q)
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.cursor_time) qs.set('cursor_time', params.cursor_time)
    if (params.cursor_id != null) qs.set('cursor_id', String(params.cursor_id))
    return request<DeploymentSearchResponse>(`/api/deployments/${encodeURIComponent(params.deploymentId)}/search?${qs}`)
  },
}

/** deploymentWsUrl 返回指定 deployment 的 WebSocket 日志流 URL。 */
export function deploymentWsUrl(deploymentId: string): string {
  return `${WS_BASE}/ws/deployments/${encodeURIComponent(deploymentId)}/logs`
}

/** runLogsWsUrl 返回指定 pipeline run 的 WebSocket 日志流 URL。 */
export function runLogsWsUrl(runId: string): string {
  return `${WS_BASE}/ws/runs/${encodeURIComponent(runId)}/logs`
}
