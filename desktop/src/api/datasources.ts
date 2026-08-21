// datasources.ts 封装 AI 临时测试资源的数据源与租约 API。
//
// 职责：
//   - 提供数据源登记、探测、删除和临时租约管理的类型安全 client
//   - 保持密码只存在于写入请求类型，不把它加入列表响应模型
//
// 边界：
//   - 不缓存状态，Pinia store 负责加载与错误展示
//   - 不渲染 UI；不在此处决定审批、配额或回收策略
import { request } from '@/api/agent'

/** 数据源连通性与能力探测结果。 */
export interface DataSourceProbeResult {
  ok: boolean
  checked_at?: string
  server_version?: string
  capabilities?: Record<string, boolean>
  facts?: Record<string, string>
  missing?: string[]
  fix_hint?: string
  error?: string
}

/** 对外数据源视图，故意不声明 password 字段，防止 UI 意外渲染凭据。 */
export interface DataSource {
  id: string
  kind: 'postgres' | 'redis' | string
  name: string
  host: string
  port: number
  user?: string
  extra?: Record<string, string>
  probe: DataSourceProbeResult
  source?: string
  created_at?: string
}

/** 数据源写入请求；password 为空时更新接口保持旧密码。 */
export interface DataSourceInput {
  kind: 'postgres' | 'redis' | string
  name: string
  host: string
  port: number
  user?: string
  password?: string
  extra?: Record<string, string>
}

/** 临时资源的对外定位信息；列表接口不会返回 dsn。 */
export interface ProvisionResource {
  kind: string
  name: string
  dsn?: string
  meta?: Record<string, string>
}

/** 一组共享生命周期的临时资源租约。 */
export interface Lease {
  id: string
  project_id: string
  purpose: string
  resources: ProvisionResource[]
  created_at: string
  expires_at: string
  renew_count: number
}

/** 一次启动对账的回收统计。 */
export interface ReconcileReport {
  expired_reclaimed: number
  orphans_reclaimed: Array<{ kind: string; name: string; reason: string }>
  errors?: string[]
}

/** 项目绑定的临时资源试跑结果；masked_dsns 已由 agent 脱敏。 */
export interface DryRunResult {
  plans: Array<{ kind: string; resource_name: string; steps: string[]; detail?: Record<string, string> }>
  masked_dsns: string[]
  succeeded: boolean
  error?: string
}

/** dataSourceApi 是设置页使用的数据源/租约 HTTP client。 */
export const dataSourceApi = {
  /** 列出已登记管理连接；服务端响应不含 password。 */
  list: () => request<DataSource[]>('/api/datasources'),
  /** 登记数据源并立即探测能力。 */
  create: (payload: DataSourceInput) =>
    request<DataSource>('/api/datasources', { method: 'POST', body: JSON.stringify(payload) }),
  /** 更新数据源；password 留空表示沿用旧凭据。 */
  update: (id: string, payload: DataSourceInput) =>
    request<DataSource>(`/api/datasources/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(payload) }),
  /** 删除数据源；force 只由用户明确触发。 */
  remove: (id: string, force = false) =>
    request<void>(`/api/datasources/${encodeURIComponent(id)}${force ? '?force=true' : ''}`, { method: 'DELETE' }),
  /** 重探数据源能力。 */
  probe: (id: string) =>
    request<DataSourceProbeResult>(`/api/datasources/${encodeURIComponent(id)}/probe`, { method: 'POST' }),
  /** 列出当前临时租约，资源 DSN 由服务端清空。 */
  leases: () => request<Lease[]>('/api/test-databases'),
  /** 手动回收租约。 */
  reclaim: (leaseId: string) =>
    request<void>(`/api/test-databases/${encodeURIComponent(leaseId)}`, { method: 'DELETE' }),
  /** 触发一次实例对账。 */
  reconcile: () => request<ReconcileReport>('/api/test-databases/reconcile', { method: 'POST', body: JSON.stringify({}) }),
  /** 对项目绑定执行不落租约的试跑。 */
  dryRun: (projectId: string) =>
    request<DryRunResult>(`/api/projects/${encodeURIComponent(projectId)}/test-database/dry-run`, {
      method: 'POST',
      body: JSON.stringify({}),
    }),
}
