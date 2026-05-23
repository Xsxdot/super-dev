/**
 * PanelLeaf 组件测试
 *
 * 职责：
 *   - 验证服务拖放到面板边缘时会创建分栏
 *   - 覆盖 drop 时根据当前位置重新判定落点的交互路径
 *
 * 边界：
 *   - 不渲染真实日志内容，LogPanel 使用轻量 stub
 *   - 不测试拖拽源组件，dataTransfer 由测试替身提供
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PanelLeaf from '../PanelLeaf.vue'
import ServiceRow from '../../Sidebar/ServiceRow.vue'
import RemoteLogSourceRow from '../../Sidebar/RemoteLogSourceRow.vue'
import ProjectRemoteSection from '../../Sidebar/ProjectRemoteSection.vue'
import { useAgentStore } from '../../../stores/agent'
import { useRemoteStore } from '../../../stores/remote'
import { usePanelStore, type PanelNode, type PanelSplitNode } from '../../../stores/panel'
import type { Host, LogSource, Project, Service } from '../../../api/agent'

function expectSplitNode(node: PanelNode): PanelSplitNode {
  expect(node.type).toBe('split')
  if (node.type !== 'split') throw new Error('expected split node')
  return node
}

function makeService(): Service {
  return {
    id: 'svc-1',
    project_id: 'proj-1',
    name: 'api',
    status: 'running',
    command: 'pnpm dev',
    work_dir: '/tmp/project',
    required: false,
    order: 1,
  }
}

function makeProject(service: Service): Project {
  return {
    id: 'proj-1',
    name: 'Project',
    root_path: '/tmp/project',
    services: [service],
    selected_service_ids: [],
  }
}

function makeHost(): Host {
  return {
    id: 'host-1',
    name: 'Host 1',
    ssh_host: '10.0.0.1',
    ssh_port: 22,
    ssh_user: 'root',
    remote_agent_port: 57017,
    local_tunnel_port: 57018,
    tags: ['prod'],
  }
}

function makeLogSource(id = 'remote-prod'): LogSource {
  return {
    id,
    name: 'Remote Prod',
    type: 'docker',
    host_ids: ['host-1'],
    tags: ['prod'],
    extra_args: [],
    project_id: 'proj-1',
    service_id: 'svc-1',
  }
}

function stubPanelRect(wrapper: ReturnType<typeof mount>) {
  const panelEl = wrapper.find('.panel-leaf').element as HTMLElement
  vi.spyOn(panelEl, 'getBoundingClientRect').mockReturnValue({
    left: 0,
    top: 0,
    width: 400,
    height: 300,
    right: 400,
    bottom: 300,
    x: 0,
    y: 0,
    toJSON: () => ({}),
  } as DOMRect)
}

describe('PanelLeaf', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('drop 时即使高亮被 dragleave 清空，也按当前右边缘位置创建分栏', async () => {
    const panelStore = usePanelStore()
    const agentStore = useAgentStore()
    const service = makeService()
    agentStore.projects = [makeProject(service)]

    const wrapper = mount(PanelLeaf, {
      props: {
        panelId: panelStore.root.id,
        serviceId: null,
        projectId: null,
        canClose: false,
      },
      global: {
        stubs: {
          LogPanel: { template: '<div class="log-panel-stub" />' },
        },
      },
    })

    const panelEl = wrapper.find('.panel-leaf').element as HTMLElement
    vi.spyOn(panelEl, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      top: 0,
      width: 400,
      height: 300,
      right: 400,
      bottom: 300,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)

    await wrapper.find('.panel-leaf').trigger('dragover', { clientX: 380, clientY: 150 })
    await wrapper.find('.panel-leaf').trigger('dragleave')
    await wrapper.find('.panel-leaf').trigger('drop', {
      clientX: 380,
      clientY: 150,
      dataTransfer: {
        getData: (type: string) => (type === 'text/plain' ? service.id : ''),
      },
    })

    const split = expectSplitNode(panelStore.root)
    expect(split.axis).toBe('h')
    expect(split.second.type).toBe('leaf')
    if (split.second.type === 'leaf') {
      expect(split.second.serviceId).toBe(service.id)
      expect(split.second.projectId).toBe(service.project_id)
    }
  })

  it('拖拽在面板内部子元素之间移动时不清空高亮', async () => {
    const panelStore = usePanelStore()

    const wrapper = mount(PanelLeaf, {
      props: {
        panelId: panelStore.root.id,
        serviceId: null,
        projectId: null,
        canClose: false,
      },
      global: {
        stubs: {
          LogPanel: { template: '<div class="log-panel-stub" />' },
        },
      },
    })

    const panelEl = wrapper.find('.panel-leaf').element as HTMLElement
    vi.spyOn(panelEl, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      top: 0,
      width: 400,
      height: 300,
      right: 400,
      bottom: 300,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)

    await wrapper.find('.panel-leaf').trigger('dragover', { clientX: 380, clientY: 150 })
    expect(wrapper.find('.drop-overlay').exists()).toBe(true)

    await wrapper.find('.panel-leaf').trigger('dragleave', { clientX: 220, clientY: 150 })

    expect(wrapper.find('.drop-overlay').exists()).toBe(true)
  })

  it('从服务行拖到面板右边缘时显示高亮并创建分栏', async () => {
    const panelStore = usePanelStore()
    const agentStore = useAgentStore()
    const service = makeService()
    agentStore.projects = [makeProject(service)]

    const wrapper = mount({
      components: { PanelLeaf, ServiceRow },
      template: `
        <div>
          <ServiceRow :service="service" project-id="proj-1" :selected="false" />
          <PanelLeaf
            :panel-id="panelId"
            :service-id="null"
            :project-id="null"
            :can-close="false"
          />
        </div>
      `,
      setup() {
        return { service, panelId: panelStore.root.id }
      },
    }, {
      global: {
        stubs: {
          LogPanel: { template: '<div class="log-panel-stub" />' },
        },
      },
    })

    const panelEl = wrapper.find('.panel-leaf').element as HTMLElement
    vi.spyOn(panelEl, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      top: 0,
      width: 400,
      height: 300,
      right: 400,
      bottom: 300,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)

    wrapper.find('.service-row').element.dispatchEvent(
      new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 10, clientY: 10 }),
    )
    await wrapper.vm.$nextTick()
    document.dispatchEvent(new MouseEvent('pointermove', { clientX: 380, clientY: 150, buttons: 1 }))
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.drop-overlay').exists()).toBe(true)

    document.dispatchEvent(new MouseEvent('pointerup', { clientX: 380, clientY: 150 }))
    await wrapper.vm.$nextTick()

    const split = expectSplitNode(panelStore.root)
    expect(split.axis).toBe('h')
    expect(split.second.type).toBe('leaf')
    if (split.second.type === 'leaf') {
      expect(split.second.serviceId).toBe(service.id)
    }
  })

  it('重复拖入已打开的远程监听时聚焦已有分栏且不重复创建', async () => {
    const panelStore = usePanelStore()
    const localLeafId = panelStore.root.id
    const remoteSource = {
      type: 'remote-log-source' as const,
      logSourceId: 'remote-prod',
      groupKey: 'all',
    }

    panelStore.replaceSource(localLeafId, {
      type: 'local-service',
      projectId: 'project-A',
      serviceId: 'svc-api',
    })
    panelStore.splitLeafWithSource(localLeafId, 'h', remoteSource, 'first')

    const remoteLeaf = panelStore.allLeaves.find(leaf => leaf.source?.type === 'remote-log-source')
    const localLeaf = panelStore.allLeaves.find(leaf => leaf.source?.type === 'local-service')
    expect(remoteLeaf).toBeTruthy()
    expect(localLeaf).toBeTruthy()
    panelStore.setFocus(localLeaf!.id)

    const wrapper = mount(PanelLeaf, {
      props: {
        panelId: localLeaf!.id,
        serviceId: localLeaf!.serviceId,
        projectId: localLeaf!.projectId,
        source: localLeaf!.source,
        canClose: true,
      },
      global: {
        stubs: {
          LogPanel: { template: '<div class="log-panel-stub" />' },
        },
      },
    })

    const panelEl = wrapper.find('.panel-leaf').element as HTMLElement
    vi.spyOn(panelEl, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      top: 0,
      width: 400,
      height: 300,
      right: 400,
      bottom: 300,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)

    await wrapper.find('.panel-leaf').trigger('drop', {
      clientX: 380,
      clientY: 150,
      dataTransfer: {
        getData: (type: string) => (
          type === 'application/superdev-panel-source' ? JSON.stringify(remoteSource) : ''
        ),
      },
    })

    expect(panelStore.allLeaves).toHaveLength(2)
    expect(panelStore.allLeaves.filter(leaf => leaf.source?.type === 'remote-log-source')).toHaveLength(1)
    expect(panelStore.focusedPanelId).toBe(remoteLeaf!.id)
  })

  it('忽略 malformed panel source payload 且不改变布局', async () => {
    const panelStore = usePanelStore()
    const initialRoot = JSON.stringify(panelStore.root)

    const wrapper = mount(PanelLeaf, {
      props: {
        panelId: panelStore.root.id,
        serviceId: null,
        projectId: null,
        canClose: false,
      },
      global: {
        stubs: {
          LogPanel: { template: '<div class="log-panel-stub" />' },
        },
      },
    })
    stubPanelRect(wrapper)

    await wrapper.find('.panel-leaf').trigger('dragover', { clientX: 380, clientY: 150 })
    await wrapper.find('.panel-leaf').trigger('drop', {
      clientX: 380,
      clientY: 150,
      dataTransfer: {
        getData: (type: string) => (
          type === 'application/superdev-panel-source' ? '{"type":' : ''
        ),
      },
    })

    expect(JSON.stringify(panelStore.root)).toBe(initialRoot)
    expect(wrapper.find('.drop-overlay').exists()).toBe(false)
  })

  it('忽略非法 panel source shape 且不改变布局', async () => {
    const panelStore = usePanelStore()
    const initialRoot = JSON.stringify(panelStore.root)

    const wrapper = mount(PanelLeaf, {
      props: {
        panelId: panelStore.root.id,
        serviceId: null,
        projectId: null,
        canClose: false,
      },
      global: {
        stubs: {
          LogPanel: { template: '<div class="log-panel-stub" />' },
        },
      },
    })
    stubPanelRect(wrapper)

    await wrapper.find('.panel-leaf').trigger('drop', {
      clientX: 380,
      clientY: 150,
      dataTransfer: {
        getData: (type: string) => (
          type === 'application/superdev-panel-source'
            ? JSON.stringify({ type: 'remote-log-source', groupKey: 'all' })
            : ''
        ),
      },
    })

    expect(JSON.stringify(panelStore.root)).toBe(initialRoot)
    expect(wrapper.find('.drop-overlay').exists()).toBe(false)
  })

  it('从远程监听分组 pointer 拖到面板右边缘时显示高亮并创建分栏', async () => {
    const panelStore = usePanelStore()
    const remoteStore = useRemoteStore()
    const logSource = makeLogSource()
    remoteStore.hosts = [makeHost()]
    remoteStore.logSources = [logSource]

    const wrapper = mount({
      components: { PanelLeaf, RemoteLogSourceRow },
      template: `
        <div>
          <RemoteLogSourceRow
            :log-source="logSource"
            @open="() => {}"
            @edit="() => {}"
            @delete="() => {}"
          />
          <PanelLeaf
            :panel-id="panelId"
            :service-id="null"
            :project-id="null"
            :can-close="false"
          />
        </div>
      `,
      setup() {
        return { logSource, panelId: panelStore.root.id }
      },
    }, {
      global: {
        stubs: {
          LogPanel: { template: '<div class="log-panel-stub" />' },
        },
      },
    })
    stubPanelRect(wrapper)

    wrapper.find('[data-test="logsource-group"]').element.dispatchEvent(
      new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 10, clientY: 10 }),
    )
    await wrapper.vm.$nextTick()
    document.dispatchEvent(new MouseEvent('pointermove', { clientX: 380, clientY: 150, buttons: 1 }))
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.drop-overlay').exists()).toBe(true)

    document.dispatchEvent(new MouseEvent('pointerup', { clientX: 380, clientY: 150 }))
    await wrapper.vm.$nextTick()

    const split = expectSplitNode(panelStore.root)
    expect(split.axis).toBe('h')
    expect(split.second.type).toBe('leaf')
    if (split.second.type === 'leaf') {
      expect(split.second.source).toEqual({
        type: 'remote-log-source',
        logSourceId: logSource.id,
        groupKey: 'all',
      })
    }
  })

  it('远程监听分组所有节点连接失败时 drop 不新增分栏并提示失败', async () => {
    const panelStore = usePanelStore()
    const remoteStore = useRemoteStore()
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    remoteStore.hosts = [makeHost()]
    remoteStore.logSources = [makeLogSource('remote-failed')]
    remoteStore.applyTunnelUpdate({ host_id: 'host-1', state: 'failed', error: 'Permission denied' })
    const initialRoot = JSON.stringify(panelStore.root)

    const wrapper = mount(PanelLeaf, {
      props: {
        panelId: panelStore.root.id,
        serviceId: null,
        projectId: null,
        canClose: false,
      },
      global: {
        stubs: {
          LogPanel: { template: '<div class="log-panel-stub" />' },
        },
      },
    })
    stubPanelRect(wrapper)

    await wrapper.find('.panel-leaf').trigger('drop', {
      clientX: 380,
      clientY: 150,
      dataTransfer: {
        getData: (type: string) => (
          type === 'application/superdev-panel-source'
            ? JSON.stringify({ type: 'remote-log-source', logSourceId: 'remote-failed', groupKey: 'all' })
            : ''
        ),
      },
    })

    expect(JSON.stringify(panelStore.root)).toBe(initialRoot)
    expect(alertSpy).toHaveBeenCalledWith(expect.stringContaining('无法打开远程监听日志'))
    expect(alertSpy).toHaveBeenCalledWith(expect.stringContaining('Permission denied'))
  })

  it('达到最大分栏数时 drop 新远程监听不新增分栏并提示上限', async () => {
    const panelStore = usePanelStore()
    const remoteStore = useRemoteStore()
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    remoteStore.hosts = [makeHost()]
    remoteStore.logSources = [makeLogSource('remote-new')]

    const firstLeafId = panelStore.root.id
    panelStore.replaceSource(firstLeafId, { type: 'local-service', projectId: 'proj-1', serviceId: 'svc-1' })
    panelStore.splitLeafWithSource(firstLeafId, 'h', { type: 'local-service', projectId: 'proj-1', serviceId: 'svc-2' }, 'second')
    panelStore.splitLeafWithSource(panelStore.allLeaves[1].id, 'h', { type: 'local-service', projectId: 'proj-1', serviceId: 'svc-3' }, 'second')
    panelStore.splitLeafWithSource(panelStore.allLeaves[2].id, 'h', { type: 'local-service', projectId: 'proj-1', serviceId: 'svc-4' }, 'second')
    expect(panelStore.allLeaves).toHaveLength(4)
    const initialRoot = JSON.stringify(panelStore.root)
    const targetLeaf = panelStore.allLeaves[0]

    const wrapper = mount(PanelLeaf, {
      props: {
        panelId: targetLeaf.id,
        serviceId: targetLeaf.serviceId,
        projectId: targetLeaf.projectId,
        source: targetLeaf.source,
        canClose: true,
      },
      global: {
        stubs: {
          LogPanel: { template: '<div class="log-panel-stub" />' },
        },
      },
    })
    stubPanelRect(wrapper)

    await wrapper.find('.panel-leaf').trigger('drop', {
      clientX: 380,
      clientY: 150,
      dataTransfer: {
        getData: (type: string) => (
          type === 'application/superdev-panel-source'
            ? JSON.stringify({ type: 'remote-log-source', logSourceId: 'remote-new', groupKey: 'all' })
            : ''
        ),
      },
    })

    expect(JSON.stringify(panelStore.root)).toBe(initialRoot)
    expect(panelStore.allLeaves).toHaveLength(4)
    expect(alertSpy).toHaveBeenCalledWith(expect.stringContaining('已达到最大分栏数'))
  })

  it('从项目远程聚合分组 pointer 拖到面板右边缘时显示高亮并创建分栏', async () => {
    const panelStore = usePanelStore()
    const agentStore = useAgentStore()
    const remoteStore = useRemoteStore()
    const service = makeService()
    agentStore.projects = [makeProject(service)]
    remoteStore.hosts = [makeHost()]
    remoteStore.logSources = [makeLogSource('remote-a'), makeLogSource('remote-b')]

    const wrapper = mount({
      components: { PanelLeaf, ProjectRemoteSection },
      template: `
        <div>
          <ProjectRemoteSection project-id="proj-1" />
          <PanelLeaf
            :panel-id="panelId"
            :service-id="null"
            :project-id="null"
            :can-close="false"
          />
        </div>
      `,
      setup() {
        return { panelId: panelStore.root.id }
      },
    }, {
      global: {
        stubs: {
          LogPanel: { template: '<div class="log-panel-stub" />' },
        },
      },
    })
    stubPanelRect(wrapper)

    wrapper.find('[data-test="project-remote-group"]').element.dispatchEvent(
      new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 10, clientY: 10 }),
    )
    await wrapper.vm.$nextTick()
    document.dispatchEvent(new MouseEvent('pointermove', { clientX: 380, clientY: 150, buttons: 1 }))
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.drop-overlay').exists()).toBe(true)

    document.dispatchEvent(new MouseEvent('pointerup', { clientX: 380, clientY: 150 }))
    await wrapper.vm.$nextTick()

    const split = expectSplitNode(panelStore.root)
    expect(split.axis).toBe('h')
    expect(split.second.type).toBe('leaf')
    if (split.second.type === 'leaf') {
      expect(split.second.source).toEqual({
        type: 'remote-aggregate',
        projectId: 'proj-1',
        serviceId: 'svc-1',
        serviceName: 'api',
        logSourceIds: ['remote-a', 'remote-b'],
        groupKey: 'all',
      })
    }
  })

  it('服务行拖拽期间临时禁用文字选择，松开后恢复', async () => {
    const panelStore = usePanelStore()
    const agentStore = useAgentStore()
    const service = makeService()
    agentStore.projects = [makeProject(service)]
    const removeAllRanges = vi.fn()
    vi.spyOn(window, 'getSelection').mockReturnValue({
      removeAllRanges,
    } as unknown as Selection)

    const wrapper = mount({
      components: { PanelLeaf, ServiceRow },
      template: `
        <div>
          <ServiceRow :service="service" project-id="proj-1" :selected="false" />
          <PanelLeaf
            :panel-id="panelId"
            :service-id="null"
            :project-id="null"
            :can-close="false"
          />
        </div>
      `,
      setup() {
        return { service, panelId: panelStore.root.id }
      },
    }, {
      global: {
        stubs: {
          LogPanel: { template: '<div class="log-panel-stub" />' },
        },
      },
    })

    wrapper.find('.service-row').element.dispatchEvent(
      new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 10, clientY: 10 }),
    )
    await wrapper.vm.$nextTick()

    const moveEvent = new MouseEvent('pointermove', {
      clientX: 40,
      clientY: 40,
      buttons: 1,
      cancelable: true,
    })
    document.dispatchEvent(moveEvent)
    await wrapper.vm.$nextTick()

    expect(moveEvent.defaultPrevented).toBe(true)
    expect(document.body.classList.contains('service-dragging-no-select')).toBe(true)
    expect(removeAllRanges).toHaveBeenCalled()

    document.dispatchEvent(new MouseEvent('pointerup', { clientX: 40, clientY: 40 }))
    await wrapper.vm.$nextTick()

    expect(document.body.classList.contains('service-dragging-no-select')).toBe(false)
  })
})
