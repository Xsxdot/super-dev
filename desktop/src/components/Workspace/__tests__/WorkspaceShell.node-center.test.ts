/**
 * WorkspaceShell Node Center render test.
 *
 * Responsibilities:
 *   - Verify the global nodes workspace tab renders NodeCenterView
 *
 * Boundaries:
 *   - NodeCenterView is stubbed
 *   - Does not render real node cards
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WorkspaceShell from '../WorkspaceShell.vue'
import { useWorkspaceStore } from '@/stores/workspace'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/components/NodeCenter/NodeCenterView.vue', () => ({
  default: { template: '<section data-test="node-center-view-stub" />' },
}))

vi.mock('@/components/Workspace/RuntimeWorkbenchHeader.vue', () => ({
  default: { template: '<header data-test="runtime-header-stub" />' },
}))

vi.mock('@/components/Panel/PanelLayout.vue', () => ({
  default: { template: '<section data-test="panel-layout-stub" />' },
}))

describe('WorkspaceShell node center tab', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('renders NodeCenterView for a nodes workspace tab', () => {
    useWorkspaceStore().openNodesTab()

    const wrapper = mount(WorkspaceShell, { global: { plugins: [installTestI18n('en-US')] } })

    expect(wrapper.find('[data-test="node-center-view-stub"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="runtime-header-stub"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="panel-layout-stub"]').exists()).toBe(false)
  })
})
