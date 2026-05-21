/**
 * SettingsPage 测试桌面端设置页。
 *
 * 职责：
 *   - 验证通用设置展示和保存
 *   - 验证项目服务启动选择和显示隐藏操作
 *
 * 边界：
 *   - 不测试真实系统登录项
 *   - 不打开真实目录选择器
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import SettingsPage from '../SettingsPage.vue'
import { useAgentStore } from '@/stores/agent'
import { useSettingsStore } from '@/stores/settings'
import type { Project, Service } from '@/api/agent'

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(),
  message: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
}))

function service(id: string, name: string, required = false): Service {
  return {
    id,
    project_id: 'proj-1',
    name,
    status: '',
    command: 'pnpm dev',
    work_dir: '/tmp/project',
    required,
    order: 1,
  }
}

function project(services: Service[]): Project {
  return {
    id: 'proj-1',
    name: 'Project',
    root_path: '/tmp/project',
    services,
    selected_service_ids: ['worker'],
  }
}

describe('SettingsPage', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('通用页展示日志保留天数并保存', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = { log_retention_days: 7 }
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)
    vi.spyOn(settings, 'saveLogRetentionDays').mockResolvedValue(undefined)

    const wrapper = mount(SettingsPage)
    await nextTick()
    const input = wrapper.find('[data-test="retention-days"]')
    await input.setValue(14)
    await input.trigger('change')

    expect(settings.saveLogRetentionDays).toHaveBeenCalledWith(14)
  })

  it('项目页可切换服务隐藏状态和启动选择', async () => {
    const api = service('svc-api', 'api', true)
    const worker = service('svc-worker', 'worker')
    const agent = useAgentStore()
    agent.projects = [project([api, worker])]
    vi.spyOn(agent, 'updateSelected').mockResolvedValue(undefined)
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mount(SettingsPage)
    await wrapper.find('[data-test="settings-tab-projects"]').trigger('click')
    await wrapper.find('[data-test="toggle-hidden-svc-worker"]').trigger('click')
    await wrapper.find('[data-test="select-start-svc-worker"]').setValue(false)

    expect(settings.isServiceHidden('svc-worker')).toBe(true)
    expect(agent.updateSelected).toHaveBeenCalledWith('proj-1', ['api'])
  })
})
