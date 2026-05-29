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

export type DeployLocation = 'local' | 'remote'

export type StepScope = 'local' | 'fan-out'
export type StepAction = 'run' | 'sync'

export interface PipelineStep {
  id: string
  name: string
  scope: StepScope
  action: StepAction
  command?: string
  work_dir?: string
  sync_from?: string
  sync_to?: string
}

export interface Pipeline {
  steps: PipelineStep[]
}

export interface Deployment {
  id: string
  env_name: string
  location: DeployLocation
  command?: string
  work_dir?: string
  host_ids?: string[]
  log_type?: LogSourceType
  log_target?: string
  extra_args?: string[]
  start_command?: string
  stop_command?: string
  pipeline?: Pipeline
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
  command: string
  work_dir: string
  required: boolean
  order: number
  deployments?: Deployment[]
  env_file?: string
  env?: Record<string, string>
}

export interface Project {
  id: string
  name: string
  root_path: string
  services: Service[]
  selected_service_ids: string[]
  env_selected_service_ids?: Record<string, string[]>
  environments?: Environment[]
}

export interface LogEntry {
  id: number
  service_id: string
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
}

export interface FetchLogsParams {
  service?: string
  run?: string
  limit?: number
  before?: number
}

export interface LogSearchResponse {
  query: string
  total: number
  items: LogEntry[]
  service_counts: Record<string, number>
  has_more: boolean
}

export interface LogContextResponse {
  target_id: number
  anchor_time: string
  items_by_service: Record<string, LogEntry[]>
}

export type LogContextPageDirection = 'before' | 'after'

export interface LogContextPageResponse {
  service_id: string
  direction: LogContextPageDirection
  items: LogEntry[]
  has_more: boolean
}

export interface SearchLogsParams {
  project: string
  q: string
  service?: string[]
  limit?: number
  cursor_time?: string
  cursor_id?: number
}

export interface FetchLogContextParams {
  project: string
  id: number
  service?: string[]
  before_ms?: number
  after_ms?: number
}

export interface FetchLogContextPageParams {
  project: string
  service: string
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
}

export type LogSourceType = 'journalctl' | 'docker'

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
  command?: string
  work_dir?: string
  env?: Record<string, string>
  host_ids?: string[]
  log_type?: LogSourceType
  log_target?: string
  start_command?: string
  stop_command?: string
  pipeline?: Pipeline
}

export interface SetupServiceEntry {
  id: string
  name: string
  required: boolean
  order: number
  deployments: SetupDeployment[]
}

export interface SetupPayload {
  environments: Array<{ id?: string; name: string; is_dev: boolean; order: number }>
  services: SetupServiceEntry[]
}

export interface SshConfigEntry {
  host: string
  hostname: string
  port: number
  user: string
  identity_file?: string
}

export type TunnelState = 'idle' | 'connecting' | 'open' | 'failed' | 'closed'

export interface TunnelStatus {
  host_id: string
  state: TunnelState
  local_port?: number
  error?: string
  last_active?: string
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

  // 设置
  getSettings: () => request<AgentSettings>('/api/settings'),
  putSettings: (settings: AgentSettings) =>
    request<AgentSettings>('/api/settings', { method: 'PUT', body: JSON.stringify(settings) }),

  // 服务
  listServices: (projectId?: string) => {
    const qs = projectId ? `?project_id=${projectId}` : ''
    return request<Service[]>(`/api/services${qs}`)
  },
  startService: (id: string) =>
    request<void>(`/api/services/${id}/start`, { method: 'POST' }),
  stopService: (id: string) =>
    request<void>(`/api/services/${id}/stop`, { method: 'POST' }),
  restartService: (id: string) =>
    request<void>(`/api/services/${id}/restart`, { method: 'POST' }),
  startSelected: (projectId: string) =>
    request<void>(`/api/projects/${projectId}/start-selected`, { method: 'POST' }),
  putSelected: (projectId: string, names: string[]) =>
    request<void>(`/api/projects/${projectId}/selected`, { method: 'PUT', body: JSON.stringify({ names }) }),

  // Deployment 进程控制
  startDeployment: (id: string) =>
    request<void>(`/api/deployments/${encodeURIComponent(id)}/start`, { method: 'POST' }),
  stopDeployment: (id: string) =>
    request<void>(`/api/deployments/${encodeURIComponent(id)}/stop`, { method: 'POST' }),
  restartDeployment: (id: string) =>
    request<void>(`/api/deployments/${encodeURIComponent(id)}/restart`, { method: 'POST' }),

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
    if (params.service) qs.set('service', params.service)
    if (params.run) qs.set('run', params.run)
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.before) qs.set('before', String(params.before))
    return request<LogEntry[]>(`/api/logs${qs.toString() ? '?' + qs : ''}`)
  },
  searchLogs: (params: SearchLogsParams) => {
    const qs = new URLSearchParams()
    qs.set('project', params.project)
    qs.set('q', params.q)
    for (const serviceId of params.service ?? []) qs.append('service', serviceId)
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.cursor_time) qs.set('cursor_time', params.cursor_time)
    if (params.cursor_id) qs.set('cursor_id', String(params.cursor_id))
    return request<LogSearchResponse>(`/api/log-search?${qs}`)
  },
  fetchLogContext: (params: FetchLogContextParams) => {
    const qs = new URLSearchParams()
    qs.set('project', params.project)
    qs.set('id', String(params.id))
    for (const serviceId of params.service ?? []) qs.append('service', serviceId)
    if (params.before_ms) qs.set('before_ms', String(params.before_ms))
    if (params.after_ms) qs.set('after_ms', String(params.after_ms))
    return request<LogContextResponse>(`/api/logs/context?${qs}`)
  },
  fetchLogContextPage: (params: FetchLogContextPageParams) => {
    const qs = new URLSearchParams()
    qs.set('project', params.project)
    qs.set('service', params.service)
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
    return request<LogEntry[]>(`/api/deployments/${encodeURIComponent(params.deploymentId)}/logs${q ? '?' + q : ''}`)
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
