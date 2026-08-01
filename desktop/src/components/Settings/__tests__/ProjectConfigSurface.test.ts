/**
 * ProjectConfigSurface — 配置迁移横幅与弹窗集成测试。
 *
 * 职责：
 *   - 验证 legacy 项目渲染迁移横幅，点击后打开真实 ConfigMigrationDialog
 *   - 验证 apply 成功后弹窗的「拆分完成」态在 DOM 中真实可见——这是 Task 10
 *     review 发现的回归点：父组件的 @migrated 处理器曾在 handleMigrated
 *     的第一条语句就同步把 showMigrationDialog 置为 false，与子组件把
 *     phase 切到 'done' 落在同一个同步栈里，v-if 卸载抢在 'done' 渲染
 *     到达 DOM 之前发生，用户完全看不到成功态列出的产物清单
 *   - 验证用户主动点击弹窗「关闭」按钮后弹窗才消失（而不是 apply 后自动关闭）
 *
 * 边界：
 *   - 不访问真实后端；api.listHosts/listProjects、
 *     getConfigMigrationPreview、applyConfigMigration 均为 mock
 *   - 本文件用真实的 ProjectConfigSurface + ConfigMigrationDialog 父子组合
 *     覆盖 @migrated 事件的真实接线；ConfigMigrationDialog.test.ts 单独挂载
 *     子组件的用例不会触发这条时序问题，两者互补而非重复
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ProjectConfigSurface from '@/components/Settings/ProjectConfigSurface.vue'
import { installTestI18n } from '@/test-utils/i18n'
import type { MigrationPlan, Project } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn().mockResolvedValue([]),
      listProjects: vi.fn(),
    },
    getConfigMigrationPreview: vi.fn(),
    applyConfigMigration: vi.fn(),
  }
})

function legacyProject(): Project {
  return {
    id: 'p1', name: 'demo', root_path: '/tmp/demo', config_format: 'legacy',
    env_selected_service_ids: {},
    environments: [{ id: 'e1', name: 'dev', is_dev: true, order: 0 }],
    debug_credentials: [],
    services: [{ id: 's1', project_id: 'p1', name: 'web', status: '', required: false, order: 0, deployments: [], debug_credentials: [] }],
  }
}

function splitProject(): Project {
  return { ...legacyProject(), config_format: 'split' }
}

function emptyPlan(): MigrationPlan {
  return {
    root_path: '/tmp/demo',
    suspects: [],
    ui_state_envs: [],
    gitignore: { remove_lines: [], add_lines: [] },
    service_count: 1,
    relativized_paths: [],
  }
}

function mountSurface(project: Project) {
  return mount(ProjectConfigSurface, {
    props: { project },
    global: { plugins: [installTestI18n()] },
  })
}

describe('ProjectConfigSurface 配置迁移集成', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('legacy 项目展示迁移横幅；apply 成功后弹窗保持「拆分完成」态直到用户点击关闭', async () => {
    const { api, getConfigMigrationPreview, applyConfigMigration } = await import('@/api/agent')
    vi.mocked(api.listProjects).mockResolvedValue([splitProject()])
    vi.mocked(getConfigMigrationPreview).mockResolvedValue(emptyPlan())
    vi.mocked(applyConfigMigration).mockResolvedValue(splitProject())

    const wrapper = mountSurface(legacyProject())
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="config-migration-banner"]').exists()).toBe(true)

    await wrapper.find('[data-test="config-migration-open"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-test="migration-apply"]').exists()).toBe(true))

    await wrapper.find('[data-test="migration-apply"]').trigger('click')

    // 关键回归断言：apply 成功后「拆分完成」态必须真的落到 DOM 里，不能被
    // 父组件的 @migrated 处理器同步关闭弹窗抢跑卸载掉。
    await vi.waitFor(() => expect(wrapper.find('[data-test="migration-done"]').exists()).toBe(true))
    expect(applyConfigMigration).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="config-migration-dialog"]').exists()).toBe(true)

    // 弹窗仍带着成功态一起留在 DOM 里，直到用户自己点关闭。
    await wrapper.find('[data-test="migration-close"]').trigger('click')
    expect(wrapper.find('[data-test="config-migration-dialog"]').exists()).toBe(false)
  })
})

describe('ProjectConfigSurface 只读告警横幅', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  // 队友迁移后提交了 project.yaml，本机 pull 下来时旁边还压着自己那份
  // gitignore 的 config.yaml。split 胜出，本机的路径与密钥被整份忽略，而
  // config_format 报的是 'split'，迁移横幅不触发——用户拿到的现象只有
  // 「服务起不来」。这条横幅是唯一能让它可诊断的东西。
  it('config_stale_legacy 为真时提示并存的 config.yaml 已不生效', async () => {
    const { getConfigMigrationPreview } = await import('@/api/agent')
    vi.mocked(getConfigMigrationPreview).mockResolvedValue(emptyPlan())

    const wrapper = mountSurface({ ...splitProject(), config_stale_legacy: true })
    await new Promise(r => setTimeout(r))

    expect(wrapper.find('[data-test="config-stale-legacy-banner"]').exists()).toBe(true)
    // 迁移横幅只认 legacy 格式，不该被这条状态误触发。
    expect(wrapper.find('[data-test="config-migration-banner"]').exists()).toBe(false)
  })

  it('干净的 split 项目不出现任何告警横幅', async () => {
    const wrapper = mountSurface(splitProject())
    await new Promise(r => setTimeout(r))
    expect(wrapper.find('[data-test="config-stale-legacy-banner"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="shared-secret-warning-banner"]').exists()).toBe(false)
  })

  // 「不挡、只亮」：共享层里的疑似密钥必须看得见（脱敏），但保存按钮照常可用。
  it('shared_secret_warnings 逐条渲染脱敏值，且不禁用保存', async () => {
    const wrapper = mountSurface({
      ...splitProject(),
      shared_secret_warnings: [
        { scope: 'variables', key: 'OPENAI_KEY', masked_value: 'sk-l********', reason: '键名疑似密钥' },
      ],
    })
    await new Promise(r => setTimeout(r))

    const banner = wrapper.find('[data-test="shared-secret-warning-banner"]')
    expect(banner.exists()).toBe(true)
    expect(banner.text()).toContain('OPENAI_KEY')
    expect(banner.text()).toContain('sk-l********')
    expect((wrapper.find('[data-test="config-save"]').element as HTMLButtonElement).disabled).toBe(false)
  })
})
