/**
 * Dynamic Connector onboarding page tests.
 *
 * Responsibility: verify data-driven rendering, MCP progression truth, and manual boundaries.
 * Boundary: no real Tauri command, native window, or user configuration is used.
 */
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OnboardingPage from '../OnboardingPage.vue'
import { useOnboardingStore } from '@/stores/onboarding'
import { useSettingsStore } from '@/stores/settings'
import { installTestI18n } from '@/test-utils/i18n'
import type { AgentConnectorSummary, ConnectorOperationOutcome } from '@/api/mcpInstall'

const push = vi.fn()
const windowApiMock = vi.hoisted(() => ({ startDragging: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push }) }))
vi.mock('@tauri-apps/api/window', () => ({ getCurrentWindow: () => windowApiMock }))

function summary(id: string, detected: boolean, builtIn = true): AgentConnectorSummary {
  return {
    descriptor: {
      id,
      display_name: id === 'fixture-json-agent' ? 'Fixture JSON Agent' : id,
      built_in: builtIn,
      platforms: ['macos'],
      support_level: id === 'fixture-json-agent' ? 'standard' : 'full',
      integrations: [
        { capability: 'mcp', support: 'automatic' },
        { capability: 'skill', support: id === 'fixture-json-agent' ? 'unsupported' : 'automatic' },
        { capability: 'session_hook', support: id === 'fixture-json-agent' ? 'unsupported' : 'automatic' },
      ],
      operations: [{ operation: 'install', support: 'automatic' }],
    },
    state: {
      detected,
      detection_path: detected ? `/bin/${id}` : null,
      integrations: [],
      requires_restart: false,
    },
  }
}

function outcome(mcpResult: 'installed' | 'already_present' | 'failed'): ConnectorOperationOutcome {
  return {
    connector_id: 'fixture-json-agent',
    operation: 'install',
    result: mcpResult === 'failed' ? 'failed' : 'partial',
    integrations: [
      { capability: 'mcp', result: mcpResult },
      { capability: 'skill', result: mcpResult === 'failed' ? 'installed' : 'failed', message: 'Skill unavailable' },
      { capability: 'session_hook', result: 'unsupported' },
    ],
    requires_restart: false,
  }
}

function mountPage(summaries: AgentConnectorSummary[]) {
  const store = useOnboardingStore()
  store.connectors = summaries
  store.selectedAgents = summaries.filter(item => item.state.detected).map(item => item.descriptor.id)
  vi.spyOn(store, 'detectInstalledAgents').mockResolvedValue(undefined)
  return { store, wrapper: mount(OnboardingPage, { global: { plugins: [installTestI18n('zh-CN')] } }) }
}

describe('OnboardingPage', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    localStorage.clear()
    window.history.replaceState({}, '', '/')
    push.mockReset()
    windowApiMock.startDragging.mockReset()
    windowApiMock.startDragging.mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
  })

  it('renders detected connectors first and reveals undetected connectors on demand', async () => {
    const { wrapper } = mountPage([
      summary('fixture-json-agent', true, false),
      summary('not-installed-agent', false),
    ])

    expect(wrapper.text()).toContain('Fixture JSON Agent')
    expect(wrapper.text()).not.toContain('not-installed-agent')
    expect(wrapper.find('[data-test="agent-fixture-json-agent"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="browse-all-connectors"]').attributes('aria-expanded')).toBe('false')

    await wrapper.find('[data-test="browse-all-connectors"]').trigger('click')

    expect(wrapper.text()).toContain('not-installed-agent')
    expect(wrapper.find('[data-test="agent-not-installed-agent"]').attributes('disabled')).toBeDefined()
  })

  it('renders capability support from an unknown descriptor without id-specific branches', () => {
    const { wrapper } = mountPage([summary('fixture-json-agent', true, false)])

    expect(wrapper.text()).toContain('mcp · automatic')
    expect(wrapper.text()).toContain('skill · unsupported')
    expect(wrapper.find('[data-test="manual-agent-entry"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="cloud-agent-limit"]').text()).toContain('Remote MCP Gateway')
  })

  it('allows progression when MCP works even if an enhancement fails', async () => {
    const { store, wrapper } = mountPage([summary('fixture-json-agent', true, false)])
    store.installOutcomes = [outcome('installed')]
    await nextTick()

    expect(wrapper.find('[data-test="onboarding-progress-test"]').classes()).toContain('active')
    expect(wrapper.find('[data-test="install-success"]').text()).toContain('partial')
    expect(wrapper.find('[data-test="install-success"]').classes()).toContain('warning')
    expect(wrapper.find('[data-test="install-success"]').classes()).not.toContain('success')

    vi.spyOn(useSettingsStore(), 'setOnboardingCompleted').mockResolvedValue(undefined)
    await wrapper.find('[data-test="finish-onboarding"]').trigger('click')
    await flushPromises()
    expect(push).toHaveBeenCalledWith('/')
  })

  it('blocks completion when only enhancements succeed and MCP fails', async () => {
    const { store, wrapper } = mountPage([summary('fixture-json-agent', true, false)])
    store.installOutcomes = [outcome('failed')]
    await nextTick()

    await wrapper.find('[data-test="finish-onboarding"]').trigger('click')

    expect(push).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="finish-feedback"]').text()).toContain('请先安装 MCP 连接')
  })

  it('delegates the selected dynamic IDs to the store install action', async () => {
    const { store, wrapper } = mountPage([summary('fixture-json-agent', true, false)])
    const install = vi.spyOn(store, 'installSelectedMcp').mockResolvedValue(undefined)

    await wrapper.find('[data-test="install-mcp"]').trigger('click')

    expect(install).toHaveBeenCalledTimes(1)
    expect(store.selectedAgents).toEqual(['fixture-json-agent'])
  })

  it('keeps controls locked while a batch is running', async () => {
    const { store, wrapper } = mountPage([summary('fixture-json-agent', true, false)])
    store.installing = true
    await nextTick()

    expect(wrapper.find('[data-test="install-mcp"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="skip-onboarding"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="manual-agent-entry"]').attributes('disabled')).toBeDefined()
  })

  it('copies the verification prompt', async () => {
    const { store, wrapper } = mountPage([summary('fixture-json-agent', true, false)])

    await wrapper.find('[data-test="copy-prompt"]').trigger('click')
    await flushPromises()

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(store.demoPrompt)
    expect(wrapper.find('[data-test="copy-feedback"]').text()).toContain('已复制')
  })

  it('starts native dragging only from non-interactive chrome', async () => {
    const { wrapper } = mountPage([summary('fixture-json-agent', true, false)])

    await wrapper.find('[data-test="onboarding-header"]').trigger('mousedown', { buttons: 1 })
    await wrapper.find('[data-test="onboarding-locale-select"]').trigger('mousedown', { buttons: 1 })

    expect(windowApiMock.startDragging).toHaveBeenCalledTimes(1)
  })
})
