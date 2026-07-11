/**
 * PanelLayout 组件分栏拖拽测试。
 *
 * 职责：
 *   - 验证 split divider 拖拽会更新 panelStore 中对应 split 的 ratio
 *
 * 边界：
 *   - PanelLeaf 使用轻量 stub，不验证日志面板渲染
 *   - 不覆盖布局持久化细节，持久化由 panelStore 单元测试负责
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import PanelLayout from '../PanelLayout.vue'
import { usePanelStore, type PanelSplitNode } from '@/stores/panel'

const panelLeafInstances = vi.hoisted(() => ({ nextId: 0 }))

vi.mock('../PanelLeaf.vue', () => ({
  default: {
    props: ['panelId', 'source'],
    setup() {
      return { instanceId: ++panelLeafInstances.nextId }
    },
    template: `
      <section
        data-test="panel-leaf"
        :data-instance-id="instanceId"
        :data-deployment-id="source?.deploymentId || ''"
      >{{ panelId }}</section>
    `,
  },
}))

function installSplitRoot(axis: 'h' | 'v') {
  const panelStore = usePanelStore()
  const [first] = panelStore.allLeaves
  panelStore.setRoot({
    type: 'split',
    id: 'root-split',
    axis,
    ratio: 0.5,
    first: { ...first, id: 'leaf-a', serviceId: 'dep-A', projectId: null, source: { type: 'deployment', deploymentId: 'dep-A' } },
    second: { ...first, id: 'leaf-b', serviceId: 'dep-B', projectId: null, source: { type: 'deployment', deploymentId: 'dep-B' } },
  })
  return panelStore
}

describe('PanelLayout', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
    panelLeafInstances.nextId = 0
  })

  it('replaces the leaf component instance when the workspace layout switches to another leaf', async () => {
    const panelStore = usePanelStore()
    panelStore.setRoot({
      type: 'leaf',
      id: 'leaf-a',
      serviceId: 'dep-A',
      projectId: null,
      source: { type: 'deployment', deploymentId: 'dep-A' },
    })
    const wrapper = mount(PanelLayout)
    const firstInstanceId = wrapper.find('[data-test="panel-leaf"]').attributes('data-instance-id')

    panelStore.setRoot({
      type: 'leaf',
      id: 'leaf-b',
      serviceId: 'dep-B',
      projectId: null,
      source: { type: 'deployment', deploymentId: 'dep-B' },
    })
    await nextTick()

    const nextLeaf = wrapper.find('[data-test="panel-leaf"]')
    expect(nextLeaf.text()).toBe('leaf-b')
    expect(nextLeaf.attributes('data-deployment-id')).toBe('dep-B')
    // 不同 leaf 必须拥有独立组件状态，否则 LogPanel 的显示缓存与 virtualizer 会跨 tab 串扰。
    expect(nextLeaf.attributes('data-instance-id')).not.toBe(firstInstanceId)
  })

  it('drags horizontal split divider by pointer x to update column ratio', async () => {
    const panelStore = installSplitRoot('h')
    const wrapper = mount(PanelLayout)
    const split = wrapper.find('[data-test="panel-split"]').element as HTMLElement
    vi.spyOn(split, 'getBoundingClientRect').mockReturnValue({
      left: 100,
      top: 50,
      width: 400,
      height: 300,
      right: 500,
      bottom: 350,
      x: 100,
      y: 50,
      toJSON: () => ({}),
    })

    await wrapper.find('[data-test="split-divider"]').trigger('mousedown', { clientX: 300, clientY: 150 })
    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 380, clientY: 150 }))
    window.dispatchEvent(new MouseEvent('mouseup'))

    expect((panelStore.root as PanelSplitNode).ratio).toBeCloseTo(0.7)
  })

  it('drags vertical split divider by pointer y to update row ratio', async () => {
    const panelStore = installSplitRoot('v')
    const wrapper = mount(PanelLayout)
    const split = wrapper.find('[data-test="panel-split"]').element as HTMLElement
    vi.spyOn(split, 'getBoundingClientRect').mockReturnValue({
      left: 100,
      top: 50,
      width: 400,
      height: 300,
      right: 500,
      bottom: 350,
      x: 100,
      y: 50,
      toJSON: () => ({}),
    })

    await wrapper.find('[data-test="split-divider"]').trigger('mousedown', { clientX: 300, clientY: 150 })
    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 300, clientY: 275 }))
    window.dispatchEvent(new MouseEvent('mouseup'))

    expect((panelStore.root as PanelSplitNode).ratio).toBeCloseTo(0.75)
  })
})
