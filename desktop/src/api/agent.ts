// API 封装对 Go agent HTTP 接口的请求，统一处理 baseURL 和错误。
import { agentToken, invalidateAgentToken, withWsToken } from '@/lib/agentAuth'

// dev 模式对应开发版 agent（57018），build 后对应正式版（57017）。
// VITE_AGENT_HOST 只用于本地联调/截图 QA，避免临时 agent 占用默认端口。
const DEFAULT_AGENT_HOST = import.meta.env.DEV ? '127.0.0.1:57018' : '127.0.0.1:57017'
const AGENT_HOST = import.meta.env.VITE_AGENT_HOST || DEFAULT_AGENT_HOST
const BASE = `http://${AGENT_HOST}`
export const WS_BASE = `ws://${AGENT_HOST}`
export { AGENT_HOST }

export type AgentUninstallScriptName = 'uninstall-agent.sh' | 'uninstall-agent.ps1'

// agentUninstallScriptURL returns the local Controller URL for a version-matched manual script.
//
// The Controller serves scripts bundled with its own release, so callers must not substitute
// a latest-release URL that could drift from the installed Desktop version.
export function agentUninstallScriptURL(name: AgentUninstallScriptName): string {
  return `${BASE}/api/agent-uninstall-scripts/${encodeURIComponent(name)}`
}

// AgentAPIErrorPayload 描述 agent 结构化错误响应。
//
// 参数：
//   - code: 稳定错误码，例如 approval_required、approval_already_decided、existing_agent_detected
//   - error: 可展示错误信息
//   - stage: 生命周期操作的稳定失败阶段
//   - plan: operation 预检计划
//   - approval: 待处理审批请求
//   - decided_by: code 为 approval_already_decided 时，服务端从裁决方凭据推导的展示名
//   - version: code 为 existing_agent_detected 时，探测到的既有 agent 版本
//   - address: code 为 existing_agent_detected 时，权威的目标机直连地址（host:port，
//     取自本机为该 Host 配置的 direct 连接链项，不是探测用的临时连接）；链上
//     只有 tunnel（无 direct 项）时为空字符串
//   - data: 错误码特有的结构化副作用或恢复上下文
//
// 注意：
//   - 该结构只用于保留 agent 返回的错误上下文，不自行计算审批状态
export interface AgentAPIErrorPayload {
  code?: string
  error?: string
  stage?: string
  plan?: OperationPlan
  approval?: OperationApproval
  decided_by?: string
  version?: string
  address?: string
  data?: unknown
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
//   - stage 仅由服务端返回，不由前端推断
//   - approval token 不会出现在该错误对象中
export class AgentAPIError extends Error {
  status: number
  code?: string
  stage?: string
  plan?: OperationPlan
  approval?: OperationApproval
  decided_by?: string
  version?: string
  address?: string
  data?: unknown

  constructor(message: string, status: number, payload?: AgentAPIErrorPayload) {
    super(message)
    this.name = 'AgentAPIError'
    this.status = status
    this.code = payload?.code
    this.stage = payload?.stage
    this.plan = payload?.plan
    this.approval = payload?.approval
    this.decided_by = payload?.decided_by
    this.version = payload?.version
    this.address = payload?.address
    this.data = payload?.data
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

// isApprovalAlreadyDecidedError 判断错误是否为「该审批已被其他控制面裁决」的冲突响应（HTTP 409）。
//
// 参数：
//   - error: 任意捕获到的异常值
//
// 返回：
//   - true 表示 code 为 approval_already_decided，decided_by 携带获胜控制面的展示名
//
// 注意：
//   - 这是双控制面并发裁决下的常态信息，不是错误——调用方不应据此进入 error 态
//   - decided_by 由服务端从裁决方凭据推导，可能因裁决方展示名缺失而为空字符串，
//     调用方渲染时需与「有名字」区分，不要拼出「已由  处理」这种空洞文案
export function isApprovalAlreadyDecidedError(error: unknown): error is AgentAPIError & { decided_by?: string } {
  return error instanceof AgentAPIError && error.code === 'approval_already_decided'
}

// isExistingAgentDetectedError 判断错误是否为「安装预探测守卫拦截」的响应（HTTP 409）。
//
// 参数：
//   - error: 任意捕获到的异常值
//
// 返回：
//   - true 表示 code 为 existing_agent_detected，version 携带探测到的既有 agent
//     版本，address 携带权威的目标机直连地址（可能为空——见 AgentAPIErrorPayload
//     的 address 字段注释）
//
// 注意：
//   - 命中时不应盲目重试安装——调用方需要引导用户在「纳管」（发起接入请求，
//     经既有控制面审批换发独立凭据）与「强制重装」（显式确认、停掉对方在跑的
//     agent）之间选择，见 AgentConfigPanel 的既有 agent 检测分支
//   - 纳管请求必须使用 address，不能自己用本控制面的监听端口拼地址——那是
//     两回事（详见 AgentConfigPanel.vue 里 adoptionTargetURL 的注释）
export function isExistingAgentDetectedError(error: unknown): error is AgentAPIError & { version?: string; address?: string } {
  return error instanceof AgentAPIError && error.code === 'existing_agent_detected'
}

// requestHeaders 组装请求公共头并注入本机 agent token。
//
// 参数：
//   - headers: 调用方自带的头（如 postWithApprovalToken 族的 X-SuperDev-Approval-Token）
//
// 注意：
//   - 改为 async 是因为 agentToken() 经 Tauri IPC 读取，非同步操作
//   - Authorization 放在调用方头之后合并，不会被调用方覆盖
async function requestHeaders(headers?: HeadersInit): Promise<Record<string, string>> {
  const merged: Record<string, string> = {
    'Content-Type': 'application/json',
    'X-SuperDev-Requester': 'desktop',
    'X-SuperDev-Requester-Label': 'SuperDev Desktop',
  }
  if (headers) {
    if (typeof Headers !== 'undefined' && headers instanceof Headers) {
      headers.forEach((value, key) => {
        merged[key] = value
      })
    } else if (Array.isArray(headers)) {
      for (const [key, value] of headers) merged[key] = value
    } else {
      Object.assign(merged, headers as Record<string, string>)
    }
  }
  const token = await agentToken()
  if (token) merged.Authorization = `Bearer ${token}`
  return merged
}

// request 发送带本机 agent 鉴权头的 JSON 请求。
//
// 参数：
//   - path: agent REST 路径
//   - options: 可选 HTTP 方法、请求体与额外头
//
// 返回：解码后的 JSON 响应；非 2xx 响应抛出 AgentAPIError。
export async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const { headers, ...rest } = options ?? {}
  // 首次 401：多半是 agent 重启轮换了本机 token——失效缓存重取一次再试。
  // 仅在首次请求确实带了凭据时才重试：裸连（token 为 null）的 401 重发只会原样再失败一次。
  // 仍 401 则按既有错误路径抛出（真无凭据/凭据被拒），重试过程不打日志（高频路径）。
  const firstHeaders = await requestHeaders(headers)
  let res = await fetch(`${BASE}${path}`, {
    ...rest,
    headers: firstHeaders,
  })
  if (res.status === 401 && firstHeaders.Authorization) {
    invalidateAgentToken()
    res = await fetch(`${BASE}${path}`, {
      ...rest,
      headers: await requestHeaders(headers),
    })
  }
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

function postWithApprovalTokenAndBody(approvalToken?: string, body?: unknown): RequestInit {
  return {
    method: 'POST',
    headers: approvalToken ? { 'X-SuperDev-Approval-Token': approvalToken } : undefined,
    body: body == null ? undefined : JSON.stringify(body),
  }
}

export type DeployLocation = 'local' | 'remote'
export type RuntimeType = 'command' | 'language' | 'systemd' | 'launchd' | 'docker' | 'nginx_static' | 'external'
export type RuntimeIntent = 'start_dev' | 'start_normal' | 'debug_launch'
export type RuntimePreviewIntent = RuntimeIntent | 'attach'
export type ControlMode = 'monitor' | 'managed'
export type LogKind = 'process' | 'journalctl' | 'macos_log' | 'docker' | 'nginx' | 'file_tail' | 'command'
export type Health = 'running' | 'healthy' | 'restarting' | 'stopped' | 'failed' | 'unknown'
export type DebuggerState = 'none' | 'attached' | 'paused'
export type DebuggerOrigin = 'launched' | 'attached'

export interface PausedLocation {
  source: string
  line: number
}

export interface DebuggerStatus {
  state: DebuggerState
  language?: ServiceLanguage
  origin?: DebuggerOrigin
  lease_active?: boolean
  paused_at?: PausedLocation
}

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
  env_name: string
  deployment_id: string
  node_id: string
  node_name: string
  is_local: boolean
  error?: string
  metrics: InstanceMetrics
  debugger?: DebuggerStatus
  /** 该 deployment 声明的监听端口（来自共享层配置），端口镜像据此建立本机转发。 */
  ports?: number[]
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

export interface PipelineReservedVariable {
  name: string
  description: string
}

export interface RuntimeConfig {
  type: RuntimeType
  command?: string
  /** command runtime 的结构化可执行文件；存在时 agent 绕过平台 shell。 */
  executable?: string
  /** 与 executable 配套的逐项参数，避免平台 shell 转义差异。 */
  args?: string[]
  working_dir?: string
  cwd?: string
  env?: Record<string, string>
  config?: Record<string, unknown>
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

export interface WebReadinessConfig {
  type?: 'http' | string
  timeout_seconds?: number
}

/** 就绪探测配置：本服务何时算「起来了」。 */
export interface ReadinessProbe {
  type: 'http' | 'tcp'
  target: string
  timeout_seconds?: number
}

export interface WebAIDebugConfig {
  enabled: boolean
}

export interface WebEntrypointConfig {
  enabled: boolean
  url?: string
  default_path?: string
  readiness?: WebReadinessConfig
  ai_debug?: WebAIDebugConfig
}

export type ServiceLanguage = 'go' | 'node' | 'python' | 'java' | 'kotlin' | 'rust' | 'cpp'
export type CodeDebugProvider = 'go' | 'python' | 'node' | 'jvm' | 'native'
export type CodeDebugMode = 'launch'
export type CodeDebugPolicy = 'auto' | 'enabled' | 'disabled'

export interface LocalizedText {
  key: string
  default: string
  values?: Record<string, string>
}

export interface RuntimeSchemaField {
  key: string
  name: LocalizedText
  desc: LocalizedText
  type: 'string' | 'boolean' | 'number' | 'string_array'
  required: boolean
  default?: unknown
  group?: string
  order?: number
}

export interface RuntimeSchema {
  language: ServiceLanguage
  version: number
  title: LocalizedText
  description?: LocalizedText
  fields: RuntimeSchemaField[]
}

export interface RuntimeDiagnostic {
  severity: 'error' | 'warning' | 'info'
  field?: string
  code: string
  message: string
}

export interface RuntimeSuggestion {
  label: string
  cwd?: string
  env?: Record<string, string>
  config: Record<string, unknown>
  confidence: 'high' | 'medium' | 'low' | string
  reason?: string
}

export interface CodeDebugConfig {
  policy?: CodeDebugPolicy
  mode?: CodeDebugMode
  adapter_command?: string
  adapter_args?: string[]
  stop_on_entry?: boolean
}

// DebugCredential 是专供 AI 调试取用的明文凭据。
//
// 注意：
//   - 只在项目配置编辑和 MCP get_debug_credentials 出口使用
//   - 不应在普通服务列表、overview 或运行快照中展示
export interface DebugCredential {
  name: string
  value: string
  desc: string
}

export interface DebugCredentialHint {
  name: string
  desc?: string
  source: 'project' | 'service'
}

export interface PipelineEnvironment {
  variables?: Record<string, string>
}

export interface ProjectPipelineRole {
  from_service?: string
  hosts?: string[]
  environments?: Record<string, string[]>
}

export type ArtifactKind = 'file' | 'image'
export type SyncMode = 'transfer' | 'remote_cmd'

export interface ProjectPipeline {
  id: string
  name: string
  services?: string[]
  artifact_kind?: ArtifactKind
  variables?: Record<string, string>
  environments?: Record<string, PipelineEnvironment>
  roles?: Record<string, ProjectPipelineRole>
  sync_mode?: SyncMode
  sync_command?: string
  pipeline: Pipeline
}

export interface TemplateFileItem {
  from: string
  to: string
}

export type PipelineTemplateCategory = 'build' | 'deploy' | 'cleanup' | 'general'

export interface TemplateInput {
  label: string
  type: 'string' | 'number' | 'bool' | 'boolean' | 'select' | 'path' | 'target_role' | 'file_list'
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
  web?: WebEntrypointConfig
  /**
   * Ports 声明该 deployment 运行时监听的本机端口，端口镜像据此建立同端口转发。
   * 属共享层配置（project.yaml 随 git 流动）——不看 location：即使是本机部署，
   * 只要另一台桌面端把当前机器加为标了 dev_machine_mode 的远端主机，同样会消费这份声明。
   */
  ports?: number[]
  code_debug?: CodeDebugConfig
  /** 是否随 SuperDev 启动自动拉起（仅 local + managed 生效）。 */
  start_on_boot?: boolean
  /** 启动前需先就绪的同项目服务 ID 列表。 */
  depends_on?: string[]
  /** 本服务何时算就绪，供依赖它的服务等待。 */
  readiness?: ReadinessProbe
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
  ai_note?: string
  auth_hint?: string
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
  language?: ServiceLanguage
  deployments?: Deployment[]
  debug_credentials?: DebugCredential[]
  has_debug_credentials?: boolean
  debug_credential_hints?: DebugCredentialHint[]
  ai_note?: string
  auth_hint?: string
}

export interface Project {
  id: string
  name: string
  root_path: string
  /** 运行时探测的配置格式，不持久化；desktop 用它决定是否显示迁移横幅。 */
  config_format?: 'legacy' | 'split'
  /**
   * split 格式下旁边仍并存着一份被忽略的 config.yaml。
   * config_format 此时是 'split'，迁移横幅不会触发，必须靠这个标记单独提示
   * ——否则用户只看得到「服务起不来」这个现象。
   */
  config_stale_legacy?: boolean
  /**
   * 共享层 project.yaml（入库文件）里扫到的疑似密钥，值已由后端脱敏。
   * 「不挡、只亮」：只用于提示，后端不会因此拒绝保存。
   */
  shared_secret_warnings?: MigrationSuspect[]
  variables?: Record<string, string>
  services: Service[]
  debug_credentials?: DebugCredential[]
  has_debug_credentials?: boolean
  debug_credential_hints?: DebugCredentialHint[]
  ai_note?: string
  auth_hint?: string
  pipelines?: ProjectPipeline[]
  env_selected_service_ids?: Record<string, string[]>
  environments?: Environment[]
  /** 项目当前归属的主机 ID，缺省表示归属本机。 */
  home_host_id?: string
  /** home_host_id 对应的主机展示名；归属主机已被删除时为空（ID 仍保留）。 */
  home_host_name?: string
}

// ===== 配置分层迁移（legacy config.yaml → project.yaml + local.yaml） =====

/**
 * MigrationSuspect 是一条疑似密钥线索（迁移预览与共享层告警共用）。
 *
 * scope 前两个取值是可处置作用域（机器层 local.yaml 有对应 schema）；
 * pipeline_* 开头的只能告警——机器层对流水线没有任何表达能力。
 */
export interface MigrationSuspect {
  scope:
    | 'variables'
    | 'env_vars'
    | 'pipeline_variables'
    | 'pipeline_env_variables'
    | 'pipeline_dag_variables'
    | 'pipeline_step_with'
    | 'pipeline_sync_command'
  service?: string
  env?: string
  key: string
  masked_value: string
  reason: string
  /** pipeline_* 作用域下的流水线 ID/名称。 */
  pipeline?: string
  /** pipeline_step_with 下的步骤定位（phase/step）。 */
  detail?: string
  /** true 表示只能告警、无法处置：机器层没地方放，去留只能由人自己改配置。 */
  warn_only?: boolean
}

/** MigrationPlan 是配置分层迁移的预览结果。 */
export interface MigrationPlan {
  root_path: string
  suspects: MigrationSuspect[]
  ui_state_envs: string[]
  gitignore: { remove_lines: string[]; add_lines: string[] }
  service_count: number
  relativized_paths: string[]
}

/**
 * getConfigMigrationPreview 拉取项目的配置迁移预览。
 *
 * 返回：
 *   - MigrationPlan：项目仍是 legacy 格式，包含可迁移的内容
 *   - { status: 'not_needed' }：项目已是 split 格式，无需迁移
 *
 * 注意：
 *   - 预览中的疑似密钥值已由后端脱敏（masked_value），不是明文
 */
export async function getConfigMigrationPreview(projectId: string): Promise<MigrationPlan | { status: 'not_needed' }> {
  return request(`/api/projects/${encodeURIComponent(projectId)}/config-migration`)
}

/**
 * applyConfigMigration 按处置决定执行配置迁移，返回更新后的项目。
 *
 * 参数：
 *   - decisions: 对疑似密钥的处置列表；未被显式处置的疑似项由后端默认落本机层
 *     （「不挡、只亮」——省略即安全，但显式传 'local' 同样合法且更明确）
 */
export async function applyConfigMigration(
  projectId: string,
  decisions: Array<{ scope: string; service?: string; env?: string; key: string; disposition: 'shared' | 'local' }>,
): Promise<Project> {
  return request(`/api/projects/${encodeURIComponent(projectId)}/config-migration`, {
    method: 'POST',
    body: JSON.stringify({ decisions }),
  })
}

export interface LogEntry {
  id: string
  /** per-deployment 单调逻辑序号（agent 采集入口分配）；旧数据/未分配为缺省。 */
  seq?: number
  cursor_id?: string
  deployment_id: string
  run_id: string
  timestamp: string
  level: string
  message: string
  stream: string
  repeat_count?: number
  fold_key?: string
  source_id?: string
}

function normalizeLogEntry<T extends LogEntry>(entry: T): T {
  return { ...entry, id: String(entry.id ?? '') }
}

function normalizeLogEntries<T extends LogEntry>(entries: T[] | undefined): T[] {
  return (entries ?? []).map(entry => normalizeLogEntry(entry))
}

function normalizeLogContextResponse(response: LogContextResponse): LogContextResponse {
  const items: Record<string, LogEntry[]> = {}
  for (const [deploymentId, entries] of Object.entries(response.items_by_deployment ?? {})) {
    items[deploymentId] = normalizeLogEntries(entries)
  }
  return {
    ...response,
    target_id: String(response.target_id ?? ''),
    items_by_deployment: items,
  }
}

function normalizeRemoteSearchResponse(response: RemoteSearchResponse): RemoteSearchResponse {
  return {
    ...response,
    entries: normalizeLogEntries(response.entries),
    service_columns: response.service_columns?.map(column => ({
      ...column,
      entries: normalizeLogEntries(column.entries),
    })),
  }
}

function normalizeDeploymentLogsResponse(response: DeploymentLogsResponse | LogEntry[]): DeploymentLogsResponse {
  if (Array.isArray(response)) {
    return { items: normalizeLogEntries(response) }
  }
  return {
    ...response,
    items: normalizeLogEntries(response.items),
    next: response.next
      ? {
          time: response.next.time,
          id: response.next.id != null ? String(response.next.id) : undefined,
        }
      : undefined,
  }
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
  artifact_keep_versions: number
  sample_seeded?: boolean
  onboarding_completed?: boolean
  approval?: ApprovalPolicy
  debug_browser?: DebugBrowserSettings
}

export type AgentSettingsPatch = Partial<
  Pick<
    AgentSettings,
    'log_retention_days' | 'artifact_keep_versions' | 'onboarding_completed' | 'approval' | 'debug_browser'
  >
>

export interface ApprovalPolicy {
  config_upsert: boolean
  pipeline_upsert: boolean
  pipeline_run: boolean
  template_import: boolean
  browser_debug_open: boolean
  code_debug_open: boolean
  code_debug_evaluate: boolean
  grace_minutes: number
}

export interface DebugBrowserConfig {
  id: string
  name: string
  executable_path: string
}

export interface DebugBrowserSettings {
  default_browser_id?: string
  profile_mode?: 'ephemeral' | 'persistent'
  allow_evaluate?: boolean
  session_ttl_minutes?: number
  browsers?: DebugBrowserConfig[]
}

export interface DebugBrowser {
  id: string
  name: string
  executable_path: string
  available: boolean
}

export interface BrowserTarget {
  project_id: string
  project_name: string
  service_id: string
  service_name: string
  deployment_id: string
  env_name: string
  base_url: string
  default_path: string
}

export interface BrowserSession {
  session_id: string
  deployment_id: string
  target_url: string
  browser_id: string
  debug_port: number
  browser_ws: string
  page_ws: string
  devtools_url: string
  created_at?: string
  last_used_at?: string
  alive?: boolean
  error?: string
  closed?: boolean
}

export interface OperationTarget {
  project_id?: string
  project_name?: string
  env_name?: string
  service_id?: string
  service_name?: string
  deployment_id?: string
  host_id?: string
  template_path?: string
  template_digest?: string
  pipeline_id?: string
  artifact_version?: string
  /**
   * 服务器侧推导的请求来源（目前只有 agent.adopt 使用）。
   *
   * 注意：这是审批行上唯一不可被请求方伪造的身份信息（agent 从连接对端地址取，
   * 不读任何请求头），展示时必须与「接入方自报的名字」明确区分。
   */
  request_origin?: string
  /**
   * 由请求 ID 派生的短配对码（目前只有 agent.adopt 使用），供发起方与批准方
   * 口头核对是不是同一次请求。
   *
   * 注意：它不是秘密、也不是鉴权因子，只是匹配辅助——绝不能拿它当准入校验。
   */
  pairing_code?: string
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

export interface OperationApprovalDecision {
  approval: OperationApproval
  grace_granted: boolean
  grace_expires_at?: string
}

// OperationApprovalsSnapshot 是 /ws/operation-approvals 每帧推送的全量快照。
//
// 参数：
//   - pending: 当前待裁决审批
//   - decided: 最近已终结的审批（approved/rejected/expired/used），24h 内按 updated_at
//     降序、最多 50 条
//
// 注意：
//   - 两段都是全量快照，不是增量事件——收到即整体替换本地状态，不做增量 merge，
//     天然对丢帧免疫（与 /ws/port-mirrors 同一套契约，见 portMirror store 注释）
//   - decided 段里 expired 终态没有裁决人，decided_by 是空字符串；渲染时必须与
//     「有 decided_by」的场景区分，不要拼出「已由  处理」这种空洞文案
export interface OperationApprovalsSnapshot {
  pending: OperationApproval[]
  decided: OperationApproval[]
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
  target_id: string
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
  cursor_id?: string
}

export interface FetchLogContextParams {
  project: string
  id: string
  target_deployment?: string
  deployment?: string[]
  before_ms?: number
  after_ms?: number
}

export interface FetchLogContextPageParams {
  project: string
  deployment: string
  direction: LogContextPageDirection
  cursor_time: string
  cursor_id: string
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
  ssh_credential_configured?: boolean
  ssh_password_configured?: boolean
  ssh_private_key_configured?: boolean
  ssh_host_key_fingerprint_configured?: boolean
  /** 该主机是否被当前控制面当作开发机消费（端口镜像开关）；本机 is_self 节点恒为 false。 */
  dev_machine_mode?: boolean
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
  web?: WebEntrypointConfig
  /** 该 deployment 声明的监听端口，随 project.yaml 持久化，供端口镜像使用。 */
  ports?: number[]
  code_debug?: CodeDebugConfig
  /** 是否随 SuperDev 启动自动拉起（仅 local + managed 生效）。 */
  start_on_boot?: boolean
  /** 启动前需先就绪的同项目服务 ID 列表。 */
  depends_on?: string[]
  /** 本服务何时算就绪，供依赖它的服务等待。 */
  readiness?: ReadinessProbe
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
  language?: ServiceLanguage
  ai_note?: string
  auth_hint?: string
  debug_credentials?: DebugCredential[]
  deployments: SetupDeployment[]
}

export interface SetupPayload {
  variables?: Record<string, string>
  environments: Array<{ id?: string; name: string; is_dev: boolean; order: number; ai_note?: string; auth_hint?: string }>
  ai_note?: string
  auth_hint?: string
  services: SetupServiceEntry[]
  debug_credentials?: DebugCredential[]
  pipelines?: ProjectPipeline[]
}

export interface ProjectPipelinePreviewRequest {
  env_name: string
  service_names?: string[]
  variables?: Record<string, string>
}

/** SshKey 是 agent 在本机 ~/.ssh 扫描到的一个私钥候选。 */
export interface SshKey {
  path: string
  name: string
  type: string
  encrypted: boolean
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

export interface NodeSystemFacts {
  os: string
  kernel_arch: string
  agent_arch: string
  agent_node_id: string
  machine_id_sha256: string
}

export interface NodeStatus {
  host_id: string
  name?: string
  reachable: boolean
  agent: AgentRuntime
  deployments: RuntimeInstanceStatus[]
  managed?: ManagedDeploymentStatus
  system?: NodeSystemFacts
  route?: RouteStatus
  /**
   * 该 agent 本机是否有活跃 /ws/nodes 订阅——即该机器上是否也开着桌面端在场。
   * 用 /ws/nodes 订阅数当信号：桌面主界面常驻这条订阅，且它自带 5s 心跳帧，
   * 因此免费获得 ≤5s 时效（见 agent/nodetransport/transport.go NodeStatus 字段注释）。
   */
  desktop_online?: boolean
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
  release_base_url?: string
  bind_address?: string
  remote_agent_port?: number
  transport_type?: TransportType
  token_ttl_minutes?: number
}

export interface AgentInstallPayload {
  method: 'push_over_ssh'
  /**
   * force_reinstall 为 true 时跳过后端「既有 agent 探测守卫」，用户显式确认要
   * 盲目重装（会停掉对方在跑的 agent、覆盖其凭据）。省略/false 时，探测到既有
   * provisioned agent 会收到 409 existing_agent_detected，调用方应引导改走纳管。
   */
  force_reinstall?: boolean
}

export interface AgentUninstallPayload {
  remove_data: boolean
}

export interface AgentUninstallResponse {
  ok: boolean
  host_id: string
  removed_data: boolean
  message: string
}

export type AgentDetachReason = 'manual_uninstall_failed'

export interface AgentDetachPayload {
  reason: AgentDetachReason
}

export interface AgentDetachResponse {
  status: 'detached'
  host_id: string
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
  /**
   * guard_probe 为 'inconclusive' 时表示安装前的「既有 agent 探测守卫」无法
   * 断定目标机状态（超时/握手异常/非 agent 服务），安装照常执行但用户应自行
   * 确认目标机上没有别人在用的 agent——静默放行会掩盖「探测预算不够」被翻译
   * 成「盲目重装」的风险。省略表示探测有确定结论（未探到 / 已被 409 拦截）。
   */
  guard_probe?: 'inconclusive'
}

export interface AgentRestartResponse {
  ok: boolean
  host_id: string
  platform: string
  message: string
}

export interface AgentUpdateTargetResponse {
  version: string
  source: 'bundled' | string
  concurrency_default: number
}

export interface AgentUpdateBinaryResponse {
  ok: boolean
  host_id: string
  platform: string
  version: string
  message: string
  updated_at: string
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
  host_key_verified?: boolean
  host_key_identity_sha256?: string
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
  active_collector_count: number
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
  active_collector_count: number
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
  before?: string
  beforeTime?: string
}

export interface DeploymentLogsResponse {
  items: LogEntry[]
  next?: {
    time?: string
    id?: string
  }
}

export interface DeploymentSearchParams {
  deploymentId: string
  q: string
  limit?: number
  cursor_time?: string
  cursor_id?: string
}

export interface DeploymentSearchResponse {
  items: LogEntry[]
  cursor: { time: string; id: string } | null
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
  ssh_host_key_fingerprint?: string
  clear_ssh_password?: boolean
  clear_ssh_private_key?: boolean
  clear_ssh_host_key_fingerprint?: boolean
  /** 勾选后该主机进入端口镜像的期望态计算范围；创建/更新本机 is_self 节点时后端强制忽略。 */
  dev_machine_mode?: boolean
}

export type HostUpdatePayload = Partial<HostCreatePayload>

/**
 * HostUpdateResponse 是 PUT /api/hosts/{id} 的响应体：标准 Host 视图 + 可选 homed_projects。
 *
 * 注意：
 *   - homed_projects 仅当本次更新把 dev_machine_mode 由 true 改为 false，且该 Host
 *     当前仍是若干项目的归属时才非空（后端 hostUpdateResponseDTO 语义）
 */
export interface HostUpdateResponse extends Host {
  homed_projects?: string[]
}

/** ScanHostKeyResult 是主机 SSH host key 采集接口的成功响应。 */
export interface ScanHostKeyResult {
  fingerprint: string
}

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

// ===== 端口镜像（port mirror） =====

/** MirrorOccupier 描述 conflict 态下占用本机端口的进程。 */
export interface MirrorOccupier {
  pid: number
  name: string
  started_at: string
  /** 非空表示占用者是 SuperDev 托管 deployment，处理动作走 stop_service 语义。 */
  managed_deployment_id?: string
}

export type MirrorState = 'pending' | 'active' | 'conflict' | 'failed'

/** MirrorStatus 是一条「host × 端口」端口镜像的当前状态，对应 agent 侧 portmirror.MirrorStatus。 */
export interface MirrorStatus {
  host_id: string
  host_name: string
  deployment_id: string
  service_name: string
  port: number
  state: MirrorState
  /** conflict 固定 "port_mirror_conflict"；failed 为脱敏码或重复声明码。 */
  error?: string
  /** 仅 conflict 且占用者识别成功时存在。 */
  occupier?: MirrorOccupier
  updated_at: string
}

// ===== 项目归属转移（project home transfer） =====

/**
 * TransferCheckItem 是预检响应 / 结束后资产报告中的单条检查结果。
 *
 * code 是稳定码，供前端按类型分支处理（如 uncommitted/unpushed/running_dev/
 * no_upstream/not_a_git_repo/no_git_binary/checkout_reuse/checkout_clone/
 * remote_url_mismatch），detail 是可展示的中文说明。
 */
export interface TransferCheckItem {
  code: string
  detail: string
}

/** TransferPreflightResponse 是转移预检（只读探测，不执行任何写操作）的结果。 */
export interface TransferPreflightResponse {
  /** 必须先处理的阻塞项，任一存在则不可执行转移。 */
  blockers: TransferCheckItem[]
  /** 就绪确认项（含需人审的检出复用提示）。 */
  ready: TransferCheckItem[]
  /** 回显实际生效的目标目录：target_dir 留空时由后端按默认规则计算。 */
  target_dir: string
  /** 本机当前分支；执行转移时目标机检出同一分支。 */
  branch: string
}

export type TransferStepState = 'pending' | 'running' | 'done' | 'failed' | 'skipped'

/** TransferStep 是转移执行过程中单个步骤的状态。 */
export interface TransferStep {
  code: string
  state: TransferStepState
  detail?: string
}

export type TransferRunState = 'running' | 'succeeded' | 'failed'

/**
 * TransferStatusResponse 是一次转移执行的状态快照。
 *
 * 注意：
 *   - POST 转移/迁回接口的 202 响应体与 GET status 接口共用同一形状
 *   - asset_report 仅在转移结束（非 running）后可能非空
 */
export interface TransferStatusResponse {
  state: TransferRunState
  steps: TransferStep[]
  /** 需在新家启动的服务 + env 文件缺失 + 疑似密钥键名等提示；仅结束后可能非空。 */
  asset_report?: TransferCheckItem[]
  error?: string
}

// ===== 纳管既有 agent（agent adoption） =====
//
// 与本文件其余类型不同：这三个端点的请求目标是「目标机 agent」本身，不是本机
// agent（BASE）——目标机此刻被别的控制面管着，本控制面手上没有它的任何凭据，
// 三个端点全部在目标机的 bypass 白名单内（无需凭据即可访问）。调用方（
// AgentConfigPanel）沿用「添加主机」表单里已知的目标地址拼出 hostUrl，见
// desktop/src/lib/agentBind.ts 的 recommendedDirectAddress。

/** AdoptionState 镜像 agent/security/adoption.go 的 AdoptionRequest 状态机。 */
export type AdoptionState = 'pending' | 'approved' | 'rejected' | 'expired'

/** AdoptionCreateResponse 是 POST .../adoption-requests 的成功响应。 */
export interface AdoptionCreateResponse {
  id: string
  /** 目标机派生的配对码，需展示给发起纳管的人，让他念给按下批准的人核对。 */
  pairing_code: string
  state: AdoptionState
  expires_at: string
}

/**
 * AdoptionStatusResponse 是 GET .../adoption-requests/{id} 的响应。
 *
 * 注意：
 *   - adoption_token 只在 state=='approved' 且本次是首次领取时非空——目标机侧
 *     一次性发放，领取后立即置为已消费，此后再 GET 恒不再带出，调用方拿到就
 *     必须立刻兑换，不能缓存等下次用
 */
export interface AdoptionStatusResponse {
  state: AdoptionState
  adoption_token?: string
}

/** TokenRecord 镜像 agent/security/store.go 的 TokenRecord（hash 已脱敏，非明文）。 */
export interface TokenRecord {
  id: string
  name: string
  hash: string
  issued_at: string
}

/** AdoptionExchangeResponse 是 POST .../exchange 的成功响应；token 是本控制面的长期凭据。 */
export interface AdoptionExchangeResponse {
  token: string
  record: TokenRecord
}

/** AgentAdoptResponse 是本机 POST /api/agents/{host_id}/adopt 的响应（落盘凭据的本机端点）。 */
export interface AgentAdoptResponse {
  status: string
}

// 纳管三请求经本机 agent 的代理端点（/api/agents/{host_id}/adoption-requests*）
// 转发到目标机，不再从 webview 裸 fetch 直连目标机：目标机默认（tls_mode=auto）
// 是自签 HTTPS 监听，webview 既不能跳过证书校验也没有对方 CA，硬编码 http://
// 必然连不上；本机 agent 按 host_id 复用已配置的连接链（direct/tunnel 皆可）
// 并处理目标 TLS 姿态未知的问题。走 request() 还统一了稳定错误码解析
// （429 限流、502 adoption_target_unreachable 等经 AgentAPIError.code 可辨）。

/**
 * requestAdoption 向目标机发起一次无凭据接入请求（经本机 agent 代理）。
 *
 * 参数：
 *   - hostId: 目标主机在本机主机簿里的 host_id（代理据此走已配置的连接链）
 *   - name: 本控制面自报的展示名，供目标机审批列表和审计展示
 *
 * 注意：
 *   - 目标机对 30s 内的 pending 请求数限流（超限 429），调用方需把该失败态
 *     可视化，不能静默重试刷屏
 */
export function requestAdoption(hostId: string, name: string): Promise<AdoptionCreateResponse> {
  return request<AdoptionCreateResponse>(`/api/agents/${encodeURIComponent(hostId)}/adoption-requests`, {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

/** getAdoptionStatus 轮询接入请求的当前状态；approved 时可能一次性附带 adoption_token。 */
export function getAdoptionStatus(hostId: string, id: string): Promise<AdoptionStatusResponse> {
  return request<AdoptionStatusResponse>(`/api/agents/${encodeURIComponent(hostId)}/adoption-requests/${encodeURIComponent(id)}`)
}

/** exchangeAdoption 用一次性 adoption_token 兑换本控制面的长期凭据；只能成功兑换一次。 */
export function exchangeAdoption(hostId: string, id: string, adoptionToken: string): Promise<AdoptionExchangeResponse> {
  return request<AdoptionExchangeResponse>(`/api/agents/${encodeURIComponent(hostId)}/adoption-requests/${encodeURIComponent(id)}/exchange`, {
    method: 'POST',
    body: JSON.stringify({ adoption_token: adoptionToken }),
  })
}

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
  continueDeploymentDebug: (id: string) =>
    request<void>(`/api/deployments/${encodeURIComponent(id)}/debug/continue`, { method: 'POST' }),
  listNodes: () => request<NodeStatus[]>('/api/nodes'),
  listPortMirrors: () => request<MirrorStatus[]>('/api/port-mirrors'),
  retryPortMirror: (hostId: string, port: number) =>
    request<void>('/api/port-mirrors/retry', {
      method: 'POST',
      body: JSON.stringify({ host_id: hostId, port }),
    }),
  stopMirrorOccupier: (hostId: string, port: number) =>
    request<void>('/api/port-mirrors/stop-occupier', {
      method: 'POST',
      body: JSON.stringify({ host_id: hostId, port }),
    }),
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
  uninstallAgent: (hostId: string, payload: AgentUninstallPayload) =>
    request<AgentUninstallResponse>(`/api/agents/${encodeURIComponent(hostId)}/uninstall`, {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  detachAgent: (hostId: string, payload: AgentDetachPayload) =>
    request<AgentDetachResponse>(`/api/agents/${encodeURIComponent(hostId)}/detach`, {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  restartAgent: (hostId: string) =>
    request<AgentRestartResponse>(`/api/agents/${encodeURIComponent(hostId)}/restart`, { method: 'POST' }),
  getAgentUpdateTarget: () =>
    request<AgentUpdateTargetResponse>('/api/agents/update-target'),
  updateAgentBinary: (hostId: string) =>
    request<AgentUpdateBinaryResponse>(`/api/agents/${encodeURIComponent(hostId)}/update-binary`, {
      method: 'POST',
      body: JSON.stringify({}),
    }),
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
  /**
   * adoptAgent 把纳管流程（exchangeAdoption）换得的长期 token 落盘到本机 agents.json。
   *
   * 注意：
   *   - 这是本机 agent 的端点（走 request()/BASE/本机 token），与上面纳管三端点
   *     直连目标机不同——token 已经在目标机侧确认有效，这一步只是记到本地
   *   - 该端点只更新已存在的 Agent 记录，不创建；token 不会出现在响应体里
   */
  adoptAgent: (hostId: string, token: string) =>
    request<AgentAdoptResponse>(`/api/agents/${encodeURIComponent(hostId)}/adopt`, {
      method: 'POST',
      body: JSON.stringify({ token }),
    }),

  // 设置
  getSettings: () => request<AgentSettings>('/api/settings'),
  putSettings: (settings: AgentSettingsPatch) =>
    request<AgentSettings>('/api/settings', { method: 'PUT', body: JSON.stringify(settings) }),

  // Browser Debug
  listDebugBrowsers: () => request<DebugBrowser[]>('/api/debug-browsers'),
  detectDebugBrowsers: () => request<DebugBrowser[]>('/api/debug-browsers/detected'),
  listBrowserTargets: () => request<BrowserTarget[]>('/api/browser-targets'),
  openBrowserSession: (
    payload: { deployment_id: string; browser_id?: string; path?: string; open_devtools?: boolean },
    approvalToken?: string,
  ) =>
    request<BrowserSession>('/api/browser-sessions', {
      method: 'POST',
      headers: approvalToken ? { 'X-SuperDev-Approval-Token': approvalToken } : undefined,
      body: JSON.stringify(payload),
    }),
  closeBrowserSession: (sessionId: string) =>
    request<BrowserSession>(`/api/browser-sessions/${encodeURIComponent(sessionId)}`, { method: 'DELETE' }),

  // Operation 审批
  listOperationApprovals: (params?: { status?: string; project_id?: string; limit?: number }) =>
    request<OperationApproval[]>(`/api/operation-approvals${qs(params)}`),
  getOperationApproval: (id: string) =>
    request<OperationApprovalDetail>(`/api/operation-approvals/${encodeURIComponent(id)}`),
  approveOperationApproval: (id: string, payload: { decided_by: string; note?: string; grant_grace?: boolean }) =>
    request<OperationApprovalDecision>(`/api/operation-approvals/${encodeURIComponent(id)}/approve`, {
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

  // Language Runtime Provider
  listLanguageRuntimeProviders: () =>
    request<{ languages: ServiceLanguage[] }>('/api/language-runtime/providers'),
  describeLanguageRuntimeSchema: (language: ServiceLanguage) =>
    request<RuntimeSchema>(`/api/language-runtime/${encodeURIComponent(language)}/schema`),
  suggestServiceRuntime: (language: ServiceLanguage, body: { project_root: string; cwd?: string }) =>
    request<{ suggestions: RuntimeSuggestion[] }>(
      `/api/language-runtime/${encodeURIComponent(language)}/suggest`,
      { method: 'POST', body: JSON.stringify(body) },
    ),
  validateServiceRuntime: (language: ServiceLanguage, body: { project_root: string; cwd?: string; env?: Record<string, string>; config?: Record<string, unknown> }) =>
    request<{ valid: boolean; diagnostics: RuntimeDiagnostic[] }>(
      `/api/language-runtime/${encodeURIComponent(language)}/validate`,
      { method: 'POST', body: JSON.stringify(body) },
    ),
  previewServiceExecution: (language: ServiceLanguage, body: { project_root: string; cwd?: string; env?: Record<string, string>; config?: Record<string, unknown>; intent?: RuntimePreviewIntent; artifact_dir?: string }) =>
    request<{ preview?: string; diagnostics?: RuntimeDiagnostic[] }>(
      `/api/language-runtime/${encodeURIComponent(language)}/preview`,
      { method: 'POST', body: JSON.stringify(body) },
    ),

  // 服务
  listServices: (projectId?: string) => {
    const qs = projectId ? `?project_id=${projectId}` : ''
    return request<Service[]>(`/api/services${qs}`)
  },

  // Deployment 进程控制
  startDeployment: (id: string, intent?: RuntimeIntent, approvalToken?: string) =>
    request<void>(
      `/api/deployments/${encodeURIComponent(id)}/start`,
      postWithApprovalTokenAndBody(approvalToken, intent ? { intent } : undefined),
    ),
  stopDeployment: (id: string, approvalToken?: string) =>
    request<void>(`/api/deployments/${encodeURIComponent(id)}/stop`, postWithApprovalToken(approvalToken)),
  restartDeployment: (id: string, intent?: RuntimeIntent, approvalToken?: string) =>
    request<void>(
      `/api/deployments/${encodeURIComponent(id)}/restart`,
      postWithApprovalTokenAndBody(approvalToken, intent ? { intent } : undefined),
    ),
  startDeploymentOnHost: (id: string, hostId: string, approvalToken?: string) =>
    request<void>(
      `/api/deployments/${encodeURIComponent(id)}/hosts/${encodeURIComponent(hostId)}/start`,
      postWithApprovalToken(approvalToken),
    ),
  stopDeploymentOnHost: (id: string, hostId: string, approvalToken?: string) =>
    request<void>(
      `/api/deployments/${encodeURIComponent(id)}/hosts/${encodeURIComponent(hostId)}/stop`,
      postWithApprovalToken(approvalToken),
    ),
  restartDeploymentOnHost: (id: string, hostId: string, approvalToken?: string) =>
    request<void>(
      `/api/deployments/${encodeURIComponent(id)}/hosts/${encodeURIComponent(hostId)}/restart`,
      postWithApprovalToken(approvalToken),
    ),

  // Pipeline 模板与预览
  listPipelineReservedVariables: () => request<PipelineReservedVariable[]>('/api/pipeline/reserved-variables'),
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
  deployProjectPipeline: (projectId: string, pipelineId: string, payload: ProjectPipelineDeployRequest, approvalToken?: string) =>
    request<Run>(
      `/api/projects/${encodeURIComponent(projectId)}/pipelines/${encodeURIComponent(pipelineId)}/deploy`,
      postWithApprovalTokenAndBody(approvalToken, payload),
    ),
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
      .then(normalizeLogEntries)
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
      .then(response => ({ ...response, items: normalizeLogEntries(response.items) }))
  },
  fetchLogContext: (params: FetchLogContextParams) => {
    const qs = new URLSearchParams()
    qs.set('project', params.project)
    qs.set('id', String(params.id))
    if (params.target_deployment) qs.set('target_deployment', params.target_deployment)
    for (const deploymentId of params.deployment ?? []) qs.append('deployment', deploymentId)
    if (params.before_ms) qs.set('before_ms', String(params.before_ms))
    if (params.after_ms) qs.set('after_ms', String(params.after_ms))
    return request<LogContextResponse>(`/api/logs/context?${qs}`)
      .then(normalizeLogContextResponse)
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
      .then(response => ({ ...response, items: normalizeLogEntries(response.items) }))
  },
  /** postFrontendDiagnostics 批量上报前端诊断事件到 agent，落为 __desktop__ 虚拟 deployment 日志。 */
  postFrontendDiagnostics: (events: Record<string, unknown>[]) =>
    request<{ accepted: number }>('/api/frontend-diagnostics', {
      method: 'POST',
      body: JSON.stringify({ events }),
    }),

  // 远程监听：Host CRUD
  listHosts: () => request<Host[]>('/api/hosts'),
  scanHostKey: (payload: { ssh_host: string; ssh_port: number }) =>
    request<ScanHostKeyResult>('/api/hosts/scan-host-key', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  createHost: (payload: HostCreatePayload) =>
    request<Host>('/api/hosts', { method: 'POST', body: JSON.stringify(payload) }),
  updateHost: (id: string, payload: HostUpdatePayload) =>
    request<HostUpdateResponse>(`/api/hosts/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
  /**
   * deleteHost 删除一个 Host。
   *
   * 注意：
   *   - 该 Host 仍是若干项目的归属时返回 409，错误体带稳定码 project_home
   *     （经 request() 统一解析后体现为 AgentAPIError.code === 'project_home'，
   *     data 携带 { host_id, projects: string[] }），调用方（Task 12）据此渲染
   *     "先在项目概览迁回"引导弹窗，不提供强制删除选项
   */
  deleteHost: (id: string) =>
    request<void>(`/api/hosts/${id}`, { method: 'DELETE' }),
  getHostManagedDeploymentStatus: (id: string) =>
    request<HostManagedDeploymentStatus>(`/api/hosts/${id}/managed-deployments/status`),

  // 项目归属转移（project home transfer）
  /**
   * transferPreflight 只读预检项目转移到目标 host 是否可执行。
   *
   * 参数：
   *   - projectId: 项目 ID
   *   - hostId: 转移目标 host ID
   *   - targetDir: 目标机项目路径；留空时后端按 "~/workspace/<项目目录名>" 计算默认值
   *
   * 端点：POST /api/projects/{projectId}/transfer/preflight
   *
   * 注意：
   *   - 防御性归一化：blockers/ready 在 TS 类型里声明为非空数组，但"全绿"（无
   *     阻塞项）恰恰是最常见的 happy path——历史上后端曾在这条路径用 nil 切片
   *     编码出 `null`（已在 agent 侧修复为固定编码成 `[]`），此处仍兜底把
   *     null/undefined 转成 `[]`，双保险防止未来任何一侧的回归让调用方在
   *     `.length`/`.map(...)` 上直接崩溃
   */
  transferPreflight: (projectId: string, hostId: string, targetDir?: string) =>
    request<TransferPreflightResponse>(`/api/projects/${encodeURIComponent(projectId)}/transfer/preflight`, {
      method: 'POST',
      body: JSON.stringify({ host_id: hostId, target_dir: targetDir ?? '' }),
    }).then(res => ({ ...res, blockers: res.blockers ?? [], ready: res.ready ?? [] })),
  /**
   * startTransfer 异步启动一次正向转移（本机 → 目标 host）。
   *
   * 参数：同 transferPreflight
   *
   * 返回：
   *   - 202 + 转移的初始状态快照（与 transferStatus 同形状），调用方随后轮询
   *     transferStatus 跟踪进度
   *
   * 注意：
   *   - 同项目已有进行中的转移会以 409 拒绝（AgentAPIError.status === 409）
   *
   * 端点：POST /api/projects/{projectId}/transfer
   */
  startTransfer: (projectId: string, hostId: string, targetDir?: string) =>
    request<TransferStatusResponse>(`/api/projects/${encodeURIComponent(projectId)}/transfer`, {
      method: 'POST',
      body: JSON.stringify({ host_id: hostId, target_dir: targetDir ?? '' }),
    }),
  /**
   * transferStatus 查询项目当前/最近一次转移的执行状态。
   *
   * 参数：
   *   - projectId: 项目 ID
   *
   * 注意：
   *   - 404（AgentAPIError.status === 404）表示该项目从未发起过转移，或 agent
   *     进程重启后内存态已丢失——调用方应据此判断"当前无进行中的转移"，
   *     而不是当作请求失败处理
   *
   * 端点：GET /api/projects/{projectId}/transfer/status
   */
  transferStatus: (projectId: string) =>
    request<TransferStatusResponse>(`/api/projects/${encodeURIComponent(projectId)}/transfer/status`),
  /**
   * transferBack 异步启动一次迁回（归属机 → 本机）。
   *
   * 参数：
   *   - projectId: 项目 ID
   *
   * 注意：
   *   - 目标 host 由后端按项目当前归属反查得到，不由调用方指定
   *   - 项目已归属本机时返回 400
   *
   * 返回：202 + 转移的初始状态快照（与 transferStatus 同形状）
   *
   * 端点：POST /api/projects/{projectId}/transfer-back
   */
  transferBack: (projectId: string) =>
    request<TransferStatusResponse>(`/api/projects/${encodeURIComponent(projectId)}/transfer-back`, {
      method: 'POST',
    }),

  // 远程监听：SSH config 导入
  listSshConfigHosts: () => request<SshConfigEntry[]>('/api/ssh-config/hosts'),
  // 远程监听：本机私钥扫描（只返回路径与元信息，不含密钥内容）
  listSshKeys: () => request<SshKey[]>('/api/ssh-keys'),

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
  // 例外：这里刻意不走 request()/本机 token。localPort 是隧道在本地转发出来的端口，
  // 请求实际落到的是【远端】agent，而不是本机 agent——本机的 local-access-token 对它
  // 没有意义，塞进去只会被远端按错误凭据拒绝。这条连接的 Authorization 由隧道层
  // applyAgentHeaders 用远端 token 单独注入，此处保持裸 fetch。
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
      .then(normalizeRemoteSearchResponse)
  },

  // Deployment 统一日志接口
  fetchDeploymentLogs: (params: DeploymentFetchLogsParams) => {
    const qs = new URLSearchParams()
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.before != null) qs.set('before', String(params.before))
    if (params.beforeTime) qs.set('before_time', params.beforeTime)
    const q = qs.toString()
    return request<DeploymentLogsResponse | LogEntry[]>(`/api/deployments/${encodeURIComponent(params.deploymentId)}/logs${q ? '?' + q : ''}`)
      .then(normalizeDeploymentLogsResponse)
  },
  searchDeploymentLogs: (params: DeploymentSearchParams) => {
    const qs = new URLSearchParams()
    qs.set('q', params.q)
    if (params.limit) qs.set('limit', String(params.limit))
    if (params.cursor_time) qs.set('cursor_time', params.cursor_time)
    if (params.cursor_id != null) qs.set('cursor_id', String(params.cursor_id))
    return request<DeploymentSearchResponse>(`/api/deployments/${encodeURIComponent(params.deploymentId)}/search?${qs}`)
      .then(response => ({ ...response, items: normalizeLogEntries(response.items) }))
  },
}

// deploymentWsUrl 返回指定 deployment 的 WebSocket 日志流 URL。
//
// 注意：
//   - 改为 async 是因为服务端 /ws/ 路径鉴权走 query 参数 access_token（浏览器 WebSocket
//     无法设 Authorization 头），需经 withWsToken 异步读取本机 token 后拼接
export async function deploymentWsUrl(
  deploymentId: string,
  opts?: { replay?: number; sinceTime?: string; sinceId?: string },
): Promise<string> {
  const base = `${WS_BASE}/ws/deployments/${encodeURIComponent(deploymentId)}/logs`
  const q = new URLSearchParams()
  if (opts?.replay) q.set('replay', String(opts.replay))
  if (opts?.sinceTime) q.set('since_time', opts.sinceTime)
  if (opts?.sinceId) q.set('since_id', opts.sinceId)
  const encoded = q.toString()
  return withWsToken(encoded ? `${base}?${encoded}` : base)
}

/** nodesWsUrl 返回 NodeRegistry 快照 WebSocket URL（同上，附带 access_token）。 */
export async function nodesWsUrl(): Promise<string> {
  return withWsToken(`${WS_BASE}/ws/nodes`)
}

/** portMirrorWsUrl 返回端口镜像状态 WebSocket URL（同上，附带 access_token）。 */
export async function portMirrorWsUrl(): Promise<string> {
  return withWsToken(`${WS_BASE}/ws/port-mirrors`)
}

/** operationApprovalsWsUrl 返回操作审批快照 WebSocket URL（同上，附带 access_token）。 */
export async function operationApprovalsWsUrl(): Promise<string> {
  return withWsToken(`${WS_BASE}/ws/operation-approvals`)
}

/** runLogsWsUrl 返回指定 pipeline run 的 WebSocket 日志流 URL（同上，附带 access_token）。 */
export async function runLogsWsUrl(runId: string): Promise<string> {
  return withWsToken(`${WS_BASE}/ws/runs/${encodeURIComponent(runId)}/logs`)
}
