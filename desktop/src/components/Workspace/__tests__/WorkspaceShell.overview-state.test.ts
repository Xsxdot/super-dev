/**
 * WorkspaceShell overview state regression tests.
 *
 * Responsibility:
 *   - Verify project overview tabs keep their local UI state when users switch to log tabs.
 *
 * Boundary:
 *   - Uses light child stubs for heavyweight runtime/log/config internals.
 *   - Does not verify ProjectConfigSurface validation or save behavior.
 */
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, ref, watch } from 'vue'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WorkspaceShell from '../WorkspaceShell.vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/components/Overview/RuntimeStatusTab.vue', () => ({
  default: { template: '<section data-test="runtime-tab-stub">runtime</section>' },
}))

vi.mock('@/components/Overview/PipelinesTab.vue', () => ({
  default: { template: '<section data-test="pipelines-tab-stub">pipelines</section>' },
}))

vi.mock('@/components/Overview/ProjectIngressTab.vue', () => ({
  default: { template: '<section data-test="ingress-tab-stub">ingress</section>' },
}))

vi.mock('@/components/Settings/ProjectConfigSurface.vue', () => ({
  default: defineComponent({
    props: ['state'],
    emits: ['update:state'],
    setup(props, { emit }) {
      const draftName = ref(props.state?.draftName ?? 'dev')
      watch(draftName, value => emit('update:state', { draftName: value }), { immediate: true })
      return { draftName }
    },
    template: '<section data-test="project-config-surface"><input data-test="draft-name" v-model="draftName" /></section>',
  }),
}))

vi.mock('@/components/Workspace/RuntimeWorkbenchHeader.vue', () => ({
  default: { template: '<header data-test="runtime-header-stub" />' },
}))

vi.mock('@/components/Panel/PanelLayout.vue', () => ({
  default: { template: '<section data-test="panel-layout-stub" />' },
}))

describe('WorkspaceShell overview tab state', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('restores the active overview subtab and unsaved config draft after visiting a log tab', async () => {
    useAgentStore().projects = [{
      id: 'proj-1',
      name: 'Demo',
      root_path: '/tmp/demo',
      services: [],
      environments: [{ id: 'env-dev', name: 'dev', is_dev: true, order: 0 }],
    }]
    const workspace = useWorkspaceStore()
    const overview = workspace.openProjectOverview('proj-1')
    const logTab = workspace.ensureProjectTab('proj-1')

    const wrapper = mount(WorkspaceShell, { global: { plugins: [installTestI18n()] } })

    await wrapper.find('[data-test="overview-tab-config"]').trigger('click')
    await wrapper.find('[data-test="draft-name"]').setValue('changed-but-unsaved')

    workspace.activateTab(logTab.id)
    await flushPromises()
    expect(wrapper.find('[data-test="panel-layout-stub"]').exists()).toBe(true)

    workspace.activateTab(overview.id)
    await flushPromises()

    expect(wrapper.find('[data-test="overview-tab-config"]').classes()).toContain('active')
    expect((wrapper.find('[data-test="draft-name"]').element as HTMLInputElement).value).toBe('changed-but-unsaved')
  })
})
