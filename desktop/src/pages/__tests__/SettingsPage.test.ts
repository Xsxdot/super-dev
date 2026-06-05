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
import { open } from '@tauri-apps/plugin-dialog'
import SettingsPage from '../SettingsPage.vue'
import { useAgentStore } from '@/stores/agent'
import { usePipelineTemplateStore } from '@/stores/pipelineTemplate'
import { useSettingsStore } from '@/stores/settings'
import { installTestI18n } from '@/test-utils/i18n'
import type { Project, Service } from '@/api/agent'

const routeState = vi.hoisted(() => ({ query: {} as Record<string, string> }))

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(),
  message: vi.fn(),
  ask: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => ({ query: routeState.query }),
}))

vi.mock('@/components/Settings/HostManagerTab.vue', () => ({
  default: { template: '<section>主机管理</section>' },
}))

vi.mock('@/components/Settings/DNSProviderTab.vue', () => ({
  default: { template: '<section data-test="dns-provider-tab">DNS 提供商</section>' },
}))

vi.mock('@/components/Settings/CertificateTab.vue', () => ({
  default: { template: '<section data-test="certificate-tab">SSL 证书</section>' },
}))

function service(id: string, name: string, required = false): Service {
  return {
    id,
    project_id: 'proj-1',
    name,
    status: '',
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
    env_selected_service_ids: { dev: ['worker'] },
    environments: [{ id: 'e-dev', name: 'dev', is_dev: true, order: 0 }],
  }
}

function mountSettingsPage(locale: 'zh-CN' | 'en-US' = 'zh-CN') {
  return mount(SettingsPage, {
    global: { plugins: [installTestI18n(locale)] },
  })
}

describe('SettingsPage', () => {
  beforeEach(() => {
    localStorage.clear()
    routeState.query = {}
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('为 overlay 标题栏预留侧栏顶部安全区', () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage()
    const sidebar = wrapper.find('[data-test="settings-sidebar"]').element
    const spacer = wrapper.find('[data-test="settings-titlebar-spacer"]').element
    const back = wrapper.find('[data-test="settings-back"]').element

    expect(spacer.parentElement).toBe(sidebar)
    expect(back.parentElement).toBe(sidebar)
    expect(spacer.compareDocumentPosition(back) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(wrapper.find('[data-test="settings-titlebar-spacer"]').attributes('data-tauri-drag-region')).toBeDefined()
  })

  it('通用页展示日志保留天数并保存', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = { log_retention_days: 7, sample_seeded: false, onboarding_completed: false }
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)
    vi.spyOn(settings, 'saveLogRetentionDays').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage()
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
    vi.spyOn(agent, 'putEnvSelected').mockResolvedValue(undefined)
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage()
    await wrapper.find('[data-test="settings-tab-projects"]').trigger('click')
    await wrapper.find('[data-test="toggle-hidden-svc-worker"]').trigger('click')
    await wrapper.find('[data-test="select-start-svc-worker"]').setValue(false)

    expect(settings.isServiceHidden('svc-worker')).toBe(true)
    // 取消勾选 worker → dev 环境的已选列表从 ['worker'] 变为 []
    expect(agent.putEnvSelected).toHaveBeenCalledWith('proj-1', 'dev', [])
  })

  it('项目页可从编辑配置旁打开流水线编辑器', async () => {
    const agent = useAgentStore()
    agent.projects = [project([service('svc-web', 'web')])]
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage()
    await wrapper.find('[data-test="settings-tab-projects"]').trigger('click')

    expect(wrapper.find('[data-test="setup-project-proj-1"]').exists()).toBe(true)
    const button = wrapper.find('[data-test="pipeline-project-proj-1"]')
    expect(button.exists()).toBe(true)

    await button.trigger('click')
    expect(wrapper.text()).toContain('编辑流水线 · Project')
  })

  it('支持从 query 直达主机管理 tab', async () => {
    routeState.query = { tab: 'hosts' }
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage()
    await nextTick()

    expect(wrapper.find('[data-test="settings-tab-hosts"]').classes()).toContain('active')
    expect(wrapper.text()).toContain('主机管理')
  })

  it('设置页只在 DNS Provider tab 管理全局 DNS 提供商', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage()
    await wrapper.find('[data-test="settings-tab-dns"]').trigger('click')

    expect(wrapper.find('[data-test="dns-provider-tab"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('DNS 提供商')
    expect(wrapper.find('[data-test="settings-tab-ingress"]').exists()).toBe(false)
  })

  it('兼容旧 ingress query 并打开 DNS Provider tab', async () => {
    routeState.query = { tab: 'ingress' }
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage()
    await nextTick()

    expect(wrapper.find('[data-test="settings-tab-dns"]').classes()).toContain('active')
    expect(wrapper.find('[data-test="dns-provider-tab"]').exists()).toBe(true)
  })

  it('支持打开 SSL 证书 tab', async () => {
    routeState.query = { tab: 'ssl' }
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage()
    await nextTick()

    expect(wrapper.find('[data-test="settings-tab-ssl"]').classes()).toContain('active')
    expect(wrapper.find('[data-test="certificate-tab"]').exists()).toBe(true)
  })

  it('支持打开模板管理 tab', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage()
    await wrapper.find('[data-test="settings-tab-templates"]').trigger('click')

    expect(wrapper.text()).toContain('模板')
  })

  it('支持打开操作审批 tab', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage()
    await wrapper.find('[data-test="settings-tab-approvals"]').trigger('click')

    expect(wrapper.find('[data-test="operation-approvals-tab"]').exists()).toBe(true)
  })

  it('导入模板时打开 YAML 文件选择器并刷新模板 store', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)
    const templateStore = usePipelineTemplateStore()
    vi.spyOn(templateStore, 'loadTemplates').mockResolvedValue(undefined)
    const importTemplate = vi.spyOn(templateStore, 'importTemplate').mockResolvedValue(undefined)
    vi.mocked(open).mockResolvedValue('/tmp/custom.yaml')

    const wrapper = mountSettingsPage()
    await wrapper.find('[data-test="settings-tab-templates"]').trigger('click')
    await wrapper.find('[data-test="template-import"]').trigger('click')

    expect(open).toHaveBeenCalledWith(expect.objectContaining({
      filters: [{ name: 'YAML', extensions: ['yaml', 'yml'] }],
    }))
    expect(importTemplate).toHaveBeenCalledWith('/tmp/custom.yaml')
  })

  it('查看模板时把完整 detail 传给内容弹窗', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)
    const templateStore = usePipelineTemplateStore()
    vi.spyOn(templateStore, 'loadTemplates').mockResolvedValue(undefined)
    templateStore.templates = [{
      source: 'builtin',
      id: 'systemd',
      name: 'Systemd',
      version: '1.0.0',
      digest: 'sha256:summary',
      description: 'Deploy via systemd',
    }]
    vi.spyOn(templateStore, 'loadTemplateDetail').mockResolvedValue({
      source: 'builtin',
      id: 'systemd',
      version: '1.0.0',
      digest: 'sha256:systemd',
      yaml: 'id: systemd\n',
      template: {
        id: 'systemd',
        name: 'Systemd',
        version: '1.0.0',
        inputs: { app_name: { label: '应用名', type: 'string' } },
        steps: [],
      },
    })

    const wrapper = mountSettingsPage()
    await wrapper.find('[data-test="settings-tab-templates"]').trigger('click')
    await wrapper.find('[data-test="template-view-systemd"]').trigger('click')
    await Promise.resolve()
    await nextTick()

    expect(wrapper.text()).toContain('sha256:systemd')
    expect(wrapper.text()).toContain('应用名')
  })

  it('通用页可切换到英文并立即更新界面文案', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = { log_retention_days: 7, sample_seeded: false, onboarding_completed: false }
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage('zh-CN')
    await nextTick()

    expect(wrapper.text()).toContain('通用')
    await wrapper.find('[data-test="locale-select"]').setValue('en-US')

    expect(settings.locale).toBe('en-US')
    expect(wrapper.text()).toContain('General')
    expect(wrapper.text()).toContain('Language')
    expect(wrapper.text()).toContain('Log retention days')
  })

  it('通用页可重新运行首次引导', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = { log_retention_days: 7, sample_seeded: true, onboarding_completed: true }
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'loadAutostart').mockResolvedValue(undefined)

    const wrapper = mountSettingsPage()
    await nextTick()
    expect(wrapper.text()).toContain('使用指南 skill')
    await wrapper.find('[data-test="rerun-onboarding"]').trigger('click')

    expect(wrapper.text()).toContain('首次引导')
  })
})
