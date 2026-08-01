/**
 * ConfigMigrationDialog tests.
 *
 * 职责：
 *   - 验证疑似密钥逐条渲染且默认处置为本机层（「不挡、只亮」的安全默认）
 *   - 验证执行迁移时以用户实际选择的 disposition 组装 decisions 数组调用 applyConfigMigration
 *   - 验证后端返回 not_needed 时直接渲染已完成态，而不是当作错误处理
 *
 * 边界：
 *   - 不访问真实后端，getConfigMigrationPreview/applyConfigMigration 均为 mock
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import ConfigMigrationDialog from '@/components/Settings/ConfigMigrationDialog.vue'
import { installTestI18n } from '@/test-utils/i18n'
import type { MigrationPlan, Project } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    getConfigMigrationPreview: vi.fn(),
    applyConfigMigration: vi.fn(),
  }
})

function plan(overrides: Partial<MigrationPlan> = {}): MigrationPlan {
  return {
    root_path: '/tmp/demo',
    suspects: [],
    ui_state_envs: [],
    gitignore: { remove_lines: [], add_lines: [] },
    service_count: 1,
    relativized_paths: [],
    ...overrides,
  }
}

function twoSuspects() {
  return [
    { scope: 'variables' as const, key: 'API_TOKEN', masked_value: 'sk-1********', reason: '键名疑似密钥' },
    { scope: 'env_vars' as const, service: 'web', env: 'dev', key: 'DB_PASSWORD', masked_value: 'post********', reason: '键名疑似密钥' },
  ]
}

function project(): Project {
  return { id: 'p1', name: 'demo', root_path: '/tmp/demo', config_format: 'split', services: [] }
}

function mountDialog() {
  return mount(ConfigMigrationDialog, {
    props: { projectId: 'p1' },
    global: { plugins: [installTestI18n()] },
  })
}

describe('ConfigMigrationDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('渲染每条疑似密钥且默认处置为本机层', async () => {
    const { getConfigMigrationPreview } = await import('@/api/agent')
    vi.mocked(getConfigMigrationPreview).mockResolvedValue(plan({ service_count: 2, suspects: twoSuspects() }))
    const wrapper = mountDialog()

    await vi.waitFor(() => expect(wrapper.findAll('[data-test^="suspect-row-"]')).toHaveLength(2))

    expect((wrapper.find('[data-test="suspect-local-0"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.find('[data-test="suspect-shared-0"]').element as HTMLInputElement).checked).toBe(false)
    expect((wrapper.find('[data-test="suspect-local-1"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.find('[data-test="suspect-shared-1"]').element as HTMLInputElement).checked).toBe(false)
  })

  it('执行拆分时以用户选择的 disposition 调用 applyConfigMigration', async () => {
    const { getConfigMigrationPreview, applyConfigMigration } = await import('@/api/agent')
    vi.mocked(getConfigMigrationPreview).mockResolvedValue(plan({ suspects: twoSuspects() }))
    vi.mocked(applyConfigMigration).mockResolvedValue(project())
    const wrapper = mountDialog()
    await vi.waitFor(() => expect(wrapper.findAll('[data-test^="suspect-row-"]')).toHaveLength(2))

    // 把第一条（项目变量 API_TOKEN）显式选去共享层，第二条保留默认的本机层。
    await wrapper.find('[data-test="suspect-shared-0"]').setValue()
    await wrapper.find('[data-test="migration-apply"]').trigger('click')

    await vi.waitFor(() => expect(applyConfigMigration).toHaveBeenCalled())
    expect(applyConfigMigration).toHaveBeenCalledWith('p1', [
      { scope: 'variables', service: undefined, env: undefined, key: 'API_TOKEN', disposition: 'shared' },
      { scope: 'env_vars', service: 'web', env: 'dev', key: 'DB_PASSWORD', disposition: 'local' },
    ])
    expect(wrapper.emitted('migrated')?.[0]).toEqual([project()])
    await vi.waitFor(() => expect(wrapper.find('[data-test="migration-done"]').exists()).toBe(true))
  })

  it('status 为 not_needed 时直接展示已完成态', async () => {
    const { getConfigMigrationPreview } = await import('@/api/agent')
    vi.mocked(getConfigMigrationPreview).mockResolvedValue({ status: 'not_needed' })
    const wrapper = mountDialog()

    await vi.waitFor(() => expect(wrapper.find('[data-test="migration-done"]').exists()).toBe(true))
    expect(wrapper.find('[data-test="migration-suspects"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="migration-apply"]').exists()).toBe(false)
  })
})
