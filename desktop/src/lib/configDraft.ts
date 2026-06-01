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
import type { Project, Deployment, Environment, SetupPayload, SetupDeployment, ProjectPipeline, Pipeline, PipelineStep } from '@/api/agent'

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
      deployments: (s.deployments ?? []).map(d => JSON.parse(JSON.stringify(d))),
    })),
    pipelines: (p.pipelines ?? []).map(pipeline => JSON.parse(JSON.stringify(pipeline))),
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
      deployments: s.deployments.map<SetupDeployment>(d => ({
        id: d.id || undefined,
        env_name: d.env_name,
        location: d.location,
        command: d.command,
        work_dir: d.work_dir,
        env: stripEmptyEnvKeys(d.env),
        host_ids: d.host_ids,
        log_type: d.log_type,
        log_target: d.log_target,
        extra_args: d.extra_args,
        env_file: d.env_file,
        runtime: d.runtime,
        logs: d.logs,
        read_only: d.read_only,
        start_command: d.start_command,
        stop_command: d.stop_command,
        pipeline: d.pipeline,
      })),
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
      const command = (d.runtime?.command ?? d.command ?? '').trim()
      if (d.location === 'local' && command === '') {
        // local 部署：必须有运行命令；流水线已提升到项目级，不再作为本地命令替代品。
        errors.push(`服务「${s.name}」在「${d.env_name}」环境的本地命令不能为空`)
      }
      if (d.location === 'remote' && (d.host_ids ?? []).length === 0) {
        // remote 运行配置必须明确主机；项目级流水线通过服务环境配置再解析目标。
        errors.push(`服务「${s.name}」在「${d.env_name}」环境未选择主机`)
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
