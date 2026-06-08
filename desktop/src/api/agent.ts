// API 封装对 Go agent HTTP 接口的请求，统一处理 baseURL 和错误。

// dev 模式对应开发版 agent（57018），build 后对应正式版（57017）
const AGENT_HOST = import.meta.env.DEV ? '127.0.0.1:57018' : '127.0.0.1:57017'
const BASE = `http://${AGENT_HOST}`
export const WS_BASE = `ws://${AGENT_HOST}`
export { AGENT_HOST }

// AgentAPIErrorPayload 描述 agent 结构化错误响应。
//
// 参数：
//   - code: 稳定错误码，例如 approval_required
//   - error: 可展示错误信息
//   - plan: operation 预检计划
//   - approval: 待处理审批请求
//
// 注意：
//   - 该结构只用于保留 agent 返回的错误上下文，不自行计算审批状态
export interface AgentAPIErrorPayload {
  code?: string
  error?: string
  plan?: OperationPlan
  approval?: OperationApproval
}

// AgentAPIError 表示 agent HTTP API 返回的结构化错误。
//
// 参数：
//   - message: 可展示错误信息
//   - status: HTTP 状态码
//   - payload: agent 返回的结构化错误体
//
// 返回：
//   - 可被调用方用 code/plan/approval 精确分支处理的 Error
//
// 注意：
//   - approval token 不会出现在该错误对象中
export class AgentAPIError extends Error {
  status: number
  code?: string
  plan?: OperationPlan
  approval?: OperationApproval

  constructor(message: string, status: number, payload?: AgentAPIErrorPayload) {
    super(message)
    this.name = 'AgentAPIError'
    this.status = status
    this.code = payload?.code
    this.plan = payload?.plan
    this.approval = payload?.approval
  }
}

// isApprovalRequiredError 判断错误是否为需要用户审批的结构化响应。
//
// 参数：
//   - error: 任意捕获到的异常值
//
// 返回：
//   - true 表示包含 approval_required code 和 approval 对象
//
// 注意：
//   - 调用方仍需通过审批页批准并取得一次性 token 后才能重试操作
export function isApprovalRequiredError(error: unknown): error is AgentAPIError & { approval: OperationApproval } {
  return error instanceof AgentAPIError && error.code === 'approval_required' && !!error.approval
}

function requestHeaders(headers?: HeadersInit): Record<string, string> {
  const merged: Record<string, string> = {
    'Content-Type': 'application/json',
    'X-SuperDev-Requester': 'desktop',
    'X-SuperDev-Requester-Label': 'SuperDev Desktop',
  }
  if (!headers) return merged
  if (typeof Headers !== 'undefined' && headers instanceof Headers) {
    headers.forEach((value, key) => {
      merged[key] = value
    })
    return merged
  }
  if (Array.isArray(headers)) {
    for (const [key, value] of headers) merged[key] = value
    return merged
  }
  const plainHeaders = headers as Record<string, string>
  return { ...merged, ...plainHeaders }
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const { headers, ...rest } = options ?? {}
  const res = await fetch(`${BASE}${path}`, {
    ...rest,
    headers: requestHeaders(headers),
  })
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    let body: AgentAPIErrorPayload | undefined
    try {
      body = (await res.json()) as AgentAPIErrorPayload
      if (body.error) message = body.error
    } catch {
      /* 非 JSON 错误体 */
    }
    throw new AgentAPIError(message, res.status, body)
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

function postWithApprovalToken(approvalToken?: string): RequestInit {
  return {
    method: 'POST',
    headers: approvalToken ? { 'X-SuperDev-Approval-Token': approvalToken } : undefined,
  }
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

export type PipelineTemplateCategory = 'build' | 'deploy' | 'cleanup' | 'general'

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
  category: PipelineTemplateCategory
  version: string
  digest: string
  description?: string
  inputs?: Record<string, TemplateInput>
}

export interface PipelineTemplateModel {
  id: string
  name: string
  description?: string
  category?: PipelineTemplateCategory
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

export type RunLogStream = 'stdout' | 'stderr' | 'system' | 'command'

export interface RunLogLine {
  id: number
  run_id: string
  step_name: string
  host_id?: string
  host_name?: string
  stream: RunLogStream
  line: string
  at: number
}

export interface RunStatusPatch {
  step_name?: string
  host_id?: string
  status: RunStatus
  exit_code?: number
  at?: number
}

export type RunEvent =
  | { kind: 'log'; log: RunLogLine }
  | { kind: 'status'; status: RunStatusPatch }
  | { kind: 'done'; run: Run }

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
  version?: string
  replicas?: number
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

export type TransportType = 'tunnel' | 'direct' | 'mq' | 'bridge'

export interface TunnelParams {
  remote_agent_port: number
}

export interface DirectParams {
  address?: string
}

export interface TransportEntry {
  type: TransportType
  tunnel?: TunnelParams
  direct?: DirectParams
}

export interface TransportConfig {
  chain: TransportEntry[]
}

export interface Host {
  id: string
  name: string
  public_ip?: string
  private_ip?: string
  tags: string[]
  ssh_host?: string
  ssh_port?: number
  ssh_user?: string
  ssh_password?: string
  ssh_private_key?: string
  ssh_key_path?: string
  is_self?: boolean
  node_id?: string
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

export type AgentHealth = 'unknown' | 'healthy' | 'unreachable' | 'version-mismatch' | 'auth-failed' | 'pending-bootstrap'
export type ProbeStatus = 'reachable' | 'unreachable' | 'version-mismatch' | 'auth-failed' | 'pending-bootstrap'

export interface AgentRuntime {
  installed: boolean
  version?: string
  health: AgentHealth
  reachable: boolean
  local_port?: number
}

export interface NodeStatus {
  host_id: string
  name?: string
  reachable: boolean
  agent: AgentRuntime
  deployments: RuntimeInstanceStatus[]
  managed?: ManagedDeploymentStatus
  route?: RouteStatus
  updated_at: string
  error?: string
}

export interface ProbeResult {
  index: number
  transport_type: TransportType
  status: ProbeStatus
  reachable: boolean
  version?: string
  error?: string
  latency_ms?: number
  checked_at: string
}

export interface RouteStatus {
  selected_index: number
  selected_type?: TransportType
  degraded: boolean
  last_results?: ProbeResult[]
}

export type AgentTLSMode = 'off' | 'auto' | 'manual'

export interface AgentTLSSpec {
  mode: AgentTLSMode | string
  ca_cert?: string
  server_name?: string
}

export interface AgentConfig {
  listen_address?: string
  listen_port?: number
}

export interface AgentSecurity {
  token_configured: boolean
  provision_state: 'not-configured' | 'pending-bootstrap' | 'provisioned' | string
  tls: AgentTLSSpec
}

export interface AgentDTO {
  host_id: string
  host_name: string
  tags: string[]
  transport: TransportConfig
  config: AgentConfig
  runtime: AgentRuntime
  security: AgentSecurity
  node?: NodeStatus
  last_error?: string
  updated_at?: string
}

export interface AgentCreatePayload {
  host_id: string
  transport: TransportConfig
  config: AgentConfig
  security: AgentSecurity
}

export interface AgentTransportUpdatePayload {
  transport: TransportConfig
}

export type AgentUpdatePayload = AgentTransportUpdatePayload

export interface AgentConfigUpdatePayload {
  config: AgentConfig
  security: AgentSecurity
}

export interface AgentInstallCommandPayload {
  method?: 'generated_command'
  controller_url: string
  bind_address?: string
  remote_agent_port?: number
  transport_type?: TransportType
  token_ttl_minutes?: number
}

export interface AgentInstallPayload {
  method: 'push_over_ssh'
}

export interface AgentInstallCommandResponse {
  command: string
  restart_command?: string
  expires_at: string
  token_id: string
}

export interface AgentInstallResponse {
  ok: boolean
  host_id: string
  platform: string
  message: string
}

export interface AgentRestartResponse {
  ok: boolean
  host_id: string
  platform: string
  message: string
}

export interface AgentTransportTestPayload {
  index: number
}

export interface AgentProvisionPayload {
  index: number
  tls_mode: 'off' | 'auto' | 'manual'
}

export interface AgentProvisionResponse {
  status: 'provisioned'
  restart_required?: boolean
}

export interface TunnelStatus {
  host_id: string
  state?: TunnelState
  local_port?: number
  error?: string
  last_active?: string
  agent?: AgentHealth
  agent_version?: string
  agent_checked_at?: string
}

export interface ManagedCollectorStatus {
  deployment_id: string
  service_name?: string
  env_name?: string
  name?: string
  type?: LogSourceType | string
  collector_id?: string
  desired: boolean
  running: boolean
  status?: Health | string
  error?: string
}

export interface ManagedDeploymentStatus {
  deployment_count: number
  collector_count: number
  collectors: ManagedCollectorStatus[]
  last_result?: {
    deployment_count: number
    collector_count: number
    failed_collectors?: Array<{ name: string; type: string; error: string }>
    persisted: boolean
    error?: string
  }
}

export interface HostManagedDeploymentStatus {
  host_id: string
  host_name?: string
  desired_deployment_count: number
  desired_collector_count: number
  tunnel_connected: boolean
  remote?: ManagedDeploymentStatus
  error?: string
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
  public_ip?: string
  private_ip?: string
  tags?: string[]
  ssh_host?: string
  ssh_port?: number
  ssh_user?: string
  ssh_password?: string
  ssh_private_key?: string
  ssh_key_path?: string
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
  listNodes: () => request<NodeStatus[]>('/api/nodes'),
  listAgents: () => request<AgentDTO[]>('/api/agents'),
  createAgent: (payload: AgentCreatePayload) =>
    request<AgentDTO>('/api/agents', { method: 'POST', body: JSON.stringify(payload) }),
  getAgent: (hostId: string) =>
    request<AgentDTO>(`/api/agents/${encodeURIComponent(hostId)}`),
  updateAgent: (hostId: string, payload: AgentUpdatePayload) =>
    request<AgentDTO>(`/api/agents/${encodeURIComponent(hostId)}`, {
      method: 'PUT',
      body: JSON.stringify(payload),
    }),
  updateAgentTransport: (hostId: string, payload: AgentTransportUpdatePayload) =>
    request<AgentDTO>(`/api/agents/${encodeURIComponent(hostId)}/transport`, {
      method: 'PUT',
      body: JSON.stringify(payload),
    }),
  updateAgentConfig: (hostId: string, payload: AgentConfigUpdatePayload) =>
    request<AgentDTO>(`/api/agents/${encodeURIComponent(hostId)}/config`, {
      method: 'PUT',
      body: JSON.stringify(payload),
    }),
  deleteAgent: (hostId: string) =>
    request<void>(`/api/agents/${encodeURIComponent(hostId)}`, { method: 'DELETE' }),
  checkAgent: (hostId: string) =>
    request<AgentDTO>(`/api/agents/${encodeURIComponent(hostId)}/check`, { method: 'POST' }),
  installAgent: (hostId: string, payload: AgentInstallPayload) =>
    request<AgentInstallResponse>(`/api/agents/${encodeURIComponent(hostId)}/install`, {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  restartAgent: (hostId: string) =>
    request<AgentRestartResponse>(`/api/agents/${encodeURIComponent(hostId)}/restart`, { method: 'POST' }),
  generateAgentInstallCommand: (hostId: string, payload: AgentInstallCommandPayload) =>
    request<AgentInstallCommandResponse>(`/api/agents/${encodeURIComponent(hostId)}/install-command`, {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  testAgentTransport: (hostId: string, payload: AgentTransportTestPayload) =>
    request<ProbeResult>(`/api/agents/${encodeURIComponent(hostId)}/transports/test`, {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  provisionAgent: (hostId: string, payload: AgentProvisionPayload) =>
    request<AgentProvisionResponse>(`/api/agents/${encodeURIComponent(hostId)}/provision`, {
      method: 'POST',
      body: JSON.stringify(payload),
    }),

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
  startDeployment: (id: string, approvalToken?: string) =>
    request<void>(`/api/deployments/${encodeURIComponent(id)}/start`, postWithApprovalToken(approvalToken)),
  stopDeployment: (id: string, approvalToken?: string) =>
    request<void>(`/api/deployments/${encodeURIComponent(id)}/stop`, postWithApprovalToken(approvalToken)),
  restartDeployment: (id: string, approvalToken?: string) =>
    request<void>(`/api/deployments/${encodeURIComponent(id)}/restart`, postWithApprovalToken(approvalToken)),

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
  startEnvSelected: (projectId: string, envName: string, approvalToken?: string) =>
    request<void>(`/api/projects/${encodeURIComponent(projectId)}/envs/${encodeURIComponent(envName)}/start-selected`, {
      method: 'POST',
      headers: approvalToken ? { 'X-SuperDev-Approval-Token': approvalToken } : undefined,
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

  // 远程监听：Host CRUD
  listHosts: () => request<Host[]>('/api/hosts'),
  createHost: (payload: HostCreatePayload) =>
    request<Host>('/api/hosts', { method: 'POST', body: JSON.stringify(payload) }),
  updateHost: (id: string, payload: HostUpdatePayload) =>
    request<Host>(`/api/hosts/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteHost: (id: string) =>
    request<void>(`/api/hosts/${id}`, { method: 'DELETE' }),
  getHostManagedDeploymentStatus: (id: string) =>
    request<HostManagedDeploymentStatus>(`/api/hosts/${id}/managed-deployments/status`),

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

/** nodesWsUrl 返回 NodeRegistry 快照 WebSocket URL。 */
export function nodesWsUrl(): string {
  return `${WS_BASE}/ws/nodes`
}

/** runLogsWsUrl 返回指定 pipeline run 的 WebSocket 日志流 URL。 */
export function runLogsWsUrl(runId: string): string {
  return `${WS_BASE}/ws/runs/${encodeURIComponent(runId)}/logs`
}
