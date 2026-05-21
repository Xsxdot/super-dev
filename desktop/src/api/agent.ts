// API 封装对 Go agent HTTP 接口的请求，统一处理 baseURL 和错误。

const BASE = 'http://127.0.0.1:27017'

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
  repeat_count?: number
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

export interface LogSearchResponse {
  query: string
  total: number
  items: LogEntry[]
  service_counts: Record<string, number>
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
  putSelected: (projectId: string, names: string[]) =>
    request<void>(`/api/projects/${projectId}/selected`, { method: 'PUT', body: JSON.stringify({ names }) }),

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
}
