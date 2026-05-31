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
import type { Project, Deployment, Environment, SetupPayload, SetupDeployment } from '@/api/agent'

export interface ConfigDraftService {
  id: string
  name: string
  required: boolean
  order: number
  deployments: Deployment[]
}

export interface ConfigDraft {
  environments: Environment[]
  services: ConfigDraftService[]
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
    environments: (p.environments ?? []).map(e => ({ ...e })),
    services: (p.services ?? []).map(s => ({
      id: s.id,
      name: s.name,
      required: s.required,
      order: s.order,
      deployments: (s.deployments ?? []).map(d => JSON.parse(JSON.stringify(d))),
    })),
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
        read_only: d.read_only,
        start_command: d.start_command,
        stop_command: d.stop_command,
        pipeline: d.pipeline,
      })),
    })),
  }
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
 *   - local deployment：command 为空且无 pipeline 时报错
 *   - remote deployment：host_ids 为空时报错
 *   - pipeline 步骤：run 类型命令为空、sync 类型路径为空时报错
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
      if (d.location === 'local' && (d.command ?? '').trim() === '' && !d.pipeline) {
        // local 部署：必须有命令或 pipeline，两者都没有则报错
        errors.push(`服务「${s.name}」在「${d.env_name}」环境的本地命令不能为空`)
      }
      if (d.location === 'remote' && (d.host_ids ?? []).length === 0) {
        // remote 部署：必须选择至少一台主机
        errors.push(`服务「${s.name}」在「${d.env_name}」环境未选择主机`)
      }
      if (d.pipeline) {
        for (const step of d.pipeline.steps) {
          if (step.action === 'run' && (step.command ?? '').trim() === '') {
            errors.push(`服务「${s.name}」流水线步骤「${step.name || step.id}」命令不能为空`)
          }
          if (
            step.action === 'sync' &&
            ((step.sync_from ?? '').trim() === '' || (step.sync_to ?? '').trim() === '')
          ) {
            errors.push(`服务「${s.name}」流水线同步步骤「${step.name || step.id}」路径不能为空`)
          }
        }
      }
    }
  }

  return errors
}
