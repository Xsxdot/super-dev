/**
 * 项目配置草稿模型与转换/校验纯函数。
 *
 * 职责：
 *   - projectToDraft：把 Project 深拷贝成可编辑草稿
 *   - draftToPayload：把草稿拍平为后端 SetupPayload（忽略空 key 的 env 变量）
 *   - validateDraft：保存前校验，返回错误信息数组（空数组 = 通过）
 *
 * 边界：
 *   - 纯数据转换，不发请求、不依赖 Vue
 */
import type {
  Project,
  Deployment,
  Environment,
  SetupPayload,
  SetupDeployment,
  ProjectPipeline,
  Pipeline,
  PipelineStep,
  RuntimeConfig,
  RuntimeType,
  LogConfig,
  LogKind,
  LogSourceType,
  ControlMode,
} from '@/api/agent'

export interface ConfigDraftService {
  id: string
  name: string
  required: boolean
  order: number
  deployments: Deployment[]
}

export interface ConfigDraft {
  variables: Record<string, string>
  environments: Environment[]
  services: ConfigDraftService[]
  pipelines: ProjectPipeline[]
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value))
}

function serviceNameFromLogTarget(target?: string): string | undefined {
  if (!target) return undefined
  return target.endsWith('.service') ? target.slice(0, -'.service'.length) : target
}

function inferRuntimeType(d: Deployment): RuntimeType {
  if (d.runtime?.type) return d.runtime.type
  if (d.command !== undefined || d.work_dir !== undefined || d.env_file !== undefined || d.env !== undefined) return 'command'
  if (d.log_type === 'docker') return 'docker'
  if (d.log_type === 'journalctl' || d.log_target || d.start_command || d.stop_command) return 'systemd'
  return d.location === 'local' ? 'command' : 'external'
}

function inferControlMode(d: Deployment): ControlMode {
  if (d.control_mode) return d.control_mode
  if (d.read_only || d.runtime?.type === 'external') return 'monitor'
  return 'managed'
}

function normalizeRuntime(d: Deployment): RuntimeConfig {
  const source = d.runtime ?? ({} as RuntimeConfig)
  const runtime: RuntimeConfig = { type: inferRuntimeType(d) }

  if (runtime.type === 'command') {
    runtime.command = source.command ?? d.command ?? ''
    const workingDir = source.working_dir ?? d.work_dir
    const envFile = source.env_file ?? d.env_file
    const envVars = stripEmptyEnvKeys(source.env_vars ?? d.env)
    if (workingDir !== undefined) runtime.working_dir = workingDir
    if (envFile !== undefined) runtime.env_file = envFile
    if (envVars !== undefined) runtime.env_vars = envVars
  } else if (runtime.type === 'systemd') {
    const serviceName = source.service_name ?? serviceNameFromLogTarget(d.log_target)
    if (serviceName !== undefined) runtime.service_name = serviceName
    if (source.release_dir !== undefined) runtime.release_dir = source.release_dir
    if (source.current_dir !== undefined) runtime.current_dir = source.current_dir
    if (source.exec_start !== undefined) runtime.exec_start = source.exec_start
  } else if (runtime.type === 'launchd') {
    if (source.label !== undefined) runtime.label = source.label
    if (source.plist_path !== undefined) runtime.plist_path = source.plist_path
  } else if (runtime.type === 'docker') {
    const container = source.container ?? d.log_target
    if (container !== undefined) runtime.container = container
  } else if (runtime.type === 'nginx_static') {
    if (source.domain !== undefined) runtime.domain = source.domain
    if (source.release_dir !== undefined) runtime.release_dir = source.release_dir
    if (source.current_dir !== undefined) runtime.current_dir = source.current_dir
  } else if (runtime.type === 'external') {
    if (d.logs?.type === 'journalctl' || d.log_type === 'journalctl' || d.log_target) {
      runtime.type = 'systemd'
      const serviceName = source.service_name ?? serviceNameFromLogTarget(d.logs?.target ?? d.log_target)
      if (serviceName !== undefined) runtime.service_name = serviceName
    }
  }

  return runtime
}

function defaultLogKind(runtime: RuntimeConfig, location: Deployment['location']): LogKind {
  if (location === 'local' && runtime.type === 'command') return 'process'
  if (runtime.type === 'docker') return 'docker'
  if (runtime.type === 'nginx_static') return 'nginx'
  if (runtime.type === 'systemd') return 'journalctl'
  if (runtime.type === 'launchd') return 'macos_log'
  return 'process'
}

function defaultLogTarget(runtime: RuntimeConfig): string | undefined {
  if (runtime.type === 'systemd') {
    if (!runtime.service_name) return undefined
    return runtime.service_name.endsWith('.service') ? runtime.service_name : `${runtime.service_name}.service`
  }
  if (runtime.type === 'launchd') return runtime.label
  if (runtime.type === 'docker') return runtime.container
  if (runtime.type === 'nginx_static') return runtime.domain
  return undefined
}

function normalizeLogs(d: Deployment, runtime: RuntimeConfig): LogConfig {
  const source = d.logs ?? ({} as LogConfig)
  const kind = source.type ?? (d.log_type as LogKind | undefined) ?? defaultLogKind(runtime, d.location)
  const logs: LogConfig = { type: kind }
  const target = source.target ?? d.log_target ?? (
    kind === 'journalctl' || kind === 'macos_log' || kind === 'docker' || kind === 'nginx'
      ? defaultLogTarget(runtime)
      : undefined
  )
  const path = source.path
  const command = source.command
  const extraArgs = source.extra_args ?? d.extra_args
  if (target !== undefined) logs.target = target
  if (path !== undefined) logs.path = path
  if (command !== undefined) logs.command = command
  if (extraArgs !== undefined) logs.extra_args = extraArgs
  return logs
}

function normalizeDeployment(d: Deployment): Deployment {
  const dep = clone(d)
  const runtime = normalizeRuntime(dep)
  return {
    ...dep,
    control_mode: inferControlMode(dep),
    runtime,
    logs: normalizeLogs(dep, runtime),
  }
}

function logKindToLegacy(kind?: LogKind): LogSourceType | undefined {
  if (kind === 'journalctl' || kind === 'docker' || kind === 'file_tail' || kind === 'command') return kind
  return undefined
}

/**
 * projectToDraft 把 Project 深拷贝成草稿，编辑草稿不影响原对象。
 *
 * 参数：
 *   - p: 原始 Project 对象
 *
 * 返回：
 *   - 可安全编辑的 ConfigDraft 草稿
 */
export function projectToDraft(p: Project): ConfigDraft {
  return {
    variables: { ...(p.variables ?? {}) },
    environments: (p.environments ?? []).map(e => ({ ...e })),
    services: (p.services ?? []).map(s => ({
      id: s.id,
      name: s.name,
      required: s.required,
      order: s.order,
      deployments: (s.deployments ?? []).map(d => normalizeDeployment(d)),
    })),
    pipelines: (p.pipelines ?? []).map(pipeline => clone(pipeline)),
  }
}

/**
 * stripEmptyEnvKeys 过滤掉 key 为空字符串（含纯空白）的 env 变量。
 *
 * 参数：
 *   - env: 原始环境变量 map，可能含空 key
 *
 * 返回：
 *   - 过滤后的 env map，若结果为空则返回 undefined
 */
function stripEmptyEnvKeys(env?: Record<string, string>): Record<string, string> | undefined {
  if (!env) return undefined
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(env)) {
    if (k.trim() !== '') out[k] = v
  }
  return Object.keys(out).length ? out : undefined
}

/**
 * draftToPayload 把草稿拍平为后端 SetupPayload。
 *
 * 参数：
 *   - draft: 编辑完成的 ConfigDraft 草稿
 *
 * 返回：
 *   - 符合后端接口的 SetupPayload
 *
 * 注意：
 *   - env 变量中 key 为空字符串的条目会被自动过滤
 */
export function draftToPayload(draft: ConfigDraft): SetupPayload {
  return {
    variables: stripEmptyEnvKeys(draft.variables),
    environments: draft.environments.map(e => ({
      id: e.id || undefined,
      name: e.name,
      is_dev: e.is_dev,
      order: e.order,
    })),
    services: draft.services.map(s => ({
      id: s.id,
      name: s.name,
      required: s.required,
      order: s.order,
      deployments: s.deployments.map<SetupDeployment>((d) => {
        const dep = normalizeDeployment(d)
        const runtime = dep.runtime!
        const logs = dep.logs
        const controlMode = dep.control_mode ?? 'managed'
        return {
          id: dep.id || undefined,
          env_name: dep.env_name,
          location: dep.location,
          control_mode: controlMode,
          command: runtime.type === 'command' ? runtime.command : dep.command,
          work_dir: runtime.type === 'command' ? runtime.working_dir : dep.work_dir,
          env: runtime.type === 'command' ? stripEmptyEnvKeys(runtime.env_vars) : stripEmptyEnvKeys(dep.env),
          host_ids: dep.host_ids,
          log_type: logKindToLegacy(logs?.type),
          log_target: logs?.target,
          extra_args: logs?.extra_args,
          env_file: runtime.type === 'command' ? runtime.env_file : dep.env_file,
          runtime,
          logs,
          read_only: controlMode === 'monitor' ? true : undefined,
          start_command: dep.start_command,
          stop_command: dep.stop_command,
          pipeline: dep.pipeline,
        }
      }),
    })),
    pipelines: draft.pipelines,
  }
}

function pipelineSteps(pipeline?: Pipeline): PipelineStep[] {
  if (!pipeline) return []
  return [
    ...(pipeline.build ?? []),
    ...(pipeline.deploy ?? []),
    ...(pipeline.finally ?? []),
  ]
}

/**
 * validateDraft 保存前校验，返回错误信息数组（空数组 = 通过）。
 *
 * 参数：
 *   - draft: 待校验的 ConfigDraft 草稿
 *
 * 返回：
 *   - 错误信息数组，若为空数组表示校验通过
 *
 * 注意：
 *   - local deployment：runtime.command / command 为空时报错
 *   - remote deployment：host_ids 为空时报错
 *   - 项目级 pipeline 步骤：名称和插件类型为空时报错
 */
export function validateDraft(draft: ConfigDraft): string[] {
  const errors: string[] = []

  // 校验环境配置
  const envNames = new Set<string>()
  for (const e of draft.environments) {
    if (e.name.trim() === '') {
      errors.push('环境名称不能为空')
    } else if (envNames.has(e.name)) {
      errors.push(`环境名称重复：${e.name}`)
    } else {
      envNames.add(e.name)
    }
  }

  // 校验服务配置
  const svcNames = new Set<string>()
  for (const s of draft.services) {
    if (s.name.trim() === '') {
      errors.push('服务名称不能为空')
    } else if (svcNames.has(s.name)) {
      errors.push(`服务名重复：${s.name}`)
    } else {
      svcNames.add(s.name)
    }

    // 校验每个部署配置
    for (const d of s.deployments) {
      const dep = normalizeDeployment(d)
      const command = (dep.runtime?.command ?? '').trim()
      if (dep.control_mode === 'managed' && dep.runtime?.type === 'command' && command === '') {
        // command 接管需要明确启动命令；流水线已提升到项目级，不再作为命令替代品。
        errors.push(`服务「${s.name}」在「${d.env_name}」环境的本地命令不能为空`)
      }
      if (dep.control_mode === 'managed' && dep.runtime?.type === 'launchd' && (dep.runtime.label ?? '').trim() === '') {
        // launchd 接管依赖稳定 label，不能退化为自定义命令或隐式猜测。
        errors.push(`服务「${s.name}」在「${d.env_name}」环境的 Launchd Label 不能为空`)
      }
      if (d.location === 'remote' && (d.host_ids ?? []).length === 0) {
        // remote 运行配置必须明确主机；项目级流水线通过服务环境配置再解析目标。
        errors.push(`服务「${s.name}」在「${d.env_name}」环境未选择主机`)
      }
      const logs = dep.logs
      if (logs?.type === 'file_tail' && (logs.path ?? '').trim() === '') {
        errors.push(`服务「${s.name}」在「${d.env_name}」环境的日志文件路径不能为空`)
      }
      if (logs?.type === 'command' && (logs.command ?? '').trim() === '') {
        errors.push(`服务「${s.name}」在「${d.env_name}」环境的日志命令不能为空`)
      }
      if ((logs?.type === 'journalctl' || logs?.type === 'macos_log' || logs?.type === 'docker' || logs?.type === 'nginx') && (logs.target ?? '').trim() === '') {
        if (logs?.type === 'macos_log') {
          errors.push(`服务「${s.name}」在「${d.env_name}」环境的 macOS 日志目标不能为空`)
          continue
        }
        errors.push(`服务「${s.name}」在「${d.env_name}」环境的日志目标不能为空`)
      }
    }
  }

  for (const projectPipeline of draft.pipelines) {
    for (const step of pipelineSteps(projectPipeline.pipeline)) {
      if ((step.name ?? '').trim() === '') {
        errors.push(`项目流水线「${projectPipeline.name || projectPipeline.id || '未命名'}」步骤名称不能为空`)
      }
      if ((step.type ?? '').trim() === '') {
        errors.push(`项目流水线「${projectPipeline.name || projectPipeline.id || '未命名'}」步骤「${step.name || '未命名'}」插件类型不能为空`)
      }
    }
  }

  return errors
}
