// API 封装对 Go agent HTTP 接口的请求，统一处理 baseURL 和错误。

const BASE = 'http://localhost:27017'

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json() as Promise<T>
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
  env_file?: string
  env?: Record<string, string>
}

export interface Project {
  id: string
  name: string
  root_path: string
  services: Service[]
  selected_service_ids: string[]
}

export interface LogEntry {
  id: number
  service_id: string
  run_id: string
  timestamp: string
  level: string
  message: string
  stream: string
}

export interface LogRule {
  id: string
  name: string
  type: 'include' | 'exclude'
  keywords: string[]
  logic: 'and' | 'or'
  enabled: boolean
}

export interface FetchLogsParams {
  service?: string
  run?: string
  limit?: number
  before?: number
}

export const api = {
  // 项目
  listProjects: () => request<Project[]>('/api/projects'),
  addProject: (root_path: string) =>
    request<Project>('/api/projects', { method: 'POST', body: JSON.stringify({ root_path }) }),
  deleteProject: (id: string) =>
    request<void>(`/api/projects/${id}`, { method: 'DELETE' }),
  getProjectRules: (id: string) => request<LogRule[]>(`/api/projects/${id}/rules`),
  putProjectRules: (id: string, rules: LogRule[]) =>
    request<LogRule[]>(`/api/projects/${id}/rules`, { method: 'PUT', body: JSON.stringify(rules) }),

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

  // 日志
  fetchLogs: (params: FetchLogsParams) => {
    const qs = new URLSearchParams()
    if (params.service) qs.set('service', params.service)
    if (params.run) qs.set('run', params.run)
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.before) qs.set('before', String(params.before))
    return request<LogEntry[]>(`/api/logs${qs.toString() ? '?' + qs : ''}`)
  },
}
