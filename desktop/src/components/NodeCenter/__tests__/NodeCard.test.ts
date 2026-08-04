/**
 * NodeCard component tests.
 *
 * Responsibilities:
 *   - Verify a node card renders rich deployment status
 *   - Verify missing metrics degrade without breaking layout
 *   - Verify clicking a deployment row requests log opening
 *   - Verify the dev-machine badge and port-mirror section render from props only
 *     (Task 11), and that a conflict row emits instead of resolving anything itself
 *
 * Boundaries:
 *   - Does not read Pinia stores
 *   - Does not open workspace tabs
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NodeCard from '../NodeCard.vue'
import { installTestI18n } from '@/test-utils/i18n'
import type { NodeCenterNode } from '@/lib/nodeCenter'
import type { MirrorRowView } from '@/lib/portMirrorView'
import type { RuntimeInstanceStatus } from '@/api/agent'

function mirrorRow(overrides: Partial<MirrorRowView> = {}): MirrorRowView {
  return {
    port: 9100,
    state: 'active',
    label: ':9100 ⇄ 127.0.0.1:9100',
    conflict: false,
    openUrl: 'http://127.0.0.1:9100',
    hostId: 'host-1',
    hostName: 'ali-01',
    ...overrides,
  }
}

function instance(partial: Partial<RuntimeInstanceStatus> = {}): RuntimeInstanceStatus {
  return {
    service_id: 'svc-api',
    service_name: 'api',
    env_name: 'prod',
    deployment_id: 'dep-api',
    node_id: 'host-1',
    node_name: 'ali-01',
    is_local: false,
    metrics: {
      cpu_percent: 12.5,
      mem_bytes: 128 * 1024 * 1024,
      uptime_sec: 3661,
      restarts: 0,
      health: 'running',
      base: 'systemd',
    },
    ...partial,
  }
}

function card(overrides: Partial<NodeCenterNode> = {}): NodeCenterNode {
  return {
    hostId: 'host-1',
    name: 'ali-01',
    address: '10.0.0.8',
    reachable: true,
    muted: false,
    agent: {
      installed: true,
      version: '0.1.0',
      health: 'healthy',
      reachable: true,
    },
    deployments: [{ instance: instance(), envName: 'prod', projectName: 'Demo', abnormal: false }],
    serviceCount: 1,
    updatedAt: '2026-06-06T10:00:00Z',
    configured: true,
    mirrors: [],
    devMachine: false,
    desktopOnline: false,
    ...overrides,
  }
}

describe('NodeCard', () => {
  it('renders node header and rich service metrics', () => {
    const wrapper = mount(NodeCard, {
      props: { node: card() },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="node-card-host-1"]').classes()).toContain('is-reachable')
    expect(wrapper.text()).toContain('ali-01')
    expect(wrapper.text()).toContain('Remote · agent 0.1.0 · healthy · 1 service')
    expect(wrapper.text()).toContain('api')
    expect(wrapper.text()).toContain('Demo · prod')
    expect(wrapper.text()).toContain('12.5%')
    expect(wrapper.text()).toContain('128 MiB')
    expect(wrapper.text()).toContain('1h 1m')
    expect(wrapper.text()).toContain('0')
    expect(wrapper.find('[data-test="cpu-bar-dep-api"]').attributes('style')).toContain('width: 12.5%')
  })

  it('renders disconnected nodes and empty service lists', () => {
    const wrapper = mount(NodeCard, {
      props: {
        node: card({
          reachable: false,
          muted: true,
          agent: { installed: false, health: 'unknown', reachable: false },
          deployments: [],
          serviceCount: 0,
        }),
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="node-card-host-1"]').classes()).toContain('is-muted')
    expect(wrapper.text()).toContain('Disconnected')
    expect(wrapper.text()).toContain('No remote services')
  })

  it('uses dashes and hides the CPU bar when metrics are missing', () => {
    const wrapper = mount(NodeCard, {
      props: {
        node: card({
          deployments: [{
            envName: undefined,
            abnormal: true,
            instance: instance({
              error: 'process unavailable',
              metrics: {
                cpu_percent: null,
                mem_bytes: null,
                uptime_sec: null,
                restarts: null,
                health: 'unknown',
                base: 'systemd',
              },
            }),
          }],
        }),
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.text()).toContain('—')
    expect(wrapper.text()).toContain('process unavailable')
    expect(wrapper.find('[data-test="cpu-bar-dep-api"]').exists()).toBe(false)
  })

  it('renders abnormal services first based on the provided view model order', () => {
    const wrapper = mount(NodeCard, {
      props: {
        node: card({
          serviceCount: 2,
          deployments: [
            {
              abnormal: true,
              envName: 'prod',
              instance: instance({
                service_name: 'worker',
                deployment_id: 'dep-worker',
                metrics: { cpu_percent: null, mem_bytes: null, uptime_sec: null, restarts: 3, health: 'failed', base: 'systemd' },
              }),
            },
            { abnormal: false, envName: 'prod', instance: instance() },
          ],
        }),
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    const rows = wrapper.findAll('[data-test^="node-service-"]')
    expect(rows.map(row => row.text())).toEqual(expect.arrayContaining([
      expect.stringContaining('worker'),
      expect.stringContaining('api'),
    ]))
    expect(rows[0].text()).toContain('worker')
  })

  it('renders project and environment as secondary service metadata', () => {
    const wrapper = mount(NodeCard, {
      props: {
        node: card({
          deployments: [{
            abnormal: false,
            envName: 'prod',
            projectName: 'Billing API',
            instance: instance({
              service_name: 'server',
              deployment_id: 'dep-billing-server',
            }),
          }],
        }),
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    const row = wrapper.find('[data-test="node-service-dep-billing-server"]')
    expect(row.find('[data-test="service-name"]').text()).toBe('server')
    expect(row.find('[data-test="service-context"]').text()).toBe('Billing API · prod')
  })

  it('emits open logs with deployment and node ids', async () => {
    const wrapper = mount(NodeCard, {
      props: { node: card() },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="node-service-dep-api"]').trigger('click')

    expect(wrapper.emitted('open-logs')?.[0]).toEqual(['dep-api', 'host-1'])
  })

  it('renders current route and degraded marker', () => {
    const wrapper = mount(NodeCard, {
      props: { node: card({ route: { selectedIndex: 1, selectedType: 'tunnel', degraded: true } }) },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="node-route-badge"]').text()).toBe('Tunnel · degraded')
  })

  it('renders the selected connection type as a localized badge beside the node name', () => {
    const wrapper = mount(NodeCard, {
      props: { node: card({ route: { selectedIndex: 0, selectedType: 'direct', degraded: false } }) },
      global: { plugins: [installTestI18n('zh-CN')] },
    })

    const titleRow = wrapper.find('.node-title-row')
    expect(titleRow.text()).toContain('ali-01')
    expect(titleRow.find('[data-test="node-route-badge"]').text()).toBe('直连')
    expect(wrapper.text()).not.toContain('via direct')
  })

  describe('端口镜像区与开发机标记（Task 11）', () => {
    it('devMachine 为 true 时 agent 摘要行追加 · 开发机 标记', () => {
      const wrapper = mount(NodeCard, {
        props: { node: card({ devMachine: true }) },
        global: { plugins: [installTestI18n('zh-CN')] },
      })

      expect(wrapper.text()).toContain('远程 · agent 0.1.0 · healthy · 1 个服务 · 开发机')
    })

    it('devMachine 为 false 时不追加开发机标记', () => {
      const wrapper = mount(NodeCard, {
        props: { node: card({ devMachine: false }) },
        global: { plugins: [installTestI18n('zh-CN')] },
      })

      expect(wrapper.text()).not.toContain('开发机')
    })

    it('devMachine 且有 mirrors 时渲染镜像区标题与逐行状态', () => {
      const wrapper = mount(NodeCard, {
        props: {
          node: card({
            devMachine: true,
            mirrors: [
              mirrorRow({ port: 9100, state: 'active', conflict: false }),
              mirrorRow({ port: 5173, state: 'conflict', conflict: true }),
            ],
          }),
        },
        global: { plugins: [installTestI18n('zh-CN')] },
      })

      const section = wrapper.find('[data-test="node-mirror-section"]')
      expect(section.exists()).toBe(true)
      expect(section.text()).toContain('端口镜像（本机 ⇄ ali-01）')

      const activeRow = wrapper.find('[data-test="node-mirror-row-9100"]')
      expect(activeRow.text()).toContain('127.0.0.1:9100 ⇄ :9100')
      expect(activeRow.text()).toContain('已镜像')

      const conflictRow = wrapper.find('[data-test="node-mirror-row-5173"]')
      expect(conflictRow.text()).toContain('127.0.0.1:5173 ⇄ :5173')
      expect(conflictRow.text()).toContain('冲突 · 本机端口被占')
    })

    it('devMachine 为 false 时即使传入 mirrors 也不渲染镜像区（节点卡不做本机判定，只服从 props）', () => {
      const wrapper = mount(NodeCard, {
        props: { node: card({ devMachine: false, mirrors: [mirrorRow()] }) },
        global: { plugins: [installTestI18n('zh-CN')] },
      })

      expect(wrapper.find('[data-test="node-mirror-section"]').exists()).toBe(false)
    })

    it('devMachine 为 true 但 mirrors 为空时不渲染镜像区（不展示空标题）', () => {
      const wrapper = mount(NodeCard, {
        props: { node: card({ devMachine: true, mirrors: [] }) },
        global: { plugins: [installTestI18n('zh-CN')] },
      })

      expect(wrapper.find('[data-test="node-mirror-section"]').exists()).toBe(false)
    })

    it('点击冲突行 emit mirror-conflict-click，携带 hostId/port', async () => {
      const wrapper = mount(NodeCard, {
        props: {
          node: card({
            devMachine: true,
            mirrors: [mirrorRow({ port: 5173, state: 'conflict', conflict: true, hostId: 'host-1' })],
          }),
        },
        global: { plugins: [installTestI18n('zh-CN')] },
      })

      await wrapper.find('[data-test="node-mirror-row-5173"]').trigger('click')

      expect(wrapper.emitted('mirror-conflict-click')?.[0]).toEqual([{ hostId: 'host-1', port: 5173 }])
    })

    it('点击非冲突（active）行不 emit mirror-conflict-click', async () => {
      const wrapper = mount(NodeCard, {
        props: {
          node: card({
            devMachine: true,
            mirrors: [mirrorRow({ port: 9100, state: 'active', conflict: false })],
          }),
        },
        global: { plugins: [installTestI18n('zh-CN')] },
      })

      await wrapper.find('[data-test="node-mirror-row-9100"]').trigger('click')

      expect(wrapper.emitted('mirror-conflict-click')).toBeFalsy()
    })

    it('pending/failed 状态行也能渲染出对应文案，不留空白', () => {
      const wrapper = mount(NodeCard, {
        props: {
          node: card({
            devMachine: true,
            mirrors: [
              mirrorRow({ port: 9300, state: 'pending', conflict: false, openUrl: undefined }),
              mirrorRow({ port: 9400, state: 'failed', conflict: false, openUrl: undefined }),
            ],
          }),
        },
        global: { plugins: [installTestI18n('zh-CN')] },
      })

      expect(wrapper.find('[data-test="node-mirror-row-9300"]').text()).toContain('建立中')
      expect(wrapper.find('[data-test="node-mirror-row-9400"]').text()).toContain('镜像失败')
    })
  })

  // 桌面端在线徽标（Task 10）：本组件不判定/不持有 host 是否为本机——「仅远程节点
  // 显示」完全由上游 buildNodeCenterNodes()（desktop/src/lib/nodeCenter.ts）的
  // remote-only 过滤保证（is_self host 与 host_id === 'local' 的快照恒不产出节点卡，
  // 见 nodeCenter.test.ts「本机 (is_self) host 不会产出节点卡」用例），本组件只需
  // 无条件服从 props.node.desktopOnline，不重复判定，同端口镜像区的既有约定。
  describe('桌面端在线徽标（Task 10）', () => {
    it('desktopOnline 为 true 时渲染徽标，复用原型样式类 node-route-badge desktop-online', () => {
      const wrapper = mount(NodeCard, {
        props: { node: card({ desktopOnline: true }) },
        global: { plugins: [installTestI18n('zh-CN')] },
      })

      const badge = wrapper.find('[data-test="node-desktop-online-badge"]')
      expect(badge.exists()).toBe(true)
      expect(badge.classes()).toEqual(expect.arrayContaining(['node-route-badge', 'desktop-online']))
      expect(badge.text()).toBe('桌面端在线')
    })

    it('desktopOnline 为 false 时不渲染徽标', () => {
      const wrapper = mount(NodeCard, {
        props: { node: card({ desktopOnline: false }) },
        global: { plugins: [installTestI18n('zh-CN')] },
      })

      expect(wrapper.find('[data-test="node-desktop-online-badge"]').exists()).toBe(false)
    })
  })
})
